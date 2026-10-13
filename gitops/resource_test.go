package gitops_test

// resource_test.go pins gitops.Resource, the observer of one declared
// resource whose manifests a block-level handler commits:
//
//   - ObserveResource renders from the request the ref carries;
//   - it first checks head: files at head that differ from the render, or
//     stand only in part, are unknown naming the paths, and an empty
//     repository is absent with no notification read. Otherwise it reads
//     the app's newest notification, whatever its revision, and checks the
//     files at that notification's revision: when they match what head
//     holds, the notification judges the resource (succeeded is present or
//     absent, pending is unknown, failed is an error); when they do not,
//     Argo CD has not yet reported a revision carrying the change, which is
//     unknown. No notification at all is unknown. It never takes the
//     revision to judge from the ref's identity;
//   - another app's commits never change what one app observes.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/gitops"
	"github.com/mitstack-eu/operator-kit/v2/handler"
)

var _ handler.Observer = (*gitops.Resource)(nil)

func observe(t *testing.T, b *gitopsBlock, d contract.Desired) contract.Observation {
	t.Helper()
	obs, err := b.resource.ObserveResource(context.Background(), refFor(d))
	if err != nil {
		t.Fatalf("ObserveResource: %v", err)
	}
	return obs
}

func onCluster(d contract.Desired, cluster string) contract.Desired {
	d.Params = map[string]string{"appName": d.Params["appName"], "cluster": cluster}
	return d
}

func TestResource_ObserveResourceRendersFromTheRequestTheRefCarries(t *testing.T) {
	var seen []contract.Desired
	b := newBlock(func(d contract.Desired) (gitops.Rendered, error) {
		seen = append(seen, d)
		return renderNamespace(d)
	}, "app-alpha")
	commitApp(t, b, desiredFor("app-alpha"))
	seen = nil
	ref := refFor(desiredFor("app-alpha"))

	obs, err := b.resource.ObserveResource(context.Background(), ref)

	if err != nil || obs.State != contract.Present || !reflect.DeepEqual(obs.Outputs, alphaOutputs()) {
		t.Fatalf("ObserveResource = %+v, %v, want present with %v", obs, err, alphaOutputs())
	}
	if len(seen) == 0 {
		t.Fatal("ObserveResource never rendered")
	}
	for _, d := range seen {
		if !reflect.DeepEqual(d, ref.Desired) {
			t.Errorf("Render was handed %+v, want the request the ref carries, %+v", d, ref.Desired)
		}
	}
}

func TestResource_ObserveIsPresentWithTheOutputsWhenTheFilesStandAtHeadAndItsDeploySucceeded(t *testing.T) {
	b := newNamespaceBlock()
	commitApp(t, b, desiredFor("app-alpha"))

	obs := observe(t, b, desiredFor("app-alpha"))

	if obs.State != contract.Present || !reflect.DeepEqual(obs.Outputs, alphaOutputs()) {
		t.Errorf("Observe = %+v, want present with %v", obs, alphaOutputs())
	}
}

func TestResource_ObserveIsUnknownWithADetailWhileHeadsDeployIsPendingOrUnreported(t *testing.T) {
	for name, seed := range map[string]func(b *gitopsBlock, rev string){
		"no notification yet": func(*gitopsBlock, string) {},
		"syncing": func(b *gitopsBlock, rev string) {
			b.notes.Set(gitops.Notification{App: "app-alpha", Revision: rev, Phase: "Running", Health: "Progressing"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := newBlock(renderNamespace)
			rev := commitApp(t, b, desiredFor("app-alpha"))
			seed(b, rev)

			obs := observe(t, b, desiredFor("app-alpha"))

			if obs.State != contract.Unknown || obs.StatusDetail == "" {
				t.Errorf("Observe = %+v, want unknown with a detail", obs)
			}
		})
	}
}

func TestResource_ObserveFailsCarryingPhaseHealthAndMessageWhenHeadsDeployFailed(t *testing.T) {
	b := newBlock(renderNamespace)
	rev := commitApp(t, b, desiredFor("app-alpha"))
	b.notes.Set(gitops.Notification{App: "app-alpha", Revision: rev, Phase: "Failed", Health: "Degraded", Message: "namespace quota exceeded"})

	_, err := b.resource.ObserveResource(context.Background(), refFor(desiredFor("app-alpha")))

	if err == nil {
		t.Fatal("ObserveResource returned no error for a failed deploy")
	}
	for _, want := range []string{"Failed", "Degraded", "namespace quota exceeded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not carry %q", err, want)
		}
	}
}

