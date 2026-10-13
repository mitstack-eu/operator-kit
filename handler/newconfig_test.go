package handler_test

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

func manifestDeclaring(hooks ...string) []byte {
	var b strings.Builder
	b.WriteString("apiVersion: mitstack/v1alpha\n")
	b.WriteString("kind: Block\n")
	b.WriteString("metadata:\n  id: test-block\n  name: Test Block\n")
	b.WriteString("spec:\n  layer: infra\n  lifecycle:\n")
	if len(hooks) == 0 {
		b.WriteString("    hooks: []\n")
	} else {
		b.WriteString("    hooks:\n")
		for _, h := range hooks {
			b.WriteString("      - " + h + "\n")
		}
	}
	return []byte(b.String())
}

const manifestWithNoHooksKey = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: test-block
  name: Test Block
spec:
  layer: infra
`

func noopHook(context.Context, contract.Desired) error { return nil }

func TestNew_ErrorsWhenResourceIsNil(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, nil, s)

	if _, err := handler.New(cfg); err == nil {
		t.Fatal("expected an error for a nil Resource, got none")
	}
}

func TestNew_ErrorsOnUnknownHookPoint(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.Manifest = manifestDeclaring("beforeEnsure")
	cfg.Hooks = map[string]handler.Hook{
		"onFrobnicate": noopHook, // not one of the four HookXxx constants
	}

	_, err := handler.New(cfg)
	if err == nil {
		t.Fatal("expected an error for an unknown hook point, got none")
	}
	if !strings.Contains(err.Error(), "onFrobnicate") {
		t.Errorf("error %q does not name the offending hook %q", err.Error(), "onFrobnicate")
	}
	for _, known := range []string{handler.HookBeforeEnsure, handler.HookAfterEnsure, handler.HookBeforeDelete, handler.HookAfterDelete} {
		if !strings.Contains(err.Error(), known) {
			t.Errorf("error %q does not list known hook point %q", err.Error(), known)
		}
	}
}

func TestNew_ErrorsOnHookNotDeclaredInManifest(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.Manifest = manifestDeclaring("afterDelete")
	cfg.Hooks = map[string]handler.Hook{
		handler.HookBeforeEnsure: noopHook,
	}

	_, err := handler.New(cfg)
	if err == nil {
		t.Fatal("expected an error for a hook not declared in the manifest, got none")
	}
	if !strings.Contains(err.Error(), handler.HookBeforeEnsure) {
		t.Errorf("error %q does not name the offending hook %q", err.Error(), handler.HookBeforeEnsure)
	}
	if !strings.Contains(err.Error(), "afterDelete") {
		t.Errorf("error %q does not name what the manifest actually declares (%q)", err.Error(), "afterDelete")
	}
}

func TestNew_ErrorsWhenHooksNonEmptyAndManifestEmpty(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.Manifest = nil
	cfg.Hooks = map[string]handler.Hook{
		handler.HookBeforeEnsure: noopHook,
	}

	if _, err := handler.New(cfg); err == nil {
		t.Fatal("expected an error when Hooks is set but Manifest is empty, got none")
	}
}

func TestNew_AcceptsADeclaredAndRegisteredHookAndCallsItAtItsPoint(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}

	var called bool
	h := newHandler(t, f, s, func(c *handler.Config) {
		c.Manifest = manifestDeclaring("beforeEnsure")
		c.Hooks = map[string]handler.Hook{
			handler.HookBeforeEnsure: func(ctx context.Context, d contract.Desired) error {
				called = true
				if f.EnsureCalls != 0 {
					t.Error("beforeEnsure ran after Ensure, not before it")
				}
				return nil
			},
		}
	})

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-hook-pos", nil))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	if !called {
		t.Error("the declared-and-registered beforeEnsure hook was never called")
	}
}

func TestNew_ManifestDeclaringAHookNobodyRegistersIsAccepted(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s, func(c *handler.Config) {
		c.Manifest = manifestDeclaring("beforeEnsure", "afterEnsure")
	})

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-hook-unused", nil))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	if f.EnsureCalls != 1 {
		t.Errorf("EnsureCalls = %d, want 1 — an unregistered declared hook must not change verb dispatch", f.EnsureCalls)
	}
}

func TestNew_EmptyManifestHooksListDeclaresNone(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.Manifest = manifestDeclaring() // "hooks: []"
	cfg.Hooks = map[string]handler.Hook{
		handler.HookBeforeEnsure: noopHook,
	}

	if _, err := handler.New(cfg); err == nil {
		t.Fatal("expected an error: \"hooks: []\" declares no hook, so beforeEnsure is undeclared")
	}
}

func TestNew_ManifestWithNoHooksKeyDeclaresNone(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.Manifest = []byte(manifestWithNoHooksKey)
	cfg.Hooks = map[string]handler.Hook{
		handler.HookAfterDelete: noopHook,
	}

	if _, err := handler.New(cfg); err == nil {
		t.Fatal("expected an error: a manifest with no hooks: key declares no hook")
	}
}

func TestNew_ErrorsWhenJWKSURLIsEmpty(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.JWKSURL = ""

	_, err := handler.New(cfg)
	if err == nil {
		t.Fatal("expected an error for an empty JWKSURL, got none")
	}
	if !strings.Contains(err.Error(), "JWKSURL") {
		t.Errorf("error %q does not name JWKSURL", err.Error())
	}
}

func TestNew_ErrorsWhenJWKSURLIsEmptyEvenWithPublicKeyOverride(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.JWKSURL = ""
	cfg.PublicKey = func(kid string) (ed25519.PublicKey, error) { return s.Public, nil }

	_, err := handler.New(cfg)
	if err == nil {
		t.Fatal("expected an error for an empty JWKSURL even with Config.PublicKey set, got none")
	}
	if !strings.Contains(err.Error(), "JWKSURL") {
		t.Errorf("error %q does not name JWKSURL", err.Error())
	}
}

const manifestWithDecoyTopLevelHooks = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: test-block
  name: Test Block
hooks:
  - decoyHook
spec:
  layer: infra
  lifecycle:
    hooks:
      - beforeEnsure
`

