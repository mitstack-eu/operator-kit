package gitops_test

// Resource judges the app's newest notification once the files at its revision stand as they do at head, and never takes the revision from the ref's identity.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/gitops"
	"github.com/mitstack-eu/operator-kit/v3/handler"
)

var _ handler.Observer = (*gitops.Resource)(nil)

func onCluster(d contract.Desired, cluster string) contract.Desired {
	d.Params = map[string]string{"appName": d.Params["appName"], "cluster": cluster}
	return d
}

func alpha() contract.Desired { return desiredFor("app-alpha") }

func succeededAt(b *gitopsBlock, revision string) {
	b.notes.Set(gitops.Notification{App: "app-alpha", Project: "app-alpha", Revision: revision, Phase: "Succeeded", Health: "Healthy"})
}

func TestResource_ObserveResource(t *testing.T) {
	cases := []struct {
		name string
		// auto makes every revision of app-alpha and app-beta succeed.
		auto       bool
		arrange    func(t *testing.T, b *gitopsBlock) contract.ResourceRef
		wantState  contract.State
		wantDetail string
		wantErr    []string
		check      func(t *testing.T, b *gitopsBlock)
	}{
		{"files standing at head whose deploy succeeded are present with the outputs", true, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			commitApp(t, b, alpha())
			return refFor(alpha())
		}, contract.Present, "", nil, nil},
		{"head's deploy not yet reported is unknown with a detail", false, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			commitApp(t, b, alpha())
			return refFor(alpha())
		}, contract.Unknown, "", nil, nil},
		{"head's deploy still syncing is unknown with a detail", false, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			b.notes.Set(gitops.Notification{App: "app-alpha", Revision: commitApp(t, b, alpha()), Phase: "Running", Health: "Progressing"})
			return refFor(alpha())
		}, contract.Unknown, "", nil, nil},
		{"head's failed deploy fails carrying phase, health and message", false, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			b.notes.Set(gitops.Notification{App: "app-alpha", Revision: commitApp(t, b, alpha()), Phase: "Failed", Health: "Degraded", Message: "namespace quota exceeded"})
			return refFor(alpha())
		}, "", "", []string{"Failed", "Degraded", "namespace quota exceeded"}, nil},
		{"the newest notification is judged, not the revision the ref's identity records", false, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			stale := commitApp(t, b, alpha())
			moved := onCluster(alpha(), "k3s-nbg1")
			b.notes.Set(gitops.Notification{App: "app-alpha", Revision: stale, Phase: "Failed", Health: "Degraded"})
			succeededAt(b, commitApp(t, b, moved))
			ref := refFor(moved)
			ref.Identity = map[string]string{"app": "app-alpha", "revision": stale}
			return ref
		}, contract.Present, "", nil, nil},
		{"an empty repository is absent with no notification read", false, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			return refFor(alpha())
		}, contract.Absent, "", nil, func(t *testing.T, b *gitopsBlock) {
			if got := b.notes.Reads("app-alpha", ""); got != 0 {
				t.Errorf("read the app's newest notification %d time(s), want none", got)
			}
		}},
		{"a notification naming a later revision still carrying the files is present", false, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			commitApp(t, b, alpha())
			succeededAt(b, commitApp(t, b, desiredFor("app-beta")))
			commitApp(t, b, desiredFor("app-gamma"))
			return refFor(alpha())
		}, contract.Present, "", nil, nil},
		{"a notification naming a revision older than the change is unknown, saying Argo CD has not reported it", false, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			succeededAt(b, commitApp(t, b, alpha()))
			changed := onCluster(alpha(), "k3s-nbg1")
			commitApp(t, b, changed)
			return refFor(changed)
		}, contract.Unknown, "Argo CD", nil, nil},
		{"after removal, a notification naming a later revision without the files is absent", false, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			commitApp(t, b, alpha())
			removeApp(t, b, alpha())
			succeededAt(b, commitApp(t, b, desiredFor("app-beta")))
			commitApp(t, b, desiredFor("app-gamma"))
			return refFor(alpha())
		}, contract.Absent, "", nil, nil},
		{"after removal, a notification naming a revision still carrying the files is unknown", false, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			succeededAt(b, commitApp(t, b, alpha()))
			removeApp(t, b, alpha())
			return refFor(alpha())
		}, contract.Unknown, "Argo CD", nil, nil},
		{"after removal, head's succeeded deploy is absent", true, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			commitApp(t, b, alpha())
			removeApp(t, b, alpha())
			return refFor(alpha())
		}, contract.Absent, "", nil, nil},
		{"after removal with no notification it is unknown", false, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			commitApp(t, b, alpha())
			removeApp(t, b, alpha())
			return refFor(alpha())
		}, contract.Unknown, "", nil, nil},
		{"after removal with a pending deploy it is unknown", false, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			commitApp(t, b, alpha())
			b.notes.Set(gitops.Notification{App: "app-alpha", Revision: removeApp(t, b, alpha()), Phase: "Running", Health: "Progressing", Message: "prune failed"})
			return refFor(alpha())
		}, contract.Unknown, "", nil, nil},
		{"after removal a failed deploy fails carrying the message", false, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			commitApp(t, b, alpha())
			b.notes.Set(gitops.Notification{App: "app-alpha", Revision: removeApp(t, b, alpha()), Phase: "Error", Health: "Degraded", Message: "prune failed"})
			return refFor(alpha())
		}, "", "", []string{"prune failed"}, nil},
		{"only some files standing is unknown naming the missing path", true, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			if _, err := b.repo.Commit(context.Background(), commitApp(t, b, alpha()), map[string][]byte{alphaServiceAccountPath: nil}, "hand edit"); err != nil {
				t.Fatalf("hand edit: %v", err)
			}
			return refFor(alpha())
		}, contract.Unknown, alphaServiceAccountPath, nil, nil},
		{"a file standing with other content is unknown naming its path", true, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			if _, err := b.repo.Commit(context.Background(), commitApp(t, b, alpha()), map[string][]byte{alphaNamespacePath: []byte("kind: Namespace\n# drifted\n")}, "hand edit"); err != nil {
				t.Fatalf("hand edit: %v", err)
			}
			return refFor(alpha())
		}, contract.Unknown, alphaNamespacePath, nil, nil},
		{"a request that cannot be rendered fails saying why, and writes nothing", true, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			d := alpha()
			d.Params = map[string]string{"appName": "app-alpha"}
			return refFor(d)
		}, "", "", []string{"rendering", "no cluster param"}, func(t *testing.T, b *gitopsBlock) {
			if got := len(b.repo.Commits()); got != 0 {
				t.Errorf("Commits = %d, want 0: an observe never writes", got)
			}
		}},
		{"another app's commits leave the observed app present", true, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			commitApp(t, b, alpha())
			commitApp(t, b, desiredFor("app-beta"))
			removeApp(t, b, desiredFor("app-beta"))
			return refFor(alpha())
		}, contract.Present, "", nil, func(t *testing.T, b *gitopsBlock) {
			wantFilesAt(t, b.repo, b.head(t), renderApp("app-alpha", clusterParam).Files)
		}},
		{"a repository that cannot be reached fails naming it", true, func(t *testing.T, b *gitopsBlock) contract.ResourceRef {
			b.resource.Repository = func(context.Context, contract.Desired) (gitops.Repository, error) {
				return nil, errors.New("the GitOps repository is unreachable")
			}
			return refFor(alpha())
		}, "", "", []string{"unreachable"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newBlock(renderNamespace)
			if tc.auto {
				b = newNamespaceBlock()
			}
			ref := tc.arrange(t, b)

			obs, err := b.resource.ObserveResource(context.Background(), ref)

			if tc.wantErr != nil {
				for _, want := range tc.wantErr {
					if err == nil || !strings.Contains(err.Error(), want) {
						t.Errorf("ObserveResource error = %v, want it to carry %q", err, want)
					}
				}
			} else if err != nil || obs.State != tc.wantState {
				t.Fatalf("ObserveResource = %+v, %v; want %s", obs, err, tc.wantState)
			}
			if tc.wantState == contract.Present && !reflect.DeepEqual(obs.Outputs, alphaOutputs()) {
				t.Errorf("outputs = %v, want %v", obs.Outputs, alphaOutputs())
			}
			if tc.wantState == contract.Unknown && (obs.StatusDetail == "" || !strings.Contains(obs.StatusDetail, tc.wantDetail)) {
				t.Errorf("statusDetail = %q, want a detail carrying %q", obs.StatusDetail, tc.wantDetail)
			}
			if tc.check != nil {
				tc.check(t, b)
			}
		})
	}
}

func TestResource_ObserveResourceRendersFromTheRequestTheRefCarries(t *testing.T) {
	var seen []contract.Desired
	b := newBlock(func(d contract.Desired) (gitops.Rendered, error) {
		seen = append(seen, d)
		return renderNamespace(d)
	}, "app-alpha")
	commitApp(t, b, alpha())
	seen = nil
	ref := refFor(alpha())

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
