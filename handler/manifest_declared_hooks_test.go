package handler_test

import (
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// The platform's manifest parser refuses a spec.lifecycle.hooks declaration
// naming a hook outside contract.LifecycleHookPoints(), or naming one twice,
// and reads every YAML scalar form as its plain value. New must agree with
// it on the same manifest bytes, whatever the Config registers.

// assertDeclarationRefused fails the test unless err is non-nil and names
// both the spec.lifecycle.hooks field and the offending hook name.
func assertDeclarationRefused(t *testing.T, err error, offending string) {
	t.Helper()
	if err == nil {
		t.Fatalf("handler.New accepted a manifest whose spec.lifecycle.hooks declares %q; want a refusal", offending)
	}
	if !strings.Contains(err.Error(), "spec.lifecycle.hooks") {
		t.Errorf("error %q does not name the field spec.lifecycle.hooks", err.Error())
	}
	if !strings.Contains(err.Error(), offending) {
		t.Errorf("error %q does not name the offending hook %q", err.Error(), offending)
	}
}

func TestNew_RefusesADeclaredUnknownHookWithNoHooksRegistered(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = manifestDeclaring("beforeEnsure", "bogusHook")

	_, err := handler.New(cfg)
	assertDeclarationRefused(t, err, "bogusHook")
}

func TestNew_RefusesADeclaredUnknownHookWithAKnownHookRegistered(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = manifestDeclaring("beforeEnsure", "bogusHook")
	cfg.Hooks = map[string]handler.Hook{handler.HookBeforeEnsure: noopHook}

	_, err := handler.New(cfg)
	assertDeclarationRefused(t, err, "bogusHook")
}

func TestNew_RefusesADeclaredUnknownHookInAFlowList(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = manifestWithLifecycleHooksLine("hooks: [beforeEnsure, bogusHook]")

	_, err := handler.New(cfg)
	assertDeclarationRefused(t, err, "bogusHook")
}

func TestNew_RefusesADuplicateDeclaredHookWithNoHooksRegistered(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = manifestDeclaring("beforeEnsure", "afterDelete", "beforeEnsure")

	_, err := handler.New(cfg)
	assertDeclarationRefused(t, err, "beforeEnsure")
}

func TestNew_RefusesADuplicateDeclaredHookWithThatHookRegistered(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = manifestDeclaring("beforeEnsure", "beforeEnsure")
	cfg.Hooks = map[string]handler.Hook{handler.HookBeforeEnsure: noopHook}

	_, err := handler.New(cfg)
	assertDeclarationRefused(t, err, "beforeEnsure")
}

// TestNew_RefusesADuplicateDeclaredHookSpelledPlainAndQuoted pins that a
// quoted item is the same name as its plain spelling, as YAML reads it, so
// declaring both is a duplicate.
func TestNew_RefusesADuplicateDeclaredHookSpelledPlainAndQuoted(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = manifestWithHooksBlock(
		"      - beforeEnsure\n" +
			"      - \"beforeEnsure\"\n")

	_, err := handler.New(cfg)
	assertDeclarationRefused(t, err, "beforeEnsure")
}

// manifestWithHooksBlock builds a manifest whose spec.lifecycle "hooks:" key
// is followed by the given item lines verbatim.
func manifestWithHooksBlock(items string) []byte {
	return []byte("apiVersion: mitstack/v1alpha\n" +
		"kind: Block\n" +
		"metadata:\n  id: test-block\n  name: Test Block\n" +
		"spec:\n  layer: infra\n  lifecycle:\n" +
		"    hooks:\n" + items)
}

// TestNew_ReadsHookNamesAsYAMLScalars pins that each written form yields the
// plain names YAML reads: a registered hook is accepted only when the
// manifest declares exactly its name, so New succeeding proves the reader
// stripped the quotes and the comment.
func TestNew_ReadsHookNamesAsYAMLScalars(t *testing.T) {
	cases := []struct {
		name       string
		manifest   []byte
		registered []string
	}{
		{
			name:       "double-quoted sequence item",
			manifest:   manifestWithHooksBlock("      - \"beforeEnsure\"\n"),
			registered: []string{handler.HookBeforeEnsure},
		},
		{
			name:       "single-quoted sequence item",
			manifest:   manifestWithHooksBlock("      - 'beforeEnsure'\n"),
			registered: []string{handler.HookBeforeEnsure},
		},
		{
			name:       "sequence item with an inline comment",
			manifest:   manifestWithHooksBlock("      - beforeEnsure # runs before Ensure\n"),
			registered: []string{handler.HookBeforeEnsure},
		},
		{
			name:       "quoted sequence item with an inline comment",
			manifest:   manifestWithHooksBlock("      - \"beforeEnsure\" # pre\n      - afterDelete\n"),
			registered: []string{handler.HookBeforeEnsure, handler.HookAfterDelete},
		},
		{
			name:       "flow list with a double-quoted and a plain item",
			manifest:   manifestWithLifecycleHooksLine(`hooks: ["beforeEnsure", afterDelete]`),
			registered: []string{handler.HookBeforeEnsure, handler.HookAfterDelete},
		},
		{
			name:       "flow list with a single-quoted item",
			manifest:   manifestWithLifecycleHooksLine(`hooks: ['beforeEnsure']`),
			registered: []string{handler.HookBeforeEnsure},
		},
		{
			name:       "flow list followed by a trailing comment",
			manifest:   manifestWithLifecycleHooksLine("hooks: [beforeEnsure] # c"),
			registered: []string{handler.HookBeforeEnsure},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			cfg := baseConfig(t, &kittest.FakeResource{}, s)
			cfg.Manifest = tc.manifest
			cfg.Hooks = map[string]handler.Hook{}
			for _, h := range tc.registered {
				cfg.Hooks[h] = noopHook
			}

			if _, err := handler.New(cfg); err != nil {
				t.Fatalf("handler.New refused %v against a manifest that declares them as YAML reads it: %v\nmanifest:\n%s",
					tc.registered, err, tc.manifest)
			}
		})
	}
}