func TestNew_ManifestAnchoring_DecoyHooksKeyAboveSpecLifecycleIsRefusedAsUnknown(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = []byte(manifestWithDecoyTopLevelHooks)
	cfg.Hooks = map[string]handler.Hook{handler.HookBeforeEnsure: noopHook}

	requireNewRefusesWithTheParsersMessage(t, cfg, "hooks")
}

const manifestWithDecoyTooDeepHooks = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: test-block
  name: Test Block
spec:
  layer: infra
  lifecycle:
    extra:
      hooks:
        - afterEnsure
    hooks:
      - beforeEnsure
`

func TestNew_ManifestAnchoring_DecoyHooksKeyOneLevelTooDeepIsRefusedAsUnknown(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = []byte(manifestWithDecoyTooDeepHooks)
	cfg.Hooks = map[string]handler.Hook{handler.HookAfterEnsure: noopHook}

	requireNewRefusesWithTheParsersMessage(t, cfg, "spec.lifecycle.extra")
}

func manifestWithLifecycleHooksLine(hooksLine string) []byte {
	return []byte("apiVersion: mitstack/v1alpha\n" +
		"kind: Block\n" +
		"metadata:\n  id: test-block\n  name: Test Block\n" +
		"spec:\n  layer: infra\n  lifecycle:\n" +
		"    " + hooksLine + "\n")
}

func TestNew_ManifestFlowFormHooksListIsRead(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	var called bool
	h := newHandler(t, &kittest.FakeResource{}, s, func(c *handler.Config) {
		c.Manifest = manifestWithLifecycleHooksLine("hooks: [beforeEnsure]")
		c.Hooks = map[string]handler.Hook{
			handler.HookBeforeEnsure: func(context.Context, contract.Desired) error {
				called = true
				return nil
			},
		}
	})

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-manifest-flow-form", nil))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	if !called {
		t.Error("beforeEnsure was never called — New must read the flow form \"hooks: [beforeEnsure]\"")
	}
}

// No hook is registered on purpose: a registered one would fail with the ordinary "hook not declared" refusal and mask that a value nobody can read must not be mistaken for a declaration of nothing.
func TestNew_ManifestUnreadableHooksValueIsRefused(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.Manifest = manifestWithLifecycleHooksLine("hooks: beforeEnsure")

	_, err := handler.New(cfg)
	if err == nil {
		t.Fatal("expected an error for a hooks: value that fits no supported form, got none — " +
			"an unreadable declaration must not be silently treated as declaring nothing")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "manifest") {
		t.Errorf("error %q does not name the manifest", err.Error())
	}
	requireNewRefusesWithTheParsersMessage(t, cfg, contract.LifecycleHooksField)
}

const manifestWithHooksBlockCommentBetweenItems = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: test-block
  name: Test Block
spec:
  layer: infra
  lifecycle:
    hooks:
      - beforeEnsure
      # a comment between two items
      - afterDelete
`

