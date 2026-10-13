package gitops_test

// namespace_block_test.go serves the test block, a kubernetes.namespace
// declared in mitstack/v1beta, through handler.New at block level, the way
// the engine drives it:
//
//   - a subscribe commits the app's manifests once through Deploy and
//     answers the namespace the gitops.Resource observes, and the same
//     delivery under one idempotency key commits nothing more;
//   - an unsubscribe commits their removal through Deploy and settles absent;
//   - a validate naming the resource is served by its gitops.Resource with
//     the identity and type the engine recorded, while a subscribe or
//     unsubscribe naming it is refused with a 400 naming it;
//   - no seeded secret leaks through the handler's responses or logs.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/gitops"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/webhook"
)

// namespaceBlock serves the test block at block level: Ensure and Delete
// commit the app's manifests, or their removal, and wait for Argo CD
// through Deploy, and the block's gitops.Resource observes the namespace.
type namespaceBlock struct{ *gitopsBlock }

func (b namespaceBlock) Ensure(ctx context.Context, d contract.Desired) error {
	out, err := b.resource.Render(d)
	if err != nil {
		return err
	}
	return b.deploy(ctx, d, out.App, out.Files, out.App+": update manifests")
}

func (b namespaceBlock) Observe(context.Context, contract.Desired) (contract.Observation, error) {
	return contract.Observation{State: contract.Unknown}, nil
}

func (b namespaceBlock) Delete(ctx context.Context, d contract.Desired) (map[string]string, error) {
	out, err := b.resource.Render(d)
	if err != nil {
		return nil, err
	}
	removal := make(map[string][]byte, len(out.Files))
	for path := range out.Files {
		removal[path] = nil
	}
	return nil, b.deploy(ctx, d, out.App, removal, out.App+": remove manifests")
}

func (b namespaceBlock) deploy(ctx context.Context, d contract.Desired, app string, files map[string][]byte, message string) error {
	repo, err := b.resource.Repository(ctx, d)
	if err != nil {
		return err
	}
	notes, err := b.resource.Notifications(ctx, d)
	if err != nil {
		return err
	}
	out, err := gitops.Deploy(ctx, repo, notes, gitops.BlockDeploy{
		App: app, Files: files, Message: message, Secrets: gitops.SecretValues(d), Poll: time.Millisecond,
	})
	if err != nil {
		return err
	}
	if out.Outcome.Verdict == gitops.VerdictFailed {
		return handler.Refuse("deploy-failed", "Argo CD's sync of %s failed: %s", out.Revision, out.Outcome.Notification.Message)
	}
	return nil
}

// serveNamespace serves b at block level behind a fresh kittest signer,
// with logs as its /logs buffer when non-nil.
func serveNamespace(t *testing.T, b *gitopsBlock, logs *webhook.LogBuffer) (*handler.Handler, *kittest.Signer) {
	t.Helper()
	s := kittest.NewSigner(t, "gitops-test-key", 7)
	h, err := handler.New(handler.Config{
		Resource:     namespaceBlock{b},
		Manifest:     readNamespaceManifest(t),
		Observers:    map[string]handler.Observer{resourceName: b.resource},
		JWKSURL:      s.JWKSServer(t),
		Logs:         logs,
		VerbBudget:   3,
		PollInterval: time.Millisecond,
		Sleep:        func(context.Context, time.Duration) {},
	})
	if err != nil {
		t.Fatalf("handler.New: %v", err)
	}
	return h, s
}

// requestFields is the envelope of a request for the app, naming no
// resource and carrying the cluster param and every kind of secret.
func requestFields(operation, app string) map[string]any {
	return map[string]any{
		"operation":      operation,
		"subscriptionId": "sub-" + app,
		"blockId":        "gitops-app-namespace",
		"blockVersion":   "1.0.0",
		"orgId":          "org-1",
		"appId":          app,
		"params":         map[string]any{"appName": app, "cluster": clusterParam},
		"secrets": map[string]any{
			"github":  map[string]any{"token": githubToken},
			"hetzner": map[string]any{"apiToken": hetznerSecret},
		},
		"secretInputs": map[string]any{"dbPassword": secretInput},
		"appSecrets":   map[string]any{"STRIPE_KEY": appSecret},
	}
}

