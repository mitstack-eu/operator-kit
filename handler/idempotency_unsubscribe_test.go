package handler_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// An unsubscribe answer of absent:false says the resource was still observed
// after the verb budget ran out, and the engine re-drives the same key to
// learn when it has gone. An absence answer must come from observing the
// resource, so a stored absent:false would pin every re-delivery to "not yet
// absent" for the whole TTL. A settled absent:true answer remains stored.

// TestServe_IdempotencyUnsubscribeNotAbsentIsNeverCached pins that an
// unsubscribe answered absent:false leaves no cache entry: a re-delivery of
// the same key and subscription runs Delete and Observe again instead of
// replaying the first answer.
func TestServe_IdempotencyUnsubscribeNotAbsentIsNeverCached(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	present := contract.Present
	f := &kittest.FakeResource{Count: 1, ObserveState: &present}
	h := newHandler(t, f, s, func(c *handler.Config) { c.VerbBudget = 2 })

	extra := map[string]any{"operation": "unsubscribe", "idempotencyKey": "idem-unsub-present", "subscriptionId": "sub-1"}

	status1, raw1 := postHook(t, h, s, envelope(t, s, "nonce-unsub-present-a", extra))
	if status1 != http.StatusOK {
		t.Fatalf("first delivery: status = %d, body: %s", status1, raw1)
	}
	if resp := decodeResponse(t, raw1); resp.Absent == nil || *resp.Absent {
		t.Fatalf("first delivery Absent = %v, want a non-nil false — the fixture must stay present, body: %s", resp.Absent, raw1)
	}
	if f.DeleteCalls != 1 || f.ObserveCalls != 2 {
		t.Fatalf("after the first delivery Delete=%d Observe=%d, want Delete=1 Observe=2", f.DeleteCalls, f.ObserveCalls)
	}

	status2, raw2 := postHook(t, h, s, envelope(t, s, "nonce-unsub-present-b", extra))
	if f.DeleteCalls != 2 {
		t.Errorf("DeleteCalls after the re-delivery = %d, want 2 — an absent:false answer must never be cached", f.DeleteCalls)
	}
	if f.ObserveCalls != 4 {
		t.Errorf("ObserveCalls after the re-delivery = %d, want 4 — the re-delivery must observe the resource again", f.ObserveCalls)
	}
	if status2 != http.StatusOK {
		t.Fatalf("re-delivery: status = %d, body: %s", status2, raw2)
	}
	if resp := decodeResponse(t, raw2); resp.Absent == nil || *resp.Absent {
		t.Errorf("re-delivery Absent = %v, want a non-nil false from its own observation, body: %s", resp.Absent, raw2)
	}
}

// TestServe_IdempotencyUnsubscribeAbsentIsStillDeduplicated pins that the
// narrowing above leaves a settled absent:true answer stored: a re-delivery
// of the same key replays the identical bytes without calling Delete or
// Observe again.
func TestServe_IdempotencyUnsubscribeAbsentIsStillDeduplicated(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{Count: 1} // Delete drives Count to 0, so Observe reports Absent
	h := newHandler(t, f, s)

	extra := map[string]any{"operation": "unsubscribe", "idempotencyKey": "idem-unsub-absent", "subscriptionId": "sub-1"}

	status1, raw1 := postHook(t, h, s, envelope(t, s, "nonce-unsub-absent-a", extra))
	if status1 != http.StatusOK {
		t.Fatalf("first delivery: status = %d, body: %s", status1, raw1)
	}
	if resp := decodeResponse(t, raw1); resp.Absent == nil || !*resp.Absent {
		t.Fatalf("first delivery Absent = %v, want a non-nil true, body: %s", resp.Absent, raw1)
	}
	observeAfterFirst := f.ObserveCalls

	status2, raw2 := postHook(t, h, s, envelope(t, s, "nonce-unsub-absent-b", extra))
	if status2 != status1 {
		t.Errorf("replay status = %d, want %d", status2, status1)
	}
	if !bytes.Equal(raw1, raw2) {
		t.Errorf("replay body = %s, want the exact first body %s", raw2, raw1)
	}
	if f.DeleteCalls != 1 {
		t.Errorf("DeleteCalls = %d, want 1 — a settled absent:true unsubscribe must still be replayed from the cache", f.DeleteCalls)
	}
	if f.ObserveCalls != observeAfterFirst {
		t.Errorf("ObserveCalls = %d, want still %d — the replay must not observe the resource", f.ObserveCalls, observeAfterFirst)
	}
}