func TestNew_ManifestCommentBetweenHookItemsDeclaresBoth(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.Manifest = []byte(manifestWithHooksBlockCommentBetweenItems)
	cfg.Hooks = map[string]handler.Hook{
		handler.HookBeforeEnsure: noopHook,
		handler.HookAfterDelete:  noopHook,
	}

	if _, err := handler.New(cfg); err != nil {
		t.Fatalf("handler.New: unexpected error — both beforeEnsure and afterDelete are declared either side of the comment: %v", err)
	}
}

func TestNew_ManifestCommentBetweenHookItemsErrorListsBothDeclaredHooks(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.Manifest = []byte(manifestWithHooksBlockCommentBetweenItems)
	cfg.Hooks = map[string]handler.Hook{
		handler.HookBeforeDelete: noopHook, // declared by neither item
	}

	_, err := handler.New(cfg)
	if err == nil {
		t.Fatal("expected an error: beforeDelete is declared by neither item, got none")
	}
	if !strings.Contains(err.Error(), "beforeEnsure") {
		t.Errorf("error %q does not list beforeEnsure among what the manifest declares", err.Error())
	}
	if !strings.Contains(err.Error(), "afterDelete") {
		t.Errorf("error %q does not list afterDelete among what the manifest declares — "+
			"it must never claim the manifest fails to declare a hook it does declare", err.Error())
	}
}

const manifestWithTabIndentedHooksSequence = "apiVersion: mitstack/v1alpha\n" +
	"kind: Block\n" +
	"metadata:\n  id: test-block\n  name: Test Block\n" +
	"spec:\n  layer: infra\n  lifecycle:\n" +
	"    hooks:\n" +
	"\t- beforeEnsure\n" +
	"\t- afterDelete\n"

func TestNew_ManifestTabIndentedHooksSequenceFailsNamingManifest(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.Manifest = []byte(manifestWithTabIndentedHooksSequence)
	// cfg.Hooks stays nil so the only possible error is the manifest read itself.

	_, err := handler.New(cfg)
	if err == nil {
		t.Fatal("expected an error for a tab-indented hooks sequence, got none — " +
			"a tab is not valid indentation and must not be read as an empty declaration")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "manifest") {
		t.Errorf("error %q does not name the manifest", err.Error())
	}
	if strings.Contains(err.Error(), "does not declare it") {
		t.Errorf("error %q comes from the \"hook not declared\" refusal, not the reader's own error path — "+
			"got here with no hook registered, so declaredSet must never have been consulted", err.Error())
	}
	if f.EnsureCalls != 0 || f.ObserveCalls != 0 || f.DeleteCalls != 0 {
		t.Errorf("Resource was called before New even returned: Ensure=%d Observe=%d Delete=%d",
			f.EnsureCalls, f.ObserveCalls, f.DeleteCalls)
	}
}

const manifestWithSiblingSpecOtherHooksStanza = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: test-block
  name: Test Block
spec:
  layer: infra
  lifecycle:
    hooks:
      - beforeEnsure
  other:
    hooks:
      - afterDelete
`

func TestNew_ManifestSiblingSpecOtherHooksStanzaIsRefusedAsUnknown(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = []byte(manifestWithSiblingSpecOtherHooksStanza)
	cfg.Hooks = map[string]handler.Hook{handler.HookBeforeEnsure: noopHook}

	requireNewRefusesWithTheParsersMessage(t, cfg, "spec.other")
}

const manifestWithCommentedOutHooksKey = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: test-block
  name: Test Block
spec:
  layer: infra
  lifecycle:
    # hooks:
    #   - beforeEnsure
`

func TestNew_ManifestCommentedOutHooksKeyDeclaresNone(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.Manifest = []byte(manifestWithCommentedOutHooksKey)
	cfg.Hooks = map[string]handler.Hook{
		handler.HookBeforeEnsure: noopHook,
	}

	if _, err := handler.New(cfg); err == nil {
		t.Fatal("expected an error: a commented-out hooks: line declares no hook, same as no hooks: key at all")
	}
}

func TestNew_ManifestCRLFLineEndingsHooksSequenceIsRead(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	cfg := baseConfig(t, f, s)
	cfg.Manifest = []byte(strings.ReplaceAll(string(manifestDeclaring("beforeEnsure", "afterDelete")), "\n", "\r\n"))
	cfg.Hooks = map[string]handler.Hook{
		handler.HookBeforeEnsure: noopHook,
		handler.HookAfterDelete:  noopHook,
	}

	if _, err := handler.New(cfg); err != nil {
		t.Fatalf("handler.New: unexpected error for a CRLF-terminated manifest: %v", err)
	}
}
