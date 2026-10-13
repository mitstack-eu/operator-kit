package handler_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

func TestServe_ValidateCallsObserveExactlyOnce(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{Count: 1}
	h := newHandler(t, f, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-validate-once", map[string]any{"operation": "validate"}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	if f.ObserveCalls != 1 {
		t.Errorf("ObserveCalls = %d, want exactly 1", f.ObserveCalls)
	}
	if f.EnsureCalls != 0 || f.DeleteCalls != 0 {
		t.Errorf("EnsureCalls=%d DeleteCalls=%d, want 0 and 0 for validate", f.EnsureCalls, f.DeleteCalls)
	}
}

// Unknown leaves absent out, as Observation.Response does, and the handler adds pending: true, which Response cannot carry.
func TestServe_ValidateAnswersObservationResponsePlusPendingOnUnknown(t *testing.T) {
	cases := []struct {
		name        string
		state       contract.State
		wantAbsent  *bool
		wantPending bool
	}{
		{"present", contract.Present, boolPtr(false), false},
		{"absent", contract.Absent, boolPtr(true), false},
		{"unknown", contract.Unknown, nil, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			state := c.state
			f := &kittest.FakeResource{
				ObserveState: &state,
				Outputs:      map[string]string{"k": "v"},
				StatusDetail: "detail-" + c.name,
			}
			h := newHandler(t, f, s)

			status, raw := postHook(t, h, s, envelope(t, s, "nonce-validate-"+c.name, map[string]any{"operation": "validate"}))
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200, body: %s", status, raw)
			}
			assertConforms(t, raw)

			resp := decodeResponse(t, raw)
			if c.wantAbsent == nil {
				if resp.Absent != nil {
					t.Errorf("Absent = %v, want absent to be omitted entirely for Unknown (fail open)", *resp.Absent)
				}
			} else {
				if resp.Absent == nil || *resp.Absent != *c.wantAbsent {
					t.Errorf("Absent = %v, want %v", resp.Absent, *c.wantAbsent)
				}
			}
			if resp.Pending != c.wantPending {
				t.Errorf("Pending = %v, want %v", resp.Pending, c.wantPending)
			}
			if resp.StatusDetail != "detail-"+c.name {
				t.Errorf("StatusDetail = %q, want the observation's own detail", resp.StatusDetail)
			}
			if resp.Outputs["k"] != "v" {
				t.Errorf("Outputs[k] = %q, want the observation's own output", resp.Outputs["k"])
			}
		})
	}
}

func TestServe_ValidateObserveErrorIsNotPending(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{ObserveErr: errors.New("observe backend unavailable")}
	h := newHandler(t, f, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-validate-observe-err", map[string]any{"operation": "validate"}))
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for a plain Observe error, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Pending {
		t.Error("Pending = true, want false — an Observe error is classified, never pending")
	}
	if resp.Error == "" {
		t.Error("Error is empty, want the Observe failure surfaced as an error")
	}
}

func boolPtr(b bool) *bool { return &b }