func TestResource_ObserveJudgesTheNewestNotificationNotTheRevisionTheRefsIdentityRecords(t *testing.T) {
	b := newBlock(renderNamespace)
	stale := commitApp(t, b, desiredFor("app-alpha"))
	moved := onCluster(desiredFor("app-alpha"), "k3s-nbg1")
	head := commitApp(t, b, moved)
	b.notes.Set(gitops.Notification{App: "app-alpha", Revision: stale, Phase: "Failed", Health: "Degraded"})
	b.notes.Set(gitops.Notification{App: "app-alpha", Revision: head, Phase: "Succeeded", Health: "Healthy"})
	ref := refFor(moved)
	ref.Identity = map[string]string{"app": "app-alpha", "revision": stale}

	obs, err := b.resource.ObserveResource(context.Background(), ref)

	if err != nil || obs.State != contract.Present {
		t.Errorf("ObserveResource = %+v, %v, want present: head %s holds the files and its deploy succeeded", obs, err, head)
	}
}

func TestResource_ObserveIsAbsentOnAnEmptyRepository(t *testing.T) {
	b := newBlock(renderNamespace)

	if obs := observe(t, b, desiredFor("app-alpha")); obs.State != contract.Absent {
		t.Errorf("Observe = %+v, want absent", obs)
	}
	if got := b.notes.Reads("app-alpha", ""); got != 0 {
		t.Errorf("Observe read the app's newest notification %d time(s), want none: an empty repository is absent with no read", got)
	}
}

func TestResource_ObserveIsPresentWhenTheOnlyNotificationNamesALaterRevisionStillCarryingTheFiles(t *testing.T) {
	b := newBlock(renderNamespace)
	commitApp(t, b, desiredFor("app-alpha"))
	later := commitApp(t, b, desiredFor("app-beta")) // another app's commit lands after ours
	b.notes.Set(gitops.Notification{App: "app-alpha", Project: "app-alpha", Revision: later, Phase: "Succeeded", Health: "Healthy"})
	commitApp(t, b, desiredFor("app-gamma")) // head moves on again, with no notification for it yet

	obs := observe(t, b, desiredFor("app-alpha"))

	if obs.State != contract.Present || !reflect.DeepEqual(obs.Outputs, alphaOutputs()) {
		t.Errorf("Observe = %+v, want present with %v: Argo CD synced %s, which carries our files", obs, alphaOutputs(), later)
	}
}

func TestResource_ObserveIsUnknownWhenTheNewestNotificationNamesARevisionOlderThanTheChange(t *testing.T) {
	b := newBlock(renderNamespace)
	older := commitApp(t, b, desiredFor("app-alpha"))
	b.notes.Set(gitops.Notification{App: "app-alpha", Project: "app-alpha", Revision: older, Phase: "Succeeded", Health: "Healthy"})
	changed := onCluster(desiredFor("app-alpha"), "k3s-nbg1")
	commitApp(t, b, changed)

	obs, err := b.resource.ObserveResource(context.Background(), refFor(changed))

	if err != nil {
		t.Fatalf("ObserveResource: %v, want unknown, not an error", err)
	}
	if obs.State != contract.Unknown || !strings.Contains(obs.StatusDetail, "Argo CD") {
		t.Errorf("Observe = %+v, want unknown with a detail saying Argo CD has not yet reported a revision carrying the change", obs)
	}
}

func TestResource_ObserveAfterRemovalIsAbsentWhenTheOnlyNotificationNamesALaterRevisionWithoutTheFiles(t *testing.T) {
	b := newBlock(renderNamespace)
	commitApp(t, b, desiredFor("app-alpha"))
	removeApp(t, b, desiredFor("app-alpha"))
	later := commitApp(t, b, desiredFor("app-beta"))
	b.notes.Set(gitops.Notification{App: "app-alpha", Project: "app-alpha", Revision: later, Phase: "Succeeded", Health: "Healthy"})
	commitApp(t, b, desiredFor("app-gamma")) // head moves on again, with no notification for it yet

	if obs := observe(t, b, desiredFor("app-alpha")); obs.State != contract.Absent {
		t.Errorf("Observe = %+v, want absent: Argo CD synced %s, which no longer carries our files", obs, later)
	}
}

func TestResource_ObserveAfterRemovalIsUnknownWhenTheNewestNotificationNamesARevisionStillCarryingTheFiles(t *testing.T) {
	b := newBlock(renderNamespace)
	before := commitApp(t, b, desiredFor("app-alpha"))
	b.notes.Set(gitops.Notification{App: "app-alpha", Project: "app-alpha", Revision: before, Phase: "Succeeded", Health: "Healthy"})
	removeApp(t, b, desiredFor("app-alpha"))

	obs, err := b.resource.ObserveResource(context.Background(), refFor(desiredFor("app-alpha")))

	if err != nil {
		t.Fatalf("ObserveResource: %v, want unknown, not an error", err)
	}
	if obs.State != contract.Unknown || !strings.Contains(obs.StatusDetail, "Argo CD") {
		t.Errorf("Observe = %+v, want unknown with a detail saying Argo CD has not yet reported a revision carrying the removal", obs)
	}
}

