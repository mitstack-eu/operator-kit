// Package handler_test exercises the lifecycle webhook Handler end to end with real signed requests.
package handler_test

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/webhook"
)

func noopSleep(context.Context, time.Duration) {}

func noopHook(context.Context, contract.Desired) error { return nil }

func baseConfig(t *testing.T, r handler.Resource, s *kittest.Signer) handler.Config {
	t.Helper()
	return handler.Config{Resource: r, JWKSURL: s.JWKSServer(t), VerbBudget: 3, PollInterval: time.Millisecond, Sleep: noopSleep}
}

// harness serves signed requests to one Handler, each with a fresh nonce, as a genuine retry carries.
type harness struct {
	t *testing.T
	s *kittest.Signer
	h *handler.Handler
}

func newHarness(t *testing.T, r handler.Resource, mutate ...func(*handler.Config)) *harness {
	t.Helper()
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, r, s)
	for _, m := range mutate {
		m(&cfg)
	}
	h, err := handler.New(cfg)
	if err != nil {
		t.Fatalf("handler.New: %v", err)
	}
	return &harness{t: t, s: s, h: h}
}

var nonces atomic.Int64

// envelope attaches a valid OBO token unless fields sets one, so a test that wants no obo field builds its body with kittest.Envelope directly.
func envelope(t *testing.T, s *kittest.Signer, fields map[string]any) []byte {
	t.Helper()
	all := map[string]any{"operation": "subscribe", "subscriptionId": "sub-1", "blockId": "block-1", "orgId": "org-1"}
	for k, v := range fields {
		all[k] = v
	}
	if _, ok := all["obo"]; !ok {
		subID, _ := all["subscriptionId"].(string)
		orgID, _ := all["orgId"].(string)
		appID, _ := all["appId"].(string)
		tok, err := webhook.MintOBO(s.Private, s.Kid, webhook.OBOClaims{OrgID: orgID, AppID: appID, SubscriptionID: subID}, time.Hour, time.Now())
		if err != nil {
			t.Fatalf("minting a default OBO token: %v", err)
		}
		all["obo"] = tok
	}
	return kittest.Envelope(fmt.Sprintf("nonce-%d", nonces.Add(1)), time.Now(), all)
}

func (x *harness) request(fields map[string]any) *http.Request {
	x.t.Helper()
	return x.s.HookRequest(x.t, "/hook", envelope(x.t, x.s, fields))
}

