package handler_test

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

func TestHookConstants_AreTheContractHookPoints(t *testing.T) {
	got := []string{handler.HookBeforeEnsure, handler.HookAfterEnsure, handler.HookBeforeDelete, handler.HookAfterDelete}
	if want := contract.LifecycleHookPoints(); !reflect.DeepEqual(got, want) {
		t.Fatalf("handler hook constants = %v, contract.LifecycleHookPoints() = %v", got, want)
	}
}

// goldenHooks registers a hook for every name the shared golden declares;
// the beforeEnsure hook increments *beforeEnsureCalls.
func goldenHooks(beforeEnsureCalls *int) map[string]handler.Hook {
	hooks := map[string]handler.Hook{}
	for _, name := range contract.HooksManifestGoldenDeclares() {
		hooks[name] = noopHook
	}
	hooks[handler.HookBeforeEnsure] = func(context.Context, contract.Desired) error {
		*beforeEnsureCalls++
		return nil
	}
	return hooks
}

func TestNew_AcceptsTheSharedHooksGoldenWithEveryDeclaredHookRegistered(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.Manifest = contract.HooksManifestGolden()
	cfg.Hooks = map[string]handler.Hook{}
	for _, name := range contract.HooksManifestGoldenDeclares() {
		cfg.Hooks[name] = noopHook
	}

	if _, err := handler.New(cfg); err != nil {
		t.Fatalf("handler.New refused the shared hooks golden with its declared hooks registered: %v", err)
	}
}

func TestServe_SharedHooksGoldenReachesTheBeforeEnsureHook(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}

	var calls int
	h := newHandler(t, f, s, func(c *handler.Config) {
		c.Manifest = contract.HooksManifestGolden()
		c.Hooks = goldenHooks(&calls)
	})

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-golden-hook", nil))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	if calls != 1 {
		t.Errorf("beforeEnsure hook ran %d times on one subscribe, want 1", calls)
	}
	if f.EnsureCalls == 0 {
		t.Error("subscribe never reached Ensure")
	}
}

func TestNew_RefusesAHookTheSharedGoldenDoesNotDeclare(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.Manifest = contract.HooksManifestGolden()
	cfg.Hooks = map[string]handler.Hook{
		handler.HookAfterEnsure: noopHook,
	}

	_, err := handler.New(cfg)
	if err == nil {
		t.Fatal("handler.New accepted afterEnsure, which the shared golden does not declare")
	}
	if !strings.Contains(err.Error(), handler.HookAfterEnsure) {
		t.Errorf("error %q does not name the undeclared hook %q", err.Error(), handler.HookAfterEnsure)
	}
}
