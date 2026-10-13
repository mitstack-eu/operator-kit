package handler_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// bothHooksGoldens names each golden a block may be written as.
func bothHooksGoldens() map[string][]byte {
	return map[string][]byte{
		"kind: Block golden": contract.HooksManifestGolden(),
		"type: block twin":   contract.TypeBlockHooksManifestGolden(),
	}
}

func declaredGoldenHooks() map[string]handler.Hook {
	hooks := map[string]handler.Hook{}
	for _, name := range contract.HooksManifestGoldenDeclares() {
		hooks[name] = noopHook
	}
	return hooks
}

func TestNew_AcceptsTheTypeBlockTwinWithEveryDeclaredHookRegistered(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = contract.TypeBlockHooksManifestGolden()
	cfg.Hooks = declaredGoldenHooks()

	if _, err := handler.New(cfg); err != nil {
		t.Fatalf("handler.New refused the type: block twin with its declared hooks registered: %v", err)
	}
}

func TestNew_RefusesAHookTheTypeBlockTwinDoesNotDeclare(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = contract.TypeBlockHooksManifestGolden()
	cfg.Hooks = map[string]handler.Hook{handler.HookAfterEnsure: noopHook}

	_, err := handler.New(cfg)
	if err == nil {
		t.Fatal("handler.New accepted afterEnsure, which the type: block twin does not declare")
	}
	if !strings.Contains(err.Error(), handler.HookAfterEnsure) {
		t.Errorf("error %q does not name the undeclared hook %q", err.Error(), handler.HookAfterEnsure)
	}
}

func TestServe_TypeBlockTwinReachesTheBeforeEnsureHook(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}

	var calls int
	h := newHandler(t, f, s, func(c *handler.Config) {
		c.Manifest = contract.TypeBlockHooksManifestGolden()
		c.Hooks = declaredGoldenHooks()
		c.Hooks[handler.HookBeforeEnsure] = func(context.Context, contract.Desired) error {
			calls++
			return nil
		}
	})

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-type-block-hook", nil))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	if calls != 1 {
		t.Errorf("beforeEnsure hook ran %d times on one subscribe, want 1", calls)
	}
}

// unsubscribeAnswer drives one unsubscribe against a Resource that keeps
// reporting Present, and returns what the handler answered and how often it
// polled Observe: the two things a retainsOnUnsubscribe declaration changes.
type unsubscribeAnswer struct {
	absent       *bool
	observeCalls int
	deleteCalls  int
}

func unsubscribeWith(t *testing.T, manifest []byte, nonce string) unsubscribeAnswer {
	t.Helper()
	s := kittest.NewSigner(t, "kid-1", 7)
	present := contract.Present
	f := &kittest.FakeResource{Count: 1, ObserveState: &present}
	h := newHandler(t, f, s, func(c *handler.Config) {
		c.Manifest = manifest
		c.Hooks = declaredGoldenHooks()
		c.VerbBudget = 3
	})

	status, raw := postHook(t, h, s, envelope(t, s, nonce, map[string]any{"operation": "unsubscribe"}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	resp := decodeResponse(t, raw)
	return unsubscribeAnswer{absent: resp.Absent, observeCalls: f.ObserveCalls, deleteCalls: f.DeleteCalls}
}

func TestServe_TypeBlockTwinAnswersUnsubscribeAsTheHooksGoldenDoes(t *testing.T) {
	answers := map[string]unsubscribeAnswer{}
	for name, manifest := range bothHooksGoldens() {
		answers[name] = unsubscribeWith(t, manifest, "nonce-unsub-"+strings.ReplaceAll(name, " ", "-"))
	}

	old, twin := answers["kind: Block golden"], answers["type: block twin"]
	if old.absent == nil || twin.absent == nil {
		t.Fatalf("an unsubscribe answer carried no absent marker: golden %v, twin %v", old.absent, twin.absent)
	}
	if *old.absent != *twin.absent {
		t.Errorf("absent = %v for the twin, %v for the golden", *twin.absent, *old.absent)
	}
	if old.observeCalls != twin.observeCalls {
		t.Errorf("ObserveCalls = %d for the twin, %d for the golden", twin.observeCalls, old.observeCalls)
	}
	if old.deleteCalls != twin.deleteCalls {
		t.Errorf("DeleteCalls = %d for the twin, %d for the golden", twin.deleteCalls, old.deleteCalls)
	}
}
