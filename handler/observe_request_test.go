package handler_test

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// requestObserverFake mirrors kittest.FakeResource but keeps the request each Observe is handed, which the shared fake is not edited to do; with PresentAfter or AbsentAfter set to N, Observe reports the target state only on the Nth call.
type requestObserverFake struct {
	mu sync.Mutex

	PresentAfter int // 0 means every call reports Present.
	AbsentAfter  int // 0 means every call reports Absent, unless PresentAfter is set.

	Outputs       map[string]string
	SecretOutputs map[string]string
	StatusDetail  string

	LastDesired  contract.Desired
	SecretsSeen  []map[string]map[string]string
	ObserveCalls int
	EnsureCalls  int
	DeleteCalls  int
}

func (f *requestObserverFake) Ensure(ctx context.Context, d contract.Desired) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.EnsureCalls++
	return nil
}

func (f *requestObserverFake) Delete(ctx context.Context, d contract.Desired) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DeleteCalls++
	return nil, nil
}

func (f *requestObserverFake) Observe(ctx context.Context, d contract.Desired) (contract.Observation, error) {
	f.mu.Lock()
	f.ObserveCalls++
	f.LastDesired = d
	f.SecretsSeen = append(f.SecretsSeen, d.Secrets)
	n := f.ObserveCalls
	f.mu.Unlock()

	state := contract.Present
	switch {
	case f.PresentAfter > 0:
		if n < f.PresentAfter {
			state = contract.Unknown
		} else {
			state = contract.Present
		}
	case f.AbsentAfter > 0:
		if n < f.AbsentAfter {
			state = contract.Unknown
		} else {
			state = contract.Absent
		}
	}

	return contract.Observation{State: state, Outputs: f.Outputs, SecretOutputs: f.SecretOutputs, StatusDetail: f.StatusDetail}, nil
}

var _ handler.Resource = (*requestObserverFake)(nil)

func TestServe_ValidateObserveReceivesTheValidateRequestSecrets(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	const tokenValue = "tok-validate-observes-9f3a"
	f := &requestObserverFake{}
	h := newHandler(t, f, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-observe-validate-secrets", map[string]any{
		"operation": "validate",
		"secrets":   map[string]any{"github": map[string]any{"token": tokenValue}},
	}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	if f.ObserveCalls != 1 {
		t.Fatalf("ObserveCalls = %d, want exactly 1", f.ObserveCalls)
	}
	if got := f.LastDesired.Secrets["github"]["token"]; got != tokenValue {
		t.Errorf(`Observe saw Secrets["github"]["token"] = %q, want %q`, got, tokenValue)
	}
}

func TestServe_ValidateObserveReceivesTheRequestsParams(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &requestObserverFake{}
	h := newHandler(t, f, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-observe-validate-params", map[string]any{
		"operation": "validate",
		"params":    map[string]string{"region": "eu-central-1"},
	}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	if f.ObserveCalls != 1 {
		t.Fatalf("ObserveCalls = %d, want exactly 1", f.ObserveCalls)
	}
	if got := f.LastDesired.Params["region"]; got != "eu-central-1" {
		t.Errorf(`Observe saw Params["region"] = %q, want %q`, got, "eu-central-1")
	}
}

func TestServe_SubscribeSettlePollsEachReceiveTheSubscribeSecrets(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	const tokenValue = "tok-subscribe-poll-7c21"
	const budget = 3
	f := &requestObserverFake{PresentAfter: budget} // Present only on the final (3rd) poll
	h := newHandler(t, f, s, func(c *handler.Config) { c.VerbBudget = budget })

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-observe-subscribe-polls", map[string]any{
		"operation": "subscribe",
		"secrets":   map[string]any{"github": map[string]any{"token": tokenValue}},
	}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	if f.ObserveCalls < 2 {
		t.Fatalf("ObserveCalls = %d, want at least 2 settle polls", f.ObserveCalls)
	}
	for i, secrets := range f.SecretsSeen {
		if got := secrets["github"]["token"]; got != tokenValue {
			t.Errorf("poll #%d saw Secrets[github][token] = %q, want %q", i+1, got, tokenValue)
		}
	}
}

