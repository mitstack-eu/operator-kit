package handler_test

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// The cache stores only a settled answer: a validate probes current state, and a pending subscribe is re-driven under the same key to learn when it converges.

func TestServe_IdempotencyValidateIsNeverCached(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{Count: 1}
	h := newHandler(t, f, s)

	extra := map[string]any{"operation": "validate", "idempotencyKey": "idem-validate", "subscriptionId": "sub-1"}

	status1, raw1 := postHook(t, h, s, envelope(t, s, "nonce-idem-validate-a", extra))
	if status1 != http.StatusOK {
		t.Fatalf("first validate: status = %d, body: %s", status1, raw1)
	}
	if f.ObserveCalls != 1 {
		t.Fatalf("ObserveCalls after the first validate = %d, want 1", f.ObserveCalls)
	}
	if resp := decodeResponse(t, raw1); resp.Absent == nil || *resp.Absent {
		t.Fatalf("first validate: want absent:false for a provisioned resource, body: %s", raw1)
	}

	f.Count = 0

	status2, raw2 := postHook(t, h, s, envelope(t, s, "nonce-idem-validate-b", extra))
	if status2 != http.StatusOK {
		t.Fatalf("second validate: status = %d, body: %s", status2, raw2)
	}
	if f.ObserveCalls != 2 {
		t.Errorf("ObserveCalls after the second validate = %d, want 2 — a validate must never be answered from the idempotency cache", f.ObserveCalls)
	}
	if resp := decodeResponse(t, raw2); resp.Absent == nil || !*resp.Absent {
		t.Errorf("second validate: want absent:true — the resource is gone, so the probe must report it, body: %s", raw2)
	}
}

func TestServe_IdempotencyPendingSubscribeIsNeverCached(t *testing.T) {
	for _, op := range []string{"subscribe", "upgrade"} {
		t.Run(op, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			absent := contract.Absent
			f := &kittest.FakeResource{ObserveState: &absent, Outputs: map[string]string{"endpoint": "https://ready"}}
			h := newHandler(t, f, s)

			extra := map[string]any{"operation": op, "idempotencyKey": "idem-pending-" + op, "subscriptionId": "sub-1"}

			status1, raw1 := postHook(t, h, s, envelope(t, s, "nonce-pending-"+op+"-a", extra))
			if status1 != http.StatusOK {
				t.Fatalf("first delivery: status = %d, body: %s", status1, raw1)
			}
			if resp := decodeResponse(t, raw1); !resp.Pending {
				t.Fatalf("first delivery Pending = false, want true — the fixture must settle pending, body: %s", raw1)
			}
			if f.EnsureCalls != 1 {
				t.Fatalf("EnsureCalls after the first delivery = %d, want 1", f.EnsureCalls)
			}

			// The resource converges between the two deliveries.
			f.ObserveState = nil

			status2, raw2 := postHook(t, h, s, envelope(t, s, "nonce-pending-"+op+"-b", extra))
			if f.EnsureCalls != 2 {
				t.Errorf("EnsureCalls after the re-delivery = %d, want 2 — a pending answer must never be cached", f.EnsureCalls)
			}
			if status2 != http.StatusOK {
				t.Fatalf("re-delivery: status = %d, body: %s", status2, raw2)
			}
			resp2 := decodeResponse(t, raw2)
			if resp2.Pending {
				t.Errorf("re-delivery Pending = true, want the fresh settled answer, body: %s", raw2)
			}
			if resp2.Outputs["endpoint"] != "https://ready" {
				t.Errorf("re-delivery Outputs = %v, want the settled observation's outputs, body: %s", resp2.Outputs, raw2)
			}
			if bytes.Equal(raw1, raw2) {
				t.Errorf("re-delivery body is byte-identical to the pending first answer %s — it was replayed from the cache", raw1)
			}
		})
	}
}

