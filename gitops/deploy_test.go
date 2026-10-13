package gitops_test

// deploy_test.go pins gitops.Deploy, which a block-level handler calls to
// ship its whole desired state: one commit of every file through
// CommitManifests, then a wait for Argo CD's verdict on that revision, or on
// head when nothing changed. A block whose Ensure calls Deploy, served
// through handler.New, answers the succeeded verdict as present and a failed
// one as a refusal.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/gitops"
	"github.com/mitstack-eu/operator-kit/v2/gitops/gitopstest"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/webhook"
)

// failingNotes answers a failed sync for any revision. gitopstest seeds a
// verdict by revision, which a test cannot know before Deploy commits.
type failingNotes struct{}

func (failingNotes) Latest(_ context.Context, app, revision string) (gitops.Notification, bool, error) {
	return gitops.Notification{App: app, Revision: revision, Phase: "Failed", Health: "Degraded", Message: "one or more objects failed to apply"}, true, nil
}

func alphaDeploy() gitops.BlockDeploy {
	return gitops.BlockDeploy{
		App:     "app-alpha",
		Files:   renderApp("app-alpha", "k3s-fsn1").Files,
		Message: "deploy app-alpha",
		Secrets: seededSecrets,
		Poll:    time.Millisecond,
	}
}

func TestDeploy_CommitsTheBlocksDesiredStateOnceAndWaitsForArgoCDsVerdict(t *testing.T) {
	repo, notes := gitopstest.NewRepository(), gitopstest.NewNotifications()
	notes.SucceedCommits("app-alpha", repo)

	got, err := gitops.Deploy(context.Background(), repo, notes, alphaDeploy())
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	commits := repo.Commits()
	if len(commits) != 1 {
		t.Fatalf("commits = %d, want one commit of the whole desired state", len(commits))
	}
	if !got.Changed || got.Revision != commits[0].Revision {
		t.Errorf("Deployed changed, revision = %v, %q; want true, %q", got.Changed, got.Revision, commits[0].Revision)
	}
	if !reflect.DeepEqual(commits[0].Changes, alphaDeploy().Files) || commits[0].Message != "deploy app-alpha" {
		t.Errorf("commit = %+v, want every file under the message %q", commits[0], "deploy app-alpha")
	}
	if got.Outcome.Verdict != gitops.VerdictSucceeded || got.Outcome.Notification.Revision != got.Revision {
		t.Errorf("outcome = %+v, want succeeded on revision %q", got.Outcome, got.Revision)
	}
}

func TestDeploy_WaitsOnHeadWhenNothingChanged(t *testing.T) {
	repo, notes := gitopstest.NewRepository(), gitopstest.NewNotifications()
	notes.SucceedCommits("app-alpha", repo)
	first, err := gitops.Deploy(context.Background(), repo, notes, alphaDeploy())
	if err != nil {
		t.Fatalf("first Deploy: %v", err)
	}

	again, err := gitops.Deploy(context.Background(), repo, notes, alphaDeploy())
	if err != nil {
		t.Fatalf("second Deploy: %v", err)
	}
	if again.Changed || again.Revision != first.Revision {
		t.Errorf("second Deployed changed, revision = %v, %q; want false and head %q", again.Changed, again.Revision, first.Revision)
	}
	if n := len(repo.Commits()); n != 1 {
		t.Errorf("commits = %d, want 1: an unchanged desired state lands nothing", n)
	}
	if again.Outcome.Verdict != gitops.VerdictSucceeded {
		t.Errorf("outcome = %+v, want the verdict on head", again.Outcome)
	}
}

func TestDeploy_SurfacesAFailedVerdict(t *testing.T) {
	repo := gitopstest.NewRepository()
	got, _ := gitops.Deploy(context.Background(), repo, failingNotes{}, alphaDeploy())
	if got.Outcome.Verdict != gitops.VerdictFailed {
		t.Errorf("outcome = %+v, want failed", got.Outcome)
	}
	if got.Outcome.Notification.Phase != "Failed" {
		t.Errorf("outcome notification = %+v, want the failed sync Argo CD reported", got.Outcome.Notification)
	}
}

func TestDeploy_RefusesAFileCarryingASecretAndCommitsNothing(t *testing.T) {
	repo, notes := gitopstest.NewRepository(), gitopstest.NewNotifications()
	in := alphaDeploy()
	in.Files["apps/app-alpha/leak.yaml"] = []byte("token: " + githubToken + "\n")
	_, err := gitops.Deploy(context.Background(), repo, notes, in)
	if !errors.Is(err, gitops.ErrSecretInManifest) {
		t.Errorf("error = %v, want ErrSecretInManifest", err)
	}
	if n := len(repo.Commits()); n != 0 {
		t.Errorf("commits = %d, want none", n)
	}
}

// --- driven from a block-level handler ---------------------------------------------

const deployBlockManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: gitops-deploy-block
  name: GitOps Deploy Block
