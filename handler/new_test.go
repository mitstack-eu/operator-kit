// New refuses a configuration that would let a block override an invariant, and agrees with the platform's manifest parser on what a manifest declares.
package handler_test

import (
	"crypto/ed25519"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// aboveSpec puts lines just above spec, where a decoy key must not be read as a lifecycle declaration.
func aboveSpec(manifest, lines string) string {
	return strings.Replace(manifest, "spec:\n", lines+"\nspec:\n", 1)
}

func withTypeObservers(edit func(map[string]handler.Observer)) func(*handler.Config) {
	return func(c *handler.Config) {
		cloud := newFakeCloud()
		c.Manifest = []byte(onlyAManifest)
		c.Observers = map[string]handler.Observer{"a": &observer{name: "a", cloud: cloud}}
		c.TypeObservers = map[string]handler.Observer{"aws.vpc": &observer{}}
		edit(c.TypeObservers)
	}
}

func withBlockLevelObservers(edit func(map[string]handler.Observer)) func(*handler.Config) {
	return func(c *handler.Config) {
		cloud := newFakeCloud()
		c.Manifest = []byte(blockLevelManifest)
		c.Observers = asObservers(blockObservers(cloud))
		edit(c.Observers)
	}
}

func then(steps ...func(*handler.Config)) func(*handler.Config) {
	return func(c *handler.Config) {
		for _, step := range steps {
			step(c)
		}
	}
}

func TestNew(t *testing.T) {
	bE, aE, bD, aD := handler.HookBeforeEnsure, handler.HookAfterEnsure, handler.HookBeforeDelete, handler.HookAfterDelete
	hooksItems := func(items string) string { return manifestWithLifecycle("    hooks:\n" + items) }
	commentBetween := hooksItems("      - beforeEnsure\n      # a comment between two items\n      - afterDelete\n")
	unchanged := func(map[string]handler.Observer) {}
	type newCase struct {
		name    string
		config  func(*handler.Config)
		refused bool
		naming  []string
		absent  string
		parser  string
	}
	cases := []newCase{
		{name: "a nil Resource is refused", config: func(c *handler.Config) { c.Resource = nil }, refused: true},
		{name: "an empty JWKSURL is refused", config: func(c *handler.Config) { c.JWKSURL = "" }, refused: true, naming: []string{"JWKSURL"}},
		{name: "an empty JWKSURL is refused even with a PublicKey override", config: func(c *handler.Config) {
			c.JWKSURL = ""
			c.PublicKey = func(string) (ed25519.PublicKey, error) { return nil, nil }
		}, refused: true, naming: []string{"JWKSURL"}},
		{name: "an unknown hook point is refused naming it and the known ones", config: declaring(manifestDeclaring(bE), "onFrobnicate"),
			refused: true, naming: []string{"onFrobnicate", bE, aE, bD, aD}},
		{name: "a hook the manifest does not declare is refused naming what it declares", config: declaring(manifestDeclaring(aD), bE),
			refused: true, naming: []string{bE, aD}},
		{name: "a hook with an empty manifest is refused", config: declaring("", bE), refused: true},
		{name: "hooks: [] declares no hook", config: declaring(manifestDeclaring(), bE), refused: true},
		{name: "a manifest with no hooks key declares no hook", config: declaring(v1alphaHead, aD), refused: true},
		{name: "a commented-out hooks key declares no hook", config: declaring(manifestWithLifecycle("    # hooks:\n    #   - beforeEnsure\n"), bE), refused: true},
		{name: "an empty flow list followed by a trailing comment declares no hook", config: declaring(manifestWithLifecycle("    hooks: [] # none yet\n"), bE),
			refused: true, naming: []string{bE}},
		{name: "a comment between hook items declares both", config: declaring(commentBetween, bE, aD)},
		{name: "a hook neither item declares is refused naming both declared hooks", config: declaring(commentBetween, bD),
			refused: true, naming: []string{bE, aD}},
		{name: "CRLF line endings are read", config: declaring(strings.ReplaceAll(manifestDeclaring(bE, aD), "\n", "\r\n"), bE, aD)},
		{name: "a tab-indented hooks sequence is refused by the reader, not as an undeclared hook", config: withManifest(hooksItems("\t- beforeEnsure\n\t- afterDelete\n")),
			refused: true, naming: []string{"manifest"}, absent: "does not declare it"},
		{name: "an unreadable hooks value is refused as unreadable, not as declaring nothing", config: withManifest(manifestWithLifecycle("    hooks: beforeEnsure\n")),
			refused: true, naming: []string{"manifest"}, parser: contract.LifecycleHooksField},
		{name: "a decoy hooks key above spec.lifecycle is refused as unknown", config: declaring(aboveSpec(manifestDeclaring(bE), "hooks:\n  - decoyHook"), bE), refused: true, parser: "hooks"},
		{name: "a hooks key one level too deep is refused as unknown", config: declaring(manifestWithLifecycle("    extra:\n      hooks:\n        - afterEnsure\n    hooks:\n      - beforeEnsure\n"), aE),
			refused: true, parser: "spec.lifecycle.extra"},
		{name: "a sibling spec.other hooks stanza is refused as unknown", config: declaring(manifestWithLifecycle("    hooks:\n      - beforeEnsure\n  other:\n    hooks:\n      - afterDelete\n"), bE),
			refused: true, parser: "spec.other"},
		{name: "a column-zero key after the hooks is refused as unknown", config: withManifest(hooksItems("      - beforeEnsure\ndecoy: true\n      - afterDelete\n")),
			refused: true, parser: "decoy"},
		{name: "a declared unknown hook is refused with no hook registered", config: withManifest(manifestDeclaring(bE, "bogusHook")),
			refused: true, naming: []string{contract.LifecycleHooksField, "bogusHook"}},
		{name: "a declared unknown hook is refused with a known hook registered", config: declaring(manifestDeclaring(bE, "bogusHook"), bE),
			refused: true, naming: []string{contract.LifecycleHooksField, "bogusHook"}},
		{name: "a declared unknown hook in a flow list is refused", config: withManifest(manifestWithLifecycle("    hooks: [beforeEnsure, bogusHook]\n")),
			refused: true, naming: []string{contract.LifecycleHooksField, "bogusHook"}},
		{name: "a hash inside quotes is not a comment, so the name is no hook point", config: withManifest(hooksItems("      - \"before#Ensure\"\n")),
			refused: true, naming: []string{contract.LifecycleHooksField, "before#Ensure"}},
		{name: "a hook declared twice is refused with no hook registered", config: withManifest(manifestDeclaring(bE, aD, bE)),
			refused: true, naming: []string{contract.LifecycleHooksField, bE}},
		{name: "a hook declared twice is refused with that hook registered", config: declaring(manifestDeclaring(bE, bE), bE),
			refused: true, naming: []string{contract.LifecycleHooksField, bE}},
		{name: "a hook declared plain and quoted is declared twice", config: withManifest(hooksItems("      - beforeEnsure\n      - \"beforeEnsure\"\n")),
			refused: true, naming: []string{contract.LifecycleHooksField, bE}},
		{name: "double-quoted sequence items are read as YAML reads them", config: declaring(hooksItems("      - \"beforeEnsure\"\n      - \"afterDelete\"\n"), bE, aD)},
		{name: "single-quoted sequence items are read as YAML reads them", config: declaring(hooksItems("      - 'beforeEnsure'\n      - 'afterDelete'\n"), bE, aD)},
		{name: "a sequence item with an inline comment is read", config: declaring(hooksItems("      - beforeEnsure # pre\n      - afterDelete\n"), bE, aD)},
		{name: "a double-quoted item with an inline comment is read", config: declaring(hooksItems("      - \"beforeEnsure\" # pre\n      - afterDelete\n"), bE, aD)},
		{name: "a single-quoted item with spaces before its comment is read", config: declaring(hooksItems("      - 'beforeEnsure'   # pre\n"), bE)},
		{name: "a comment after the hooks key is read past", config: declaring(manifestWithLifecycle("    hooks: # the hook points\n      - beforeEnsure\n"), bE)},
		{name: "a flow list with quoted items is read", config: declaring(manifestWithLifecycle("    hooks: [\"beforeEnsure\", 'afterDelete']\n"), bE, aD)},
		{name: "a flow list with a quoted and a plain item is read", config: declaring(manifestWithLifecycle("    hooks: [\"beforeEnsure\", afterDelete]\n"), bE, aD)},
		{name: "a flow list followed by a trailing comment is read", config: declaring(manifestWithLifecycle("    hooks: [beforeEnsure] # c\n"), bE)},
		{name: "a sequence at the key's own indent is read", config: declaring(manifestWithLifecycle("    hooks:\n    - beforeEnsure\n    - afterDelete\n    contract: kit/v1\n"), bE, aD)},
		{name: "a sequence at the key's own indent with a comment between items is read", config: declaring(manifestWithLifecycle("    hooks:\n    - beforeEnsure\n    # the delete side\n    - afterDelete\n"), bE, aD)},
		{name: "a comment at column 0 before the hooks key is read past", config: declaring(manifestWithLifecycle("# note\n    hooks:\n      - beforeEnsure\n"), bE)},
		{name: "a comment on the spec line does not hide the hooks", config: declaring(strings.Replace(manifestDeclaring(bE), "spec:\n", "spec: # note\n", 1), bE)},
		{name: "the shared hooks golden is accepted with every declared hook registered", config: declaring(contract.HooksManifestGolden(), contract.HooksManifestGoldenDeclares()...)},
		{name: "a hook the shared hooks golden does not declare is refused", config: declaring(contract.HooksManifestGolden(), aE), refused: true, naming: []string{aE}},
		{name: "the type: block twin is accepted with every declared hook registered", config: declaring(contract.TypeBlockHooksManifestGolden(), contract.HooksManifestGoldenDeclares()...)},
		{name: "a hook the type: block twin does not declare is refused", config: declaring(contract.TypeBlockHooksManifestGolden(), aE), refused: true, naming: []string{aE}},
		{name: "a retainsOnUnsubscribe that is not a boolean is refused", config: withManifest(manifestWithRetains("maybe")),
			refused: true, naming: []string{"retainsOnUnsubscribe"}, parser: "spec.lifecycle.retainsOnUnsubscribe"},
		{name: "a decoy top-level retainsOnUnsubscribe is refused as unknown", config: withManifest(aboveSpec(manifestDeclaring(), "retainsOnUnsubscribe: true")), refused: true, parser: "retainsOnUnsubscribe"},
		{name: "a v1beta manifest with an observer for its resource is accepted", config: func(c *handler.Config) {
			c.Manifest = plainV1Beta(t)
			c.Observers = map[string]handler.Observer{"aws.ec2-container": &observer{name: "aws.ec2-container", cloud: newFakeCloud()}}
		}},
		{name: "a hook registered against a v1beta manifest, which declares none, is refused", config: func(c *handler.Config) {
			c.Manifest = plainV1Beta(t)
			c.Observers = map[string]handler.Observer{"aws.ec2-container": &observer{name: "aws.ec2-container", cloud: newFakeCloud()}}
			c.Hooks = map[string]handler.Hook{bE: noopHook}
		}, refused: true, naming: []string{bE}},
		{name: "a v1beta manifest declaring hooks is refused", config: withManifest(withManifestEdit(t, plainV1Beta(t), "    contract: kit/v1", "    contract: kit/v1\n    hooks: [beforeEnsure]")),
			refused: true, parser: contract.LifecycleHooksField},
		{name: "a v1beta manifest declaring retainsOnUnsubscribe is refused", config: withManifest(withManifestEdit(t, plainV1Beta(t), "    contract: kit/v1", "    contract: kit/v1\n    retainsOnUnsubscribe: true")),
			refused: true, parser: "spec.lifecycle.retainsOnUnsubscribe"},
		{name: "an observer for every declared resource is accepted", config: withBlockLevelObservers(unchanged)},
		{name: "a declared resource with no observer is refused naming it", config: withBlockLevelObservers(func(o map[string]handler.Observer) { delete(o, "b") }),
			refused: true, naming: []string{`"b"`, "observe"}},
		{name: "an observer for an undeclared resource is refused naming it", config: withBlockLevelObservers(func(o map[string]handler.Observer) { o["c"] = &observer{} }),
			refused: true, naming: []string{`"c"`}},
		{name: "observers with no manifest are refused", config: then(withBlockLevelObservers(unchanged), withManifest("")), refused: true},
		{name: "observers under a v1alpha manifest are refused", config: then(withBlockLevelObservers(unchanged), withManifest(v1alphaHead)), refused: true},
		{name: "a type observer for a parsable type on a block-level block is accepted", config: withTypeObservers(func(map[string]handler.Observer) {})},
		{name: "a type observer under a key that is not a resource type id is refused", config: withTypeObservers(func(o map[string]handler.Observer) { o["Not A Type"] = &observer{} }),
			refused: true, naming: []string{`"Not A Type"`, "Config.TypeObservers"}},
		{name: "a nil type observer is refused", config: withTypeObservers(func(o map[string]handler.Observer) { o["aws.vpc"] = nil }),
			refused: true, naming: []string{`"aws.vpc"`, "Config.TypeObservers"}},
		{name: "type observers with no manifest are refused", config: then(withTypeObservers(unchanged), func(c *handler.Config) { c.Observers, c.Manifest = nil, nil }),
			refused: true, naming: []string{"Config.TypeObservers", "Config.Manifest"}},
		{name: "type observers under a v1alpha manifest are refused", config: then(withTypeObservers(unchanged), func(c *handler.Config) { c.Observers, c.Manifest = nil, []byte(v1alphaHead) }),
			refused: true, naming: []string{"Config.TypeObservers", "Config.Manifest"}},
	}
	declaringBE := manifestDeclaring(bE)
	for _, edit := range []struct{ path, old, new string }{
		{"bogus", "spec:\n", "bogus: 1\nspec:\n"},
		{"metadata.bogus", "  name: Test Block\n", "  name: Test Block\n  bogus: 1\n"},
		{"spec.bogus", "  layer: infra\n", "  layer: infra\n  bogus: 1\n"},
		{"spec.lifecycle.bogus", "  lifecycle:\n", "  lifecycle:\n    bogus: 1\n"},
		{"apiVersion", "apiVersion: mitstack/v1alpha\n", ""},
	} {
		manifest := withManifestEdit(t, []byte(declaringBE), edit.old, edit.new)
		cases = append(cases,
			newCase{name: "a manifest refused at " + edit.path + " is refused with no hook registered", config: withManifest(manifest), refused: true, parser: edit.path},
			newCase{name: "a manifest refused at " + edit.path + " is refused with the declared hook registered", config: declaring(manifest, bE), refused: true, parser: edit.path})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			f := &kittest.FakeResource{}
			cfg := baseConfig(t, f, s)
			tc.config(&cfg)

			_, err := handler.New(cfg)

			if (err != nil) != tc.refused {
				t.Fatalf("handler.New error = %v, want refused %v", err, tc.refused)
			}
			for _, want := range tc.naming {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
			if tc.absent != "" && strings.Contains(err.Error(), tc.absent) {
				t.Errorf("error %q carries %q, want the reader's own refusal", err, tc.absent)
			}
			if tc.parser != "" {
				wantParserRefusal(t, cfg.Manifest, tc.parser, err)
			}
			assertCalls(t, f, [3]int{})
		})
	}
}

