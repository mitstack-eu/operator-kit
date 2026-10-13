package handler_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// The two deliveries use different nonces, as a genuine retry would, since a repeated nonce is rejected by the replay guard regardless of idempotencyKey.
func TestServe_IdempotencyReplaysTheFirstResponseForTheSameKey(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	extra := map[string]any{"idempotencyKey": "idem-1", "subscriptionId": "sub-1"}
	status1, raw1 := postHook(t, h, s, envelope(t, s, "nonce-idem-1a", extra))
	if status1 != http.StatusOK {
		t.Fatalf("first delivery: status = %d, body: %s", status1, raw1)
	}
	if f.EnsureCalls != 1 {
		t.Fatalf("EnsureCalls after first delivery = %d, want 1", f.EnsureCalls)
	}

	status2, raw2 := postHook(t, h, s, envelope(t, s, "nonce-idem-1b", extra))
	if status2 != status1 {
		t.Errorf("replay status = %d, want the first delivery's status %d", status2, status1)
	}
	if !bytes.Equal(raw1, raw2) {
		t.Errorf("replay body = %s, want the exact first-delivery body %s", raw2, raw1)
	}
	if f.EnsureCalls != 1 {
		t.Errorf("EnsureCalls after the replay = %d, want still 1 — the Resource must not be called again", f.EnsureCalls)
	}
}

func TestServe_IdempotencyDifferentKeyCallsTheResourceAgain(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	postHook(t, h, s, envelope(t, s, "nonce-idem-2a", map[string]any{"idempotencyKey": "idem-A", "subscriptionId": "sub-1"}))
	if f.EnsureCalls != 1 {
		t.Fatalf("EnsureCalls after first delivery = %d, want 1", f.EnsureCalls)
	}

	postHook(t, h, s, envelope(t, s, "nonce-idem-2b", map[string]any{"idempotencyKey": "idem-B", "subscriptionId": "sub-1"}))
	if f.EnsureCalls != 2 {
		t.Errorf("EnsureCalls after a different idempotencyKey = %d, want 2 — a distinct key is a distinct request", f.EnsureCalls)
	}
}

func TestServe_IdempotencyEmptyKeyIsNeverCached(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	postHook(t, h, s, envelope(t, s, "nonce-idem-3a", nil))
	postHook(t, h, s, envelope(t, s, "nonce-idem-3b", nil))

	if f.EnsureCalls != 2 {
		t.Errorf("EnsureCalls = %d, want 2 — an empty idempotencyKey must never be cached/replayed", f.EnsureCalls)
	}
}

func TestServe_IdempotencyKeyedPerSubscription_NoCrossSubscriptionCollision(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	sharedKey := "idem-shared"

	statusA, rawA := postHook(t, h, s, envelope(t, s, "nonce-tenant-A", map[string]any{
		"idempotencyKey": sharedKey, "subscriptionId": "sub-A", "orgId": "org-A",
	}))
	if statusA != http.StatusOK {
		t.Fatalf("subscription A: status = %d, body: %s", statusA, rawA)
	}
	if f.EnsureCalls != 1 {
		t.Fatalf("EnsureCalls after subscription A = %d, want 1", f.EnsureCalls)
	}

	statusB, rawB := postHook(t, h, s, envelope(t, s, "nonce-tenant-B", map[string]any{
		"idempotencyKey": sharedKey, "subscriptionId": "sub-B", "orgId": "org-B",
	}))
	if statusB != http.StatusOK {
		t.Fatalf("subscription B: status = %d, body: %s", statusB, rawB)
	}
	if f.EnsureCalls != 2 {
		t.Errorf("EnsureCalls after subscription B = %d, want 2 — a different subscription sharing the same "+
			"idempotencyKey must still call the Resource; the cache must be keyed on (idempotencyKey, subscriptionId)", f.EnsureCalls)
	}

	// Replaying subscription A's own key must still replay, so the cache key was not widened into never matching.
	statusA2, rawA2 := postHook(t, h, s, envelope(t, s, "nonce-tenant-A2", map[string]any{
		"idempotencyKey": sharedKey, "subscriptionId": "sub-A", "orgId": "org-A",
	}))
	if statusA2 != statusA || !bytes.Equal(rawA2, rawA) {
		t.Errorf("re-delivery for subscription A did not replay: status=%d body=%s, want status=%d body=%s",
			statusA2, rawA2, statusA, rawA)
	}
	if f.EnsureCalls != 2 {
		t.Errorf("EnsureCalls after replaying subscription A = %d, want still 2", f.EnsureCalls)
	}
}