spec:
  version: 1.0.0
  requires: [hetzner.k3s-cluster]
  resources: [kubernetes.namespace]
  provides: [kubernetes.namespace]
  lifecycle:
    endpoint: https://gitops-deploy-block.example/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`

// deployBlock ships the app's namespace through Deploy from its one
// block-level Ensure, and refuses a failed verdict.
type deployBlock struct {
	repo  *gitopstest.Repository
	notes gitops.Notifications
}

func (b *deployBlock) Ensure(ctx context.Context, d contract.Desired) error {
	out, err := gitops.Deploy(ctx, b.repo, b.notes, gitops.BlockDeploy{
		App:     d.AppID,
		Files:   renderApp(d.AppID, "k3s-fsn1").Files,
		Message: "deploy " + d.AppID,
		Secrets: gitops.SecretValues(d),
		Poll:    time.Millisecond,
	})
	if err != nil {
		return err
	}
	if out.Outcome.Verdict == gitops.VerdictFailed {
		return handler.Refuse("deploy-failed", "Argo CD's sync of %s failed: %s", out.Revision, out.Outcome.Notification.Message)
	}
	return nil
}

func (b *deployBlock) Observe(context.Context, contract.Desired) (contract.Observation, error) {
	return contract.Observation{State: contract.Unknown}, nil
}

func (b *deployBlock) Delete(context.Context, contract.Desired) (map[string]string, error) {
	return nil, nil
}

// namespaceInRepo observes the namespace as present once its file stands at
// the repository's head.
type namespaceInRepo struct{ repo *gitopstest.Repository }

func (o namespaceInRepo) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	head, err := o.repo.Head(ctx)
	if err != nil || head == "" {
		return contract.Observation{State: contract.Absent}, err
	}
	_, ok, err := o.repo.ReadFile(ctx, head, "apps/"+r.Desired.AppID+"/namespace.yaml")
	if err != nil || !ok {
		return contract.Observation{State: contract.Absent}, err
	}
	return contract.Observation{State: contract.Present, Outputs: alphaOutputs()}, nil
}

func serveDeployBlock(t *testing.T, b *deployBlock) (int, contract.Response) {
	t.Helper()
	s := kittest.NewSigner(t, "gitops-deploy-key", 9)
	h, err := handler.New(handler.Config{
		Resource:     b,
		Manifest:     []byte(deployBlockManifest),
		Observers:    map[string]handler.Observer{"kubernetes.namespace": namespaceInRepo{repo: b.repo}},
		JWKSURL:      s.JWKSServer(t),
		VerbBudget:   3,
		PollInterval: time.Millisecond,
		Sleep:        func(context.Context, time.Duration) {},
	})
	if err != nil {
		t.Fatalf("handler.New: %v", err)
	}
	obo, err := webhook.MintOBO(s.Private, s.Kid, webhook.OBOClaims{OrgID: "org-1", AppID: "app-alpha", SubscriptionID: "sub-app-alpha"}, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body := kittest.Envelope("nonce-gitops-deploy", time.Now(), map[string]any{
		"operation": "subscribe", "subscriptionId": "sub-app-alpha", "blockId": "gitops-deploy-block",
		"orgId": "org-1", "appId": "app-alpha", "obo": obo,
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, s.HookRequest(t, "/hook", body))
	resp, err := contract.DecodeResponse(w.Body.Bytes())
	if err != nil {
		t.Fatalf("decoding the answer: %v; body %s", err, w.Body.String())
	}
	return w.Code, resp
}

func TestDeploy_FromABlockLevelHandlerCommitsOnceAndAnswersTheSucceededVerdict(t *testing.T) {
	repo, notes := gitopstest.NewRepository(), gitopstest.NewNotifications()
	notes.SucceedCommits("app-alpha", repo)

	status, resp := serveDeployBlock(t, &deployBlock{repo: repo, notes: notes})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	if n := len(repo.Commits()); n != 1 {
		t.Errorf("commits = %d, want one", n)
	}
	if o := resp.Resources["kubernetes.namespace"].Observe; o == nil || o.State != contract.Present {
		t.Errorf("kubernetes.namespace observe = %+v, want present", o)
	}
	if resp.Outputs["namespace"] != "app-alpha" {
		t.Errorf("outputs = %v, want the provided namespace's", resp.Outputs)
	}
}

func TestDeploy_FromABlockLevelHandlerSurfacesAFailedVerdict(t *testing.T) {
	repo := gitopstest.NewRepository()
	status, resp := serveDeployBlock(t, &deployBlock{repo: repo, notes: failingNotes{}})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for the refused deploy; response %+v", status, resp)
	}
	if got := handler.RefusedClass(resp.StatusDetail); got != "deploy-failed" || !strings.Contains(resp.StatusDetail, "failed to apply") {
		t.Errorf("statusDetail = %q, want the deploy-failed refusal carrying Argo CD's message", resp.StatusDetail)
	}
}
