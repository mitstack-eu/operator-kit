package gitops_test

// The test block, served at block level, commits and removes through Deploy and serves a named validate through its gitops.Resource.

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

// namespaceBlock ships its manifests, or their removal, through Deploy, and its gitops.Resource observes the namespace.
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

func namedFields(operation, app string) map[string]any {
	f := requestFields(operation, app)
	f["resource"] = resourceName
	f["resourceType"] = resourceType
	f["resourceIdentity"] = map[string]any{"app": app}
	return f
}

// post signs fields as a fresh delivery with its own nonce and OBO token.
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

// expect fails unless a post answered want, and decodes the answer.
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

func TestNamespaceBlock(t *testing.T) {
	cases := []struct {
		name string
		// before is posted first and must answer 200.
		before      map[string]any
		fields      map[string]any
		failSync    bool
		wantStatus  int
		wantCommits int
		check       func(t *testing.T, b *gitopsBlock, resp contract.Response)
	}{
		{"a subscribe commits the app's manifests once through Deploy and answers the observed namespace", nil, requestFields("subscribe", "app-alpha"), false, 200, 1,
			func(t *testing.T, b *gitopsBlock, resp contract.Response) {
				if o := resp.Resources[resourceName].Observe; resp.Pending || !reflect.DeepEqual(resp.Outputs, alphaOutputs()) || o == nil || o.State != contract.Present {
					t.Errorf("subscribe answered pending %v outputs %v observe %+v, want settled present with %v", resp.Pending, resp.Outputs, o, alphaOutputs())
				}
				wantFilesAt(t, b.repo, b.repo.Commits()[0].Revision, renderApp("app-alpha", clusterParam).Files)
			}},
		{"an unsubscribe commits the removal and settles absent", requestFields("subscribe", "app-alpha"), requestFields("unsubscribe", "app-alpha"), false, 200, 2,
			func(t *testing.T, b *gitopsBlock, resp contract.Response) {
				if o := resp.Resources[resourceName].Observe; resp.Absent == nil || !*resp.Absent || o == nil || o.State != contract.Absent {
					t.Errorf("unsubscribe answered absent %v observe %+v detail %q, want absent", resp.Absent, o, resp.StatusDetail)
				}
				wantFilesAt(t, b.repo, b.head(t), map[string][]byte{alphaNamespacePath: nil, alphaServiceAccountPath: nil})
			}},
		{"a named validate is observed with the recorded identity and type and never commits", requestFields("subscribe", "app-alpha"), namedFields("validate", "app-alpha"), false, 200, 1,
			func(t *testing.T, b *gitopsBlock, resp contract.Response) {
				o := resp.Resources[resourceName].Observe
				if o == nil || o.State != contract.Present || o.Type != resourceType || !reflect.DeepEqual(o.Identity, map[string]string{"app": "app-alpha"}) {
					t.Errorf("%s observe = %+v, want present of type %q with the recorded {app: app-alpha}", resourceName, o, resourceType)
				}
			}},
		{"a named subscribe is refused naming the resource", nil, namedFields("subscribe", "app-alpha"), false, 400, 0, wantErrorNaming(resourceName)},
		{"a named unsubscribe is refused naming the resource", nil, namedFields("unsubscribe", "app-alpha"), false, 400, 0, wantErrorNaming(resourceName)},
		{"a failed Argo CD sync is refused as deploy-failed carrying its message", nil, requestFields("subscribe", "app-alpha"), true, 400, 1,
			func(t *testing.T, _ *gitopsBlock, resp contract.Response) {
				if got := handler.RefusedClass(resp.StatusDetail); got != "deploy-failed" || !strings.Contains(resp.StatusDetail, "failed to apply") {
					t.Errorf("statusDetail = %q, want the deploy-failed refusal carrying Argo CD's message", resp.StatusDetail)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newNamespaceBlock()
			if tc.failSync {
				b.resource.Notifications = func(context.Context, contract.Desired) (gitops.Notifications, error) { return failingNotes{}, nil }
			}
			h, s := serveNamespace(t, b, nil)
			if tc.before != nil {
				expect(t, http.StatusOK)(post(t, h, s, tc.before))
			}

			resp := expect(t, tc.wantStatus)(post(t, h, s, tc.fields))

			if got := len(b.repo.Commits()); got != tc.wantCommits {
				t.Errorf("Commits = %d, want %d", got, tc.wantCommits)
			}
			tc.check(t, b, resp)
		})
	}
}

func wantErrorNaming(name string) func(t *testing.T, _ *gitopsBlock, resp contract.Response) {
	return func(t *testing.T, _ *gitopsBlock, resp contract.Response) {
		t.Helper()
		if !strings.Contains(resp.Error, `"`+name+`"`) {
			t.Errorf("error = %q, want it to name %q", resp.Error, name)
		}
	}
}

func TestNamespaceBlock_ASubscribeSentTwiceUnderOneKeyCommitsOnceAndAnswersIdentically(t *testing.T) {
	b := newNamespaceBlock()
	h, s := serveNamespace(t, b, nil)
	fields := requestFields("subscribe", "app-alpha")
	fields["idempotencyKey"] = "key-1"

	firstStatus, first := post(t, h, s, fields)
	secondStatus, second := post(t, h, s, fields)

	expect(t, http.StatusOK)(firstStatus, first)
	if secondStatus != firstStatus || string(second) != string(first) || len(b.repo.Commits()) != 1 {
		t.Errorf("the second delivery answered %d %s after %d commits, want the first's %d %s and exactly 1 commit", secondStatus, second, len(b.repo.Commits()), firstStatus, first)
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