// flakyResource fails its first Ensure call and succeeds after, since a cached 500 would turn one transient error into a permanent one.
type flakyResource struct {
	mu          sync.Mutex
	ensureCalls int
}

func (f *flakyResource) Ensure(context.Context, contract.Desired) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureCalls++
	if f.ensureCalls == 1 {
		return errors.New("transient backend error")
	}
	return nil
}

func (f *flakyResource) Observe(context.Context, contract.Desired) (contract.Observation, error) {
	return contract.Observation{State: contract.Present}, nil
}

func (f *flakyResource) Delete(context.Context, contract.Desired) (map[string]string, error) {
	return nil, nil
}

func (f *flakyResource) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ensureCalls
}

func TestServe_Idempotency500IsNeverCachedRetryRunsTheResourceAgain(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	r := &flakyResource{}
	h := newHandler(t, r, s)

	extra := map[string]any{"idempotencyKey": "idem-500", "subscriptionId": "sub-1"}

	status1, raw1 := postHook(t, h, s, envelope(t, s, "nonce-500-a", extra))
	if status1 != http.StatusInternalServerError {
		t.Fatalf("first delivery: status = %d, want 500, body: %s", status1, raw1)
	}
	if r.calls() != 1 {
		t.Fatalf("EnsureCalls after the first (failing) delivery = %d, want 1", r.calls())
	}

	status2, raw2 := postHook(t, h, s, envelope(t, s, "nonce-500-b", extra))
	if r.calls() != 2 {
		t.Errorf("EnsureCalls after the retry = %d, want 2 — a 500 must never be cached, so the retry must reach the Resource again", r.calls())
	}
	if status2 != http.StatusOK {
		t.Errorf("retry status = %d, want 200 — the retry's own (successful) answer, not the cached 500, body: %s", status2, raw2)
	}
	resp := decodeResponse(t, raw2)
	if resp.Error != "" {
		t.Errorf("retry Error = %q, want empty — the second attempt succeeded and must not carry the first attempt's failure", resp.Error)
	}
}

// blockingResource holds Ensure open until released; entered is buffered so a wrongly duplicated call never blocks the Resource itself.
type blockingResource struct {
	mu          sync.Mutex
	ensureCalls int
	entered     chan struct{}
	release     chan struct{}
}

func newBlockingResource() *blockingResource {
	return &blockingResource{entered: make(chan struct{}, 8), release: make(chan struct{})}
}

func (r *blockingResource) Ensure(ctx context.Context, d contract.Desired) error {
	r.mu.Lock()
	r.ensureCalls++
	r.mu.Unlock()
	r.entered <- struct{}{}
	<-r.release
	return nil
}

func (r *blockingResource) Observe(ctx context.Context, d contract.Desired) (contract.Observation, error) {
	return contract.Observation{State: contract.Present}, nil
}

func (r *blockingResource) Delete(ctx context.Context, d contract.Desired) (map[string]string, error) {
	return nil, nil
}

func (r *blockingResource) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ensureCalls
}

// Meaningful under -race: every field blockingResource shares across goroutines is a channel or mutex-guarded, so a race can only come from the cache.
func TestServe_IdempotencySingleFlightSameKeyCallsEnsureOnce(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	r := newBlockingResource()
	h := newHandler(t, r, s)

	extra := map[string]any{"idempotencyKey": "idem-sf", "subscriptionId": "sub-1"}

	type result struct {
		status int
		body   []byte
	}
	results := make(chan result, 2)

	go func() {
		status, raw := postHook(t, h, s, envelope(t, s, "nonce-sf-a", extra))
		results <- result{status, raw}
	}()

	select {
	case <-r.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first delivery never reached Ensure")
	}

	go func() {
		status, raw := postHook(t, h, s, envelope(t, s, "nonce-sf-b", extra))
		results <- result{status, raw}
	}()

	// Give the second delivery a window to reach Ensure a second time while the first is still blocked.
	select {
	case <-r.entered:
		t.Error("a second concurrent delivery of the same key reached Ensure before the first one returned — the cache must be single-flight")
	case <-time.After(150 * time.Millisecond):
	}

	close(r.release)

	r1 := <-results
	r2 := <-results

	if calls := r.calls(); calls != 1 {
		t.Errorf("Ensure called %d times, want exactly 1 for two concurrent deliveries of the same key", calls)
	}
	if r1.status != r2.status {
		t.Errorf("concurrent deliveries got different statuses: %d vs %d", r1.status, r2.status)
	}
	if !bytes.Equal(r1.body, r2.body) {
		t.Errorf("concurrent deliveries got different bodies: %s vs %s", r1.body, r2.body)
	}
}