func (x *harness) serve(r *http.Request) (int, []byte) {
	w := httptest.NewRecorder()
	x.h.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

func (x *harness) post(fields map[string]any) (int, []byte) {
	x.t.Helper()
	return x.serve(x.request(fields))
}

func (x *harness) postContext(ctx context.Context, fields map[string]any) (int, []byte) {
	x.t.Helper()
	return x.serve(x.request(fields).WithContext(ctx))
}

// answer posts fields and returns the response once it conforms to the lifecycle webhook response contract.
func (x *harness) answer(fields map[string]any) (int, contract.Response) {
	x.t.Helper()
	status, raw := x.post(fields)
	assertConforms(x.t, raw)
	return status, decodeResponse(x.t, raw)
}

func decodeResponse(t *testing.T, raw []byte) contract.Response {
	t.Helper()
	resp, err := contract.DecodeResponse(raw)
	if err != nil {
		t.Fatalf("response does not decode as a contract.Response: %v\nbody: %s", err, raw)
	}
	return resp
}

func assertConforms(t *testing.T, raw []byte) {
	t.Helper()
	if problems := kittest.CheckResponse(raw); len(problems) != 0 {
		t.Errorf("response does not conform to the lifecycle webhook response contract: %v\nbody: %s", problems, raw)
	}
}

func assertCalls(t *testing.T, f *kittest.FakeResource, want [3]int) {
	t.Helper()
	if got := [3]int{f.EnsureCalls, f.ObserveCalls, f.DeleteCalls}; got != want {
		t.Errorf("Ensure, Observe, Delete calls = %v, want %v", got, want)
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

func withManifest[M string | []byte](manifest M) func(*handler.Config) {
	return func(c *handler.Config) { c.Manifest = []byte(manifest) }
}

// declaring sets the manifest and registers noopHook under each named hook point.
func declaring[M string | []byte](manifest M, hooks ...string) func(*handler.Config) {
	return func(c *handler.Config) {
		c.Manifest = []byte(manifest)
		c.Hooks = map[string]handler.Hook{}
		for _, name := range hooks {
			c.Hooks[name] = noopHook
		}
	}
}

func withBudget(n int) func(*handler.Config) {
	return func(c *handler.Config) { c.VerbBudget = n }
}

const v1alphaHead = "apiVersion: mitstack/v1alpha\nkind: Block\nmetadata:\n  id: test-block\n  name: Test Block\nspec:\n  layer: infra\n"

// manifestWithLifecycle writes lines verbatim under spec.lifecycle.
func manifestWithLifecycle(lines string) string {
	return v1alphaHead + "  lifecycle:\n" + lines
}

func manifestDeclaring(hooks ...string) string {
	if len(hooks) == 0 {
		return manifestWithLifecycle("    hooks: []\n")
	}
	return manifestWithLifecycle("    hooks:\n      - " + strings.Join(hooks, "\n      - ") + "\n")
}

func manifestWithRetains(value string, hooks ...string) string {
	lines := "    retainsOnUnsubscribe: " + value + "\n"
	if len(hooks) > 0 {
		lines += "    hooks:\n      - " + strings.Join(hooks, "\n      - ") + "\n"
	}
	return manifestWithLifecycle(lines)
}

func plainV1Beta(t *testing.T) []byte {
	t.Helper()
	raw, err := contract.ManifestGoldens.ReadFile(contract.ManifestGoldensDir + "/plain-v1beta.yaml")
	if err != nil {
		t.Fatalf("reading plain-v1beta.yaml from contract.ManifestGoldens: %v", err)
	}
	return raw
}

func withManifestEdit(t *testing.T, raw []byte, old, new string) []byte {
	t.Helper()
	edited := strings.Replace(string(raw), old, new, 1)
	if edited == string(raw) {
		t.Fatalf("fixture edit: %q is not in the manifest:\n%s", old, raw)
	}
	return []byte(edited)
}

// resource is kittest.FakeResource plus what the kit's fake leaves out: it keeps every request, answers SecretOutputs and DeleteOutputs, and reports Unknown until its settleAfter-th observe.
type resource struct {
	*kittest.FakeResource
	secretOutputs, deleteOutputs map[string]string
	settleAfter                  int

	mu                         sync.Mutex
	ensured, observed, deleted []contract.Desired
}

func fake(f *kittest.FakeResource) *resource { return &resource{FakeResource: f} }

func (r *resource) record(calls *[]contract.Desired, d contract.Desired) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	*calls = append(*calls, d)
	return len(*calls)
}

func (r *resource) Ensure(ctx context.Context, d contract.Desired) error {
	r.record(&r.ensured, d)
	return r.FakeResource.Ensure(ctx, d)
}

func (r *resource) Observe(ctx context.Context, d contract.Desired) (contract.Observation, error) {
	n := r.record(&r.observed, d)
	obs, err := r.FakeResource.Observe(ctx, d)
	if n < r.settleAfter {
		obs.State = contract.Unknown
	}
	obs.SecretOutputs = r.secretOutputs
	return obs, err
}

func (r *resource) Delete(ctx context.Context, d contract.Desired) (map[string]string, error) {
	r.record(&r.deleted, d)
	if _, err := r.FakeResource.Delete(ctx, d); err != nil {
		return nil, err
	}
	return r.deleteOutputs, nil
}

// blockingResource holds Ensure and Observe open until release closes; entered is buffered so a wrongly duplicated call never blocks on the signal.
type blockingResource struct {
	mu                sync.Mutex
	ensures, observes int
	entered, release  chan struct{}
}

func newBlockingResource() *blockingResource {
	return &blockingResource{entered: make(chan struct{}, 8), release: make(chan struct{})}
}

func (r *blockingResource) block(calls *int) {
	r.mu.Lock()
	*calls++
	r.mu.Unlock()
	r.entered <- struct{}{}
	<-r.release
}

func (r *blockingResource) Ensure(context.Context, contract.Desired) error {
	r.block(&r.ensures)
	return nil
}

func (r *blockingResource) Observe(context.Context, contract.Desired) (contract.Observation, error) {
	r.block(&r.observes)
	return contract.Observation{State: contract.Present}, nil
}

func (r *blockingResource) Delete(context.Context, contract.Desired) (map[string]string, error) {
	return nil, nil
}

func (r *blockingResource) counts() (ensures, observes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ensures, r.observes
}

func awsInstances(region string) map[string]any {
	return map[string]any{"aws": map[string]any{"name": "Consumer AWS", "endpoint": "https://aws.example.com", "config": map[string]string{"region": region}}}
}

func instanceRegion(instances map[string]contract.InstanceConfig) string {
	return instances["aws"].Config["region"]
}