// namedFields is requestFields naming the app's namespace with the type
// and identity the engine recorded for it.
func namedFields(operation, app string) map[string]any {
	f := requestFields(operation, app)
	f["resource"] = resourceName
	f["resourceType"] = resourceType
	f["resourceIdentity"] = map[string]any{"app": app}
	return f
}

// post signs fields as a fresh webhook delivery, with its own nonce and
// OBO token, and returns the handler's status and exact response bytes.
func post(t *testing.T, h http.Handler, s *kittest.Signer, fields map[string]any) (int, []byte) {
	t.Helper()
	body := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		body[k] = v
	}
	subID, _ := fields["subscriptionId"].(string)
	orgID, _ := fields["orgId"].(string)
	appID, _ := fields["appId"].(string)
	obo, err := webhook.MintOBO(s.Private, s.Kid, webhook.OBOClaims{OrgID: orgID, AppID: appID, SubscriptionID: subID}, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("minting an OBO token: %v", err)
	}
	body["obo"] = obo
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, s.HookRequest(t, "/hook", kittest.Envelope(hex.EncodeToString(nonce), time.Now(), body)))
	return rec.Code, rec.Body.Bytes()
}

// expect returns a reader of a post's answer that fails unless its status is
// want, and decodes it.
func expect(t *testing.T, want int) func(int, []byte) contract.Response {
	t.Helper()
	return func(status int, raw []byte) contract.Response {
		t.Helper()
		if status != want {
			t.Fatalf("status = %d, want %d; body %s", status, want, raw)
		}
		resp, err := contract.DecodeResponse(raw)
		if err != nil {
			t.Fatalf("decoding the response: %v; body %s", err, raw)
		}
		return resp
	}
}

func TestNamespaceBlock_ASubscribeCommitsTheAppsManifestsOnceAndAnswersTheObservedNamespace(t *testing.T) {
	b := newNamespaceBlock()
	h, s := serveNamespace(t, b, nil)

	resp := expect(t, http.StatusOK)(post(t, h, s, requestFields("subscribe", "app-alpha")))

	if resp.Pending || !reflect.DeepEqual(resp.Outputs, alphaOutputs()) {
		t.Errorf("subscribe answered pending %v outputs %v, want settled with %v", resp.Pending, resp.Outputs, alphaOutputs())
	}
	if o := resp.Resources[resourceName].Observe; o == nil || o.State != contract.Present {
		t.Errorf("%s observe = %+v, want present", resourceName, o)
	}
	commits := b.repo.Commits()
	if len(commits) != 1 {
		t.Fatalf("Commits = %d, want 1", len(commits))
	}
	wantFilesAt(t, b.repo, commits[0].Revision, renderApp("app-alpha", clusterParam).Files)
}

func TestNamespaceBlock_ASubscribeSentTwiceUnderOneKeyCommitsOnceAndAnswersIdentically(t *testing.T) {
	b := newNamespaceBlock()
	h, s := serveNamespace(t, b, nil)
	fields := requestFields("subscribe", "app-alpha")
	fields["idempotencyKey"] = "key-1"

	firstStatus, first := post(t, h, s, fields)
	secondStatus, second := post(t, h, s, fields)

	expect(t, http.StatusOK)(firstStatus, first)
	if secondStatus != firstStatus || string(second) != string(first) {
		t.Errorf("the second delivery answered %d %s, want the first's %d %s", secondStatus, second, firstStatus, first)
	}
	if got := len(b.repo.Commits()); got != 1 {
		t.Errorf("Commits = %d, want exactly 1", got)
	}
}

