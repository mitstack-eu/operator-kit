package handler_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// manifestWithRetains writes the value verbatim so a test can hand it a non-boolean.
func manifestWithRetains(value string, hooks ...string) []byte {
	var b strings.Builder
	b.WriteString("apiVersion: mitstack/v1alpha\n")
	b.WriteString("kind: Block\n")
	b.WriteString("metadata:\n  id: test-block\n  name: Test Block\n")
	b.WriteString("spec:\n  layer: infra\n  lifecycle:\n")
	b.WriteString("    retainsOnUnsubscribe: " + value + "\n")
	if len(hooks) > 0 {
		b.WriteString("    hooks:\n")
		for _, h := range hooks {
			b.WriteString("      - " + h + "\n")
		}
	}
	return []byte(b.String())
}

const manifestWithDecoyTopLevelRetains = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: test-block
  name: Test Block
retainsOnUnsubscribe: true
spec:
  layer: infra
  lifecycle:
    hooks: []
`

// retainsHandoverFake keeps reporting Present after its Delete succeeded and returned its handover outputs.
type retainsHandoverFake struct {
	Outputs map[string]string

	DeleteCalls  int
	ObserveCalls int
}

func (f *retainsHandoverFake) Ensure(ctx context.Context, d contract.Desired) error { return nil }

func (f *retainsHandoverFake) Observe(ctx context.Context, d contract.Desired) (contract.Observation, error) {
	f.ObserveCalls++
	return contract.Observation{State: contract.Present}, nil
}

func (f *retainsHandoverFake) Delete(ctx context.Context, d contract.Desired) (map[string]string, error) {
	f.DeleteCalls++
	return f.Outputs, nil
}

var _ handler.Resource = (*retainsHandoverFake)(nil)

func TestServe_UnsubscribeAbsentTrueAfterOneDeleteZeroObserveWhenManifestDeclaresRetainsOnUnsubscribe(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	present := contract.Present
	f := &kittest.FakeResource{Count: 1, ObserveState: &present} // Observe keeps answering Present however Delete leaves Count
	h := newHandler(t, f, s, func(c *handler.Config) {
		c.Manifest = manifestWithRetains("true")
		c.VerbBudget = 5
	})

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-retains-absent-true", map[string]any{"operation": "unsubscribe"}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Absent == nil || *resp.Absent != true {
		t.Errorf("Absent = %v, want a non-nil true", resp.Absent)
	}
	if f.DeleteCalls != 1 {
		t.Errorf("DeleteCalls = %d, want exactly 1", f.DeleteCalls)
	}
	if f.ObserveCalls != 0 {
		t.Errorf("ObserveCalls = %d, want 0 — a declared retainsOnUnsubscribe handler must never poll Observe on unsubscribe", f.ObserveCalls)
	}
}

func TestServe_UnsubscribeAbsentFalseAfterFullBudgetWhenManifestDoesNotDeclareRetainsOnUnsubscribe(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	present := contract.Present
	f := &kittest.FakeResource{Count: 1, ObserveState: &present}
	h := newHandler(t, f, s, func(c *handler.Config) { c.VerbBudget = 3 }) // no Manifest at all

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-retains-default-absent-false", map[string]any{"operation": "unsubscribe"}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Absent == nil || *resp.Absent != false {
		t.Errorf("Absent = %v, want a non-nil false — the default is unchanged", resp.Absent)
	}
	if f.ObserveCalls != 3 {
		t.Errorf("ObserveCalls = %d, want exactly the VerbBudget (3)", f.ObserveCalls)
	}
}

func TestServe_UnsubscribeAbsentFalseAfterFullBudgetWhenManifestDeclaresRetainsOnUnsubscribeFalse(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	present := contract.Present
	f := &kittest.FakeResource{Count: 1, ObserveState: &present}
	h := newHandler(t, f, s, func(c *handler.Config) {
		c.Manifest = manifestWithRetains("false")
		c.VerbBudget = 3
	})

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-retains-explicit-false", map[string]any{"operation": "unsubscribe"}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Absent == nil || *resp.Absent != false {
		t.Errorf("Absent = %v, want a non-nil false — an explicit retainsOnUnsubscribe: false behaves like no declaration", resp.Absent)
	}
	if f.ObserveCalls != 3 {
		t.Errorf("ObserveCalls = %d, want exactly the VerbBudget (3)", f.ObserveCalls)
	}
}

func TestServe_UnsubscribeFailedDeleteAnswersErrorNotAbsentTrueWhenRetainsOnUnsubscribeDeclared(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{Count: 1, DeleteErr: errors.New("delete backend unavailable")}
	h := newHandler(t, f, s, func(c *handler.Config) {
		c.Manifest = manifestWithRetains("true")
	})

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-retains-delete-err", map[string]any{"operation": "unsubscribe"}))
	if status == http.StatusOK {
		t.Fatalf("status = 200, want an error status, body: %s", raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Error == "" {
		t.Error("Error is empty, want the Delete failure surfaced as an error")
	}
	if resp.Absent != nil && *resp.Absent == true {
		t.Error("Absent = true, want no absent:true — a failed Delete must never be answered as absent, declaration or not")
	}
	if f.ObserveCalls != 0 {
		t.Errorf("ObserveCalls = %d, want 0 — settle must never run after a failed Delete", f.ObserveCalls)
	}
}

func TestServe_UnsubscribeFailedAfterDeleteHookAnswersErrorNotAbsentTrueWhenRetainsOnUnsubscribeDeclared(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{Count: 1}
	hookErr := errors.New("afterDelete hook failed")
	h := newHandler(t, f, s, func(c *handler.Config) {
		c.Manifest = manifestWithRetains("true", "afterDelete")
		c.Hooks = map[string]handler.Hook{
			handler.HookAfterDelete: func(ctx context.Context, d contract.Desired) error { return hookErr },
		}
	})

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-retains-afterdelete-err", map[string]any{"operation": "unsubscribe"}))
	if status == http.StatusOK {
		t.Fatalf("status = 200, want an error status, body: %s", raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Absent != nil && *resp.Absent == true {
		t.Error("Absent = true, want no absent:true — a failed afterDelete hook must never be answered as absent")
	}
	if f.DeleteCalls != 1 {
		t.Errorf("DeleteCalls = %d, want 1 — Delete itself must have run before the failing hook", f.DeleteCalls)
	}
	if f.ObserveCalls != 0 {
		t.Errorf("ObserveCalls = %d, want 0 — settle must never run after a failed afterDelete hook", f.ObserveCalls)
	}
}

func TestServe_UnsubscribeHandoverOutputsLandInAbsentTrueWithZeroObserveWhenRetainsOnUnsubscribeDeclared(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &retainsHandoverFake{Outputs: map[string]string{"dataSnapshotId": "snap-789"}}
	h := newHandler(t, f, s, func(c *handler.Config) {
		c.Manifest = manifestWithRetains("true")
	})

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-retains-handover", map[string]any{"operation": "unsubscribe"}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Absent == nil || *resp.Absent != true {
		t.Fatalf("Absent = %v, want a non-nil true", resp.Absent)
	}
	if got := resp.Outputs["dataSnapshotId"]; got != "snap-789" {
		t.Errorf(`Outputs["dataSnapshotId"] = %q, want %q`, got, "snap-789")
	}
	if f.DeleteCalls != 1 {
		t.Errorf("DeleteCalls = %d, want 1", f.DeleteCalls)
	}
	if f.ObserveCalls != 0 {
		t.Errorf("ObserveCalls = %d, want 0 — a declared retainsOnUnsubscribe handler must never poll Observe, a Delete returning outputs included", f.ObserveCalls)
	}
}

func TestNew_ErrorsWhenRetainsOnUnsubscribeValueIsNotABoolean(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.Manifest = manifestWithRetains("maybe")

	_, err := handler.New(cfg)
	if err == nil {
		t.Fatal("expected an error for a retainsOnUnsubscribe value that is neither true nor false, got none")
	}
	if !strings.Contains(err.Error(), "retainsOnUnsubscribe") {
		t.Errorf("error %q does not name the field retainsOnUnsubscribe", err.Error())
	}
	requireNewRefusesWithTheParsersMessage(t, cfg, "spec.lifecycle.retainsOnUnsubscribe")
}

func TestNew_DecoyTopLevelRetainsOnUnsubscribeIsRefusedAsUnknown(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = []byte(manifestWithDecoyTopLevelRetains)

	requireNewRefusesWithTheParsersMessage(t, cfg, "retainsOnUnsubscribe")
}

func TestServe_ValidateStillCallsObserveOnceWhenRetainsOnUnsubscribeDeclared(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{Count: 1}
	h := newHandler(t, f, s, func(c *handler.Config) {
		c.Manifest = manifestWithRetains("true")
	})

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-retains-validate", map[string]any{"operation": "validate"}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	if f.ObserveCalls != 1 {
		t.Errorf("ObserveCalls = %d, want exactly 1 — validate is unaffected by retainsOnUnsubscribe", f.ObserveCalls)
	}
	if f.DeleteCalls != 0 {
		t.Errorf("DeleteCalls = %d, want 0 — validate never deletes", f.DeleteCalls)
	}
}