// wantParserRefusal fails unless contract.ParseManifest refuses manifest naming path and err carries the parser's message.
func wantParserRefusal(t *testing.T, manifest []byte, path string, err error) {
	t.Helper()
	_, perr := contract.ParseManifest(manifest)
	if perr == nil {
		t.Fatalf("contract.ParseManifest accepts the manifest, which must be refused naming %s:\n%s", path, manifest)
	}
	if !strings.Contains(perr.Error(), path) {
		t.Errorf("contract.ParseManifest error = %q, want it to name %s", perr, path)
	}
	if err == nil || !strings.Contains(err.Error(), perr.Error()) {
		t.Errorf("error = %v, want it to carry the parser's message %q", err, perr)
	}
}

func TestHookConstants_AreTheContractHookPoints(t *testing.T) {
	got := []string{handler.HookBeforeEnsure, handler.HookAfterEnsure, handler.HookBeforeDelete, handler.HookAfterDelete}

	if want := contract.LifecycleHookPoints(); !reflect.DeepEqual(got, want) {
		t.Fatalf("handler hook constants = %v, contract.LifecycleHookPoints() = %v", got, want)
	}
}

func TestConfig_CarriesNoOBOEscapeHatch(t *testing.T) {
	typ := reflect.TypeOf(handler.Config{})

	for i := 0; i < typ.NumField(); i++ {
		if name := typ.Field(i).Name; strings.Contains(strings.ToLower(name), "obo") {
			t.Errorf("handler.Config carries field %q, want no OBO-related escape hatch on Config", name)
		}
	}
}