func TestServe_UnsubscribeSettlePollsEachReceiveTheUnsubscribeSecrets(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	const tokenValue = "tok-unsubscribe-poll-4e88"
	const budget = 3
	f := &requestObserverFake{AbsentAfter: budget} // Absent only on the final (3rd) poll
	h := newHandler(t, f, s, func(c *handler.Config) { c.VerbBudget = budget })

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-observe-unsubscribe-polls", map[string]any{
		"operation": "unsubscribe",
		"secrets":   map[string]any{"github": map[string]any{"token": tokenValue}},
	}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	if f.ObserveCalls < 2 {
		t.Fatalf("ObserveCalls = %d, want at least 2 settle polls", f.ObserveCalls)
	}
	for i, secrets := range f.SecretsSeen {
		if got := secrets["github"]["token"]; got != tokenValue {
			t.Errorf("poll #%d saw Secrets[github][token] = %q, want %q", i+1, got, tokenValue)
		}
	}
}

func TestServe_ASubscribeCarryingSecretsObservesOnceAndAnswersTheBlocksOutputs(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{Outputs: map[string]string{"url": "https://example.test"}}
	h := newHandler(t, f, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-plain-resource-untouched", map[string]any{
		"secrets": map[string]any{"github": map[string]any{"token": "tok-plain-untouched-11aa"}},
	}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	if f.ObserveCalls != 1 {
		t.Errorf("ObserveCalls = %d, want exactly 1", f.ObserveCalls)
	}
	if f.EnsureCalls != 1 {
		t.Errorf("EnsureCalls = %d, want exactly 1", f.EnsureCalls)
	}
	resp := decodeResponse(t, raw)
	if resp.Outputs["url"] != "https://example.test" {
		t.Errorf("Outputs[url] = %q, want the block's own output", resp.Outputs["url"])
	}
}

// A Handler that stashed the first verb's secrets in its own state would leak them into a later unrelated request here.
func TestServe_PollSecretsFromOneVerbNeverReachALaterUnrelatedRequest(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	const tokenValue = "tok-scope-should-not-leak-2b6f"
	f := &requestObserverFake{}
	h := newHandler(t, f, s)

	status1, raw1 := postHook(t, h, s, envelope(t, s, "nonce-scope-a", map[string]any{
		"operation":      "validate",
		"subscriptionId": "sub-scope-a",
		"secrets":        map[string]any{"github": map[string]any{"token": tokenValue}},
	}))
	if status1 != http.StatusOK {
		t.Fatalf("first request: status = %d, body: %s", status1, raw1)
	}
	if got := f.LastDesired.Secrets["github"]["token"]; got != tokenValue {
		t.Fatalf("first request did not reach Observe with its own secret: got %q", got)
	}

	status2, raw2 := postHook(t, h, s, envelope(t, s, "nonce-scope-b", map[string]any{
		"operation":      "validate",
		"subscriptionId": "sub-scope-b",
	}))
	if status2 != http.StatusOK {
		t.Fatalf("second request: status = %d, body: %s", status2, raw2)
	}
	if len(f.LastDesired.Secrets) != 0 {
		t.Errorf("second request's Desired carried Secrets = %v, want none — the first verb's secrets must not persist in Handler state", f.LastDesired.Secrets)
	}
}

// The cache stores only a status and response bytes, which must already be clean.
func TestServe_IdempotencyCacheNeverStoresOrLeaksVerbSecrets(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	const tokenValue = "tok-idempotency-cache-should-not-leak-5d9c"
	f := &requestObserverFake{Outputs: map[string]string{"status": "ok"}}
	h := newHandler(t, f, s)

	var logBuf bytes.Buffer
	prevOut := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prevOut) })

	extra := map[string]any{
		"idempotencyKey": "idem-secret-cache-1",
		"subscriptionId": "sub-idem-secret-1",
		"secrets":        map[string]any{"github": map[string]any{"token": tokenValue}},
	}

	status1, raw1 := postHook(t, h, s, envelope(t, s, "nonce-idem-secret-a", extra))
	if status1 != http.StatusOK {
		t.Fatalf("first delivery: status = %d, body: %s", status1, raw1)
	}

	status2, raw2 := postHook(t, h, s, envelope(t, s, "nonce-idem-secret-b", extra))
	if status2 != status1 {
		t.Errorf("replay status = %d, want the first delivery's status %d", status2, status1)
	}
	if !bytes.Equal(raw1, raw2) {
		t.Errorf("replay body = %s, want the exact first-delivery body %s", raw2, raw1)
	}
	if f.ObserveCalls != 1 {
		t.Errorf("ObserveCalls = %d, want exactly 1 — the replay must never call the Resource again", f.ObserveCalls)
	}

	kittest.AssertNoSecret(t, "first delivery response body", string(raw1), tokenValue)
	kittest.AssertNoSecret(t, "replayed response body", string(raw2), tokenValue)
	kittest.AssertNoSecret(t, "log output", logBuf.String(), tokenValue)
}
