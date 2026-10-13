package handler_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// TestServe_UnsubscribeAbsentTrueOnlyAfterObserveReportsAbsent pins the
// success path: Delete succeeds, Observe settles to Absent within budget,
// and the response says absent:true.
func TestServe_UnsubscribeAbsentTrueOnlyAfterObserveReportsAbsent(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{Count: 1} // Delete drives Count to 0 -> Observe reports Absent
	h := newHandler(t, f, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-unsub-absent", map[string]any{"operation": "unsubscribe"}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Absent == nil || *resp.Absent != true {
		t.Errorf("Absent = %v, want a non-nil true", resp.Absent)
	}
	if resp.Error != "" {
		t.Errorf("Error = %q, want empty", resp.Error)
	}
}

// TestServe_UnsubscribeAbsentFalseWithDetailForPresent pins unsubscribe's
// deliberate divergence from Observation.Response(): unlike validate,
// unsubscribe always answers a definite boolean, so a resource still
// observed Present after Delete answers absent:false with a detail, not an
// omitted marker.
func TestServe_UnsubscribeAbsentFalseWithDetailForPresent(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	present := contract.Present
	f := &kittest.FakeResource{Count: 1, ObserveState: &present} // stays Present however Delete leaves Count
	h := newHandler(t, f, s, func(c *handler.Config) { c.VerbBudget = 2 })

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-unsub-present", map[string]any{"operation": "unsubscribe"}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Absent == nil || *resp.Absent != false {
		t.Errorf("Absent = %v, want a non-nil false (unsubscribe always answers a definite boolean)", resp.Absent)
	}
	if resp.StatusDetail == "" {
		t.Error("StatusDetail is empty, want it to say what was observed and how many attempts were made")
	}
	if resp.Error != "" {
		t.Errorf("Error = %q, want empty — a still-Present resource after budget exhaustion is not itself an error", resp.Error)
	}
}

// TestServe_UnsubscribeAbsentFalseWithDetailForUnknown mirrors the Present
// case for Unknown: validate would omit "absent" entirely for Unknown, but
// unsubscribe still answers a definite false.
func TestServe_UnsubscribeAbsentFalseWithDetailForUnknown(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	unknown := contract.Unknown
	f := &kittest.FakeResource{Count: 1, ObserveState: &unknown}
	h := newHandler(t, f, s, func(c *handler.Config) { c.VerbBudget = 2 })

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-unsub-unknown", map[string]any{"operation": "unsubscribe"}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Absent == nil || *resp.Absent != false {
		t.Errorf("Absent = %v, want a non-nil false even for Unknown — unsubscribe never fails open the way validate does", resp.Absent)
	}
	if resp.StatusDetail == "" {
		t.Error("StatusDetail is empty, want it to say what was observed and how many attempts were made")
	}
}

// TestServe_UnsubscribeObserveErrorAfterSuccessfulDeleteIsAnErrorResponse
// pins that an Observe error after Delete succeeded is classified as an
// error response — not folded into absent:false.
func TestServe_UnsubscribeObserveErrorAfterSuccessfulDeleteIsAnErrorResponse(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{Count: 1, ObserveErr: errors.New("observe backend unavailable")}
	h := newHandler(t, f, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-unsub-observe-err", map[string]any{"operation": "unsubscribe"}))
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for a plain Observe error, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Error == "" {
		t.Error("Error is empty, want the Observe failure surfaced as an error")
	}
	if resp.Absent != nil && *resp.Absent == false {
		t.Error("Absent = false, want no absent:false — an Observe error after Delete is an error response, not a definite absent marker")
	}
	if f.DeleteCalls != 1 {
		t.Errorf("DeleteCalls = %d, want 1 — Delete must have succeeded before Observe errored", f.DeleteCalls)
	}
}
