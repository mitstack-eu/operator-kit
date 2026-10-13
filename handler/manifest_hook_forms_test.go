package handler_test

import (
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// manifestWithLifecycle builds a complete v1alpha manifest whose
// spec.lifecycle holds lifecycleLines verbatim.
func manifestWithLifecycle(lifecycleLines string) []byte {
	return []byte("apiVersion: mitstack/v1alpha\n" +
		"kind: Block\n" +
		"metadata:\n  id: test-block\n  name: Test Block\n" +
		"spec:\n  layer: infra\n  lifecycle:\n" + lifecycleLines)
}

// newWithHooksRegistered runs handler.New on manifest with each of names
// registered.
func newWithHooksRegistered(t *testing.T, manifest []byte, names ...string) error {
	t.Helper()
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = manifest
	cfg.Hooks = map[string]handler.Hook{}
	for _, name := range names {
		cfg.Hooks[name] = noopHook
	}
	_, err := handler.New(cfg)
	return err
}

// TestManifestHooks_ReadsQuotedAndCommentedFormsAsYAMLDoes pins that New
// takes each quoted or commented form as declaring the plain names YAML
// reads, with no quote characters and no comment text: registering exactly
// those names is accepted.
func TestManifestHooks_ReadsQuotedAndCommentedFormsAsYAMLDoes(t *testing.T) {
	cases := []struct {
		name  string
		hooks string
		want  []string
	}{
		{"double-quoted sequence items", "    hooks:\n      - \"beforeEnsure\"\n      - \"afterDelete\"\n", []string{"beforeEnsure", "afterDelete"}},
		{"single-quoted sequence items", "    hooks:\n      - 'beforeEnsure'\n      - 'afterDelete'\n", []string{"beforeEnsure", "afterDelete"}},
		{"sequence item with an inline comment", "    hooks:\n      - beforeEnsure # pre\n      - afterDelete\n", []string{"beforeEnsure", "afterDelete"}},
		{"quoted sequence item with an inline comment", "    hooks:\n      - 'beforeEnsure'   # pre\n", []string{"beforeEnsure"}},
		{"comment after the hooks key", "    hooks: # the hook points\n      - beforeEnsure\n", []string{"beforeEnsure"}},
		{"flow list with quoted items", "    hooks: [\"beforeEnsure\", 'afterDelete']\n", []string{"beforeEnsure", "afterDelete"}},
		{"flow list with a quoted and a plain item", "    hooks: [\"beforeEnsure\", afterDelete]\n", []string{"beforeEnsure", "afterDelete"}},
		{"flow list followed by a trailing comment", "    hooks: [beforeEnsure] # c\n", []string{"beforeEnsure"}},
		{"sequence at the key's own indent", "    hooks:\n    - beforeEnsure\n    - afterDelete\n    contract: kit/v1\n", []string{"beforeEnsure", "afterDelete"}},
		{"sequence at the key's own indent with a comment between items", "    hooks:\n    - beforeEnsure\n    # the delete side\n    - afterDelete\n", []string{"beforeEnsure", "afterDelete"}},
		{"comment at column 0 before the hooks key", "# note\n    hooks:\n      - beforeEnsure\n", []string{"beforeEnsure"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := manifestWithLifecycle(tc.hooks)
			if err := newWithHooksRegistered(t, manifest, tc.want...); err != nil {
				t.Fatalf("handler.New refused %v against a manifest that declares them as YAML reads it: %v\nmanifest:\n%s",
					tc.want, err, manifest)
			}
		})
	}

	t.Run("empty flow list followed by a trailing comment", func(t *testing.T) {
		err := newWithHooksRegistered(t, manifestWithLifecycle("    hooks: [] # none yet\n"), handler.HookBeforeEnsure)
		if err == nil || !strings.Contains(err.Error(), handler.HookBeforeEnsure) {
			t.Fatalf("handler.New error = %v, want a refusal naming %q, which \"hooks: []\" does not declare", err, handler.HookBeforeEnsure)
		}
	})
}

// TestManifestHooks_ACommentOnTheSpecLineDoesNotHideTheHooks pins that a
// trailing comment on the top-level spec: line still opens spec's block.
func TestManifestHooks_ACommentOnTheSpecLineDoesNotHideTheHooks(t *testing.T) {
	manifest := []byte(strings.Replace(string(manifestWithLifecycle("    hooks:\n      - beforeEnsure\n")),
		"spec:\n", "spec: # note\n", 1))
	if err := newWithHooksRegistered(t, manifest, handler.HookBeforeEnsure); err != nil {
		t.Fatalf("handler.New refused beforeEnsure, which the manifest declares under a commented spec: line: %v\n%s", err, manifest)
	}
}

// TestManifestHooks_HashInsideQuotesIsNotAComment pins that a "#" inside a
// quoted scalar is part of the value, as in YAML, so the name read keeps it
// and New's closed-set check, not a comment stripper, is what refuses it.
func TestManifestHooks_HashInsideQuotesIsNotAComment(t *testing.T) {
	err := newWithHooksRegistered(t, manifestWithLifecycle("    hooks:\n      - \"before#Ensure\"\n"))
	if err == nil {
		t.Fatal("handler.New accepted a manifest declaring the hook \"before#Ensure\", which is no hook point")
	}
	for _, want := range []string{contract.LifecycleHooksField, "before#Ensure"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err.Error(), want)
		}
	}
}

// TestManifestHooks_AColumnZeroKeyAfterTheHooksIsRefusedAsUnknown pins that
// a line at column zero ends spec, and a key there that no manifest field
// names is refused with the parser's message rather than read as anything.
func TestManifestHooks_AColumnZeroKeyAfterTheHooksIsRefusedAsUnknown(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = manifestWithLifecycle("    hooks:\n      - beforeEnsure\ndecoy: true\n      - afterDelete\n")
	requireNewRefusesWithTheParsersMessage(t, cfg, "decoy")
}