func TestServe_IdempotencySettledSubscribeIsStillDeduplicated(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{Outputs: map[string]string{"endpoint": "https://ready"}}
	h := newHandler(t, f, s)

	extra := map[string]any{"operation": "subscribe", "idempotencyKey": "idem-settled", "subscriptionId": "sub-1"}

	status1, raw1 := postHook(t, h, s, envelope(t, s, "nonce-settled-a", extra))
	if status1 != http.StatusOK {
		t.Fatalf("first delivery: status = %d, body: %s", status1, raw1)
	}
	if resp := decodeResponse(t, raw1); resp.Pending {
		t.Fatalf("first delivery Pending = true, want a settled answer, body: %s", raw1)
	}

	status2, raw2 := postHook(t, h, s, envelope(t, s, "nonce-settled-b", extra))
	if status2 != status1 {
		t.Errorf("replay status = %d, want %d", status2, status1)
	}
	if !bytes.Equal(raw1, raw2) {
		t.Errorf("replay body = %s, want the exact first body %s", raw2, raw1)
	}
	if f.EnsureCalls != 1 {
		t.Errorf("EnsureCalls = %d, want 1 — a settled subscribe must still be replayed from the cache", f.EnsureCalls)
	}
}

// A refusal is deterministic, so a re-delivery replays it.
func TestServe_IdempotencyRefusalIsStillDeduplicated(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{EnsureErr: handler.Refuse("quota-exceeded", "org quota exhausted")}
	h := newHandler(t, f, s)

	extra := map[string]any{"operation": "subscribe", "idempotencyKey": "idem-refused", "subscriptionId": "sub-1"}

	status1, raw1 := postHook(t, h, s, envelope(t, s, "nonce-refused-a", extra))
	if status1 != http.StatusBadRequest {
		t.Fatalf("first delivery: status = %d, want 400, body: %s", status1, raw1)
	}

	status2, raw2 := postHook(t, h, s, envelope(t, s, "nonce-refused-b", extra))
	if status2 != http.StatusBadRequest || !bytes.Equal(raw1, raw2) {
		t.Errorf("replay = %d %s, want the first delivery's 400 %s", status2, raw2, raw1)
	}
	if f.EnsureCalls != 1 {
		t.Errorf("EnsureCalls = %d, want 1 — a 400 refusal must still be replayed from the cache", f.EnsureCalls)
	}
}

// entered is buffered so a second Observe call never blocks on the signal itself.
type blockingObserveResource struct {
	mu           sync.Mutex
	observeCalls int
	entered      chan struct{}
	release      chan struct{}
}

func (r *blockingObserveResource) Ensure(context.Context, contract.Desired) error { return nil }

func (r *blockingObserveResource) Observe(context.Context, contract.Desired) (contract.Observation, error) {
	r.mu.Lock()
	r.observeCalls++
	r.mu.Unlock()
	r.entered <- struct{}{}
	<-r.release
	return contract.Observation{State: contract.Present}, nil
}

func (r *blockingObserveResource) Delete(context.Context, contract.Desired) (map[string]string, error) {
	return nil, nil
}

func (r *blockingObserveResource) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.observeCalls
}

func TestServe_IdempotencyValidateNeverJoinsAnInFlightCall(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	r := &blockingObserveResource{entered: make(chan struct{}, 8), release: make(chan struct{})}
	h := newHandler(t, r, s)

	extra := map[string]any{"operation": "validate", "idempotencyKey": "idem-validate-sf", "subscriptionId": "sub-1"}

	done := make(chan struct{}, 2)
	go func() {
		postHook(t, h, s, envelope(t, s, "nonce-validate-sf-a", extra))
		done <- struct{}{}
	}()

	select {
	case <-r.entered:
	case <-time.After(5 * time.Second):
		close(r.release)
		t.Fatal("the first validate never reached Observe")
	}

	go func() {
		postHook(t, h, s, envelope(t, s, "nonce-validate-sf-b", extra))
		done <- struct{}{}
	}()

	select {
	case <-r.entered:
	case <-time.After(2 * time.Second):
		t.Error("a second validate of the same key did not reach Observe while the first was in flight — a validate must never join the single-flight wait")
	}

	close(r.release)
	<-done
	<-done

	if calls := r.calls(); calls != 2 {
		t.Errorf("ObserveCalls = %d, want 2 — each validate must observe the resource itself", calls)
	}
}