func TestNamespaceBlock_AnUnsubscribeCommitsTheRemovalAndSettlesAbsent(t *testing.T) {
	b := newNamespaceBlock()
	h, s := serveNamespace(t, b, nil)
	expect(t, http.StatusOK)(post(t, h, s, requestFields("subscribe", "app-alpha")))

	resp := expect(t, http.StatusOK)(post(t, h, s, requestFields("unsubscribe", "app-alpha")))

	if resp.Absent == nil || !*resp.Absent {
		t.Fatalf("unsubscribe answered absent %v, want true; detail %q", resp.Absent, resp.StatusDetail)
	}
	if o := resp.Resources[resourceName].Observe; o == nil || o.State != contract.Absent {
		t.Errorf("%s observe = %+v, want absent", resourceName, o)
	}
	if got := len(b.repo.Commits()); got != 2 {
		t.Errorf("Commits = %d, want 2: the manifests and their removal", got)
	}
	wantFilesAt(t, b.repo, b.head(t), map[string][]byte{alphaNamespacePath: nil, alphaServiceAccountPath: nil})
}

func TestNamespaceBlock_ANamedValidateIsObservedWithTheRecordedIdentityAndType(t *testing.T) {
	b := newNamespaceBlock()
	h, s := serveNamespace(t, b, nil)
	expect(t, http.StatusOK)(post(t, h, s, requestFields("subscribe", "app-alpha")))

	resp := expect(t, http.StatusOK)(post(t, h, s, namedFields("validate", "app-alpha")))

	o := resp.Resources[resourceName].Observe
	if o == nil || o.State != contract.Present {
		t.Fatalf("%s observe = %+v, want present", resourceName, o)
	}
	if o.Type != resourceType || !reflect.DeepEqual(o.Identity, map[string]string{"app": "app-alpha"}) {
		t.Errorf("%s observe type, identity = %q, %v; want %q, the recorded {app: app-alpha}", resourceName, o.Type, o.Identity, resourceType)
	}
	if got := len(b.repo.Commits()); got != 1 {
		t.Errorf("Commits = %d, want 1: a validate never commits", got)
	}
}

func TestNamespaceBlock_ANamedSubscribeOrUnsubscribeIsRefusedNamingTheResourceAndCommitsNothing(t *testing.T) {
	for _, operation := range []string{"subscribe", "unsubscribe"} {
		t.Run(operation, func(t *testing.T) {
			b := newNamespaceBlock()
			h, s := serveNamespace(t, b, nil)

			resp := expect(t, http.StatusBadRequest)(post(t, h, s, namedFields(operation, "app-alpha")))

			if !strings.Contains(resp.Error, `"`+resourceName+`"`) {
				t.Errorf("error = %q, want it to name %q", resp.Error, resourceName)
			}
			if got := len(b.repo.Commits()); got != 0 {
				t.Errorf("Commits = %d, want 0", got)
			}
		})
	}
}

func TestNamespaceBlock_LeaksNoSecretThroughTheHandlersResponsesOrLogs(t *testing.T) {
	kittest.AssertNoSecretLeak(t, kittest.SecretLeak{
		Secrets: seededSecrets,
		Serve: func(logs *webhook.LogBuffer) (http.Handler, *kittest.Signer) {
			b := newNamespaceBlock()
			reach := b.resource.Repository
			b.resource.Repository = func(ctx context.Context, d contract.Desired) (gitops.Repository, error) {
				if d.AppID == "app-broken" {
					return nil, errors.New("the GitOps repository for app-broken is unreachable")
				}
				return reach(ctx, d)
			}
			return serveNamespace(t, b, logs)
		},
		Requests: []kittest.LeakRequest{
			{Name: "subscribe", Body: requestFields("subscribe", "app-alpha")},
			{Name: "validate", Body: namedFields("validate", "app-alpha")},
			{Name: "unsubscribe", Body: requestFields("unsubscribe", "app-alpha")},
			{Name: "subscribe to an unreachable repository", Body: requestFields("subscribe", "app-broken"), Fails: true},
		},
	})
}