func TestResource_ObserveAfterRemovalIsAbsentOnceHeadsDeploySucceeded(t *testing.T) {
	b := newNamespaceBlock()
	commitApp(t, b, desiredFor("app-alpha"))
	removeApp(t, b, desiredFor("app-alpha"))

	if obs := observe(t, b, desiredFor("app-alpha")); obs.State != contract.Absent {
		t.Errorf("Observe = %+v, want absent", obs)
	}
}

func TestResource_ObserveAfterRemovalIsUnknownWhilePendingAndFailsWhenTheDeployFailed(t *testing.T) {
	for _, c := range []struct {
		name          string
		phase, health string
		wantErr       bool
	}{
		{"no notification", "", "", false},
		{"pending", "Running", "Progressing", false},
		{"failed", "Error", "Degraded", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newBlock(renderNamespace)
			commitApp(t, b, desiredFor("app-alpha"))
			removeApp(t, b, desiredFor("app-alpha"))
			if c.phase != "" {
				b.notes.Set(gitops.Notification{App: "app-alpha", Revision: b.head(t), Phase: c.phase, Health: c.health, Message: "prune failed"})
			}

			obs, err := b.resource.ObserveResource(context.Background(), refFor(desiredFor("app-alpha")))

			if c.wantErr {
				if err == nil || !strings.Contains(err.Error(), "prune failed") {
					t.Errorf("err = %v, want one carrying the notification's message", err)
				}
				return
			}
			if err != nil || obs.State != contract.Unknown || obs.StatusDetail == "" {
				t.Errorf("Observe = %+v, %v, want unknown with a detail", obs, err)
			}
		})
	}
}

func TestResource_ObserveIsUnknownNamingThePathWhenOnlySomeFilesStand(t *testing.T) {
	b := newNamespaceBlock()
	rev := commitApp(t, b, desiredFor("app-alpha"))
	if _, err := b.repo.Commit(context.Background(), rev, map[string][]byte{alphaServiceAccountPath: nil}, "hand edit"); err != nil {
		t.Fatalf("hand edit: %v", err)
	}

	obs := observe(t, b, desiredFor("app-alpha"))

	if obs.State != contract.Unknown || !strings.Contains(obs.StatusDetail, alphaServiceAccountPath) {
		t.Errorf("Observe = %+v, want unknown naming %s", obs, alphaServiceAccountPath)
	}
}

func TestResource_ObserveIsUnknownNamingThePathWhenAFileStandsWithOtherContent(t *testing.T) {
	b := newNamespaceBlock()
	rev := commitApp(t, b, desiredFor("app-alpha"))
	if _, err := b.repo.Commit(context.Background(), rev, map[string][]byte{alphaNamespacePath: []byte("kind: Namespace\n# drifted\n")}, "hand edit"); err != nil {
		t.Fatalf("hand edit: %v", err)
	}

	obs := observe(t, b, desiredFor("app-alpha"))

	if obs.State != contract.Unknown || !strings.Contains(obs.StatusDetail, alphaNamespacePath) {
		t.Errorf("Observe = %+v, want unknown naming %s", obs, alphaNamespacePath)
	}
}

func TestResource_ObserveFailsNamingTheRenderWhenTheRequestCannotBeRendered(t *testing.T) {
	b := newNamespaceBlock()
	d := desiredFor("app-alpha")
	d.Params = map[string]string{"appName": "app-alpha"}

	_, err := b.resource.ObserveResource(context.Background(), refFor(d))

	if err == nil || !strings.Contains(err.Error(), "rendering") || !strings.Contains(err.Error(), "no cluster param") {
		t.Errorf("ObserveResource error = %v, want one saying the render failed and why", err)
	}
	if got := len(b.repo.Commits()); got != 0 {
		t.Errorf("Commits = %d, want 0: an observe never writes", got)
	}
}

func TestResource_AnotherAppsCommitsLeaveTheObservedAppPresent(t *testing.T) {
	b := newNamespaceBlock()
	commitApp(t, b, desiredFor("app-alpha"))
	alpha := renderApp("app-alpha", clusterParam).Files

	commitApp(t, b, desiredFor("app-beta"))
	removeApp(t, b, desiredFor("app-beta"))

	wantFilesAt(t, b.repo, b.head(t), alpha)
	if obs := observe(t, b, desiredFor("app-alpha")); obs.State != contract.Present {
		t.Errorf("app-alpha observed %+v after app-beta came and went, want present", obs)
	}
}

func TestResource_ARepositoryThatCannotBeReachedFailsTheObserve(t *testing.T) {
	b := newNamespaceBlock()
	b.resource.Repository = func(context.Context, contract.Desired) (gitops.Repository, error) {
		return nil, errors.New("the GitOps repository is unreachable")
	}

	_, err := b.resource.ObserveResource(context.Background(), refFor(desiredFor("app-alpha")))

	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("ObserveResource error = %v, want the unreachable repository named", err)
	}
}
