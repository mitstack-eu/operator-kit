// New refuses a configuration that would let a block override an invariant, and agrees with the platform's manifest parser on what a manifest declares.
package handler_test

import (
	"crypto/ed25519"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/handler"
	"github.com/mitstack-eu/operator-kit/v3/kittest"
)

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
	unchanged := func(map[string]handler.Observer) {}
	plainObserved := func(c *handler.Config) {
		c.Manifest = plainV1Beta(t)
		c.Observers = map[string]handler.Observer{"aws.ec2-container": &observer{name: "aws.ec2-container", cloud: newFakeCloud()}}
	}
	type newCase struct {
		name    string
		config  func(*handler.Config)
		refused bool
		naming  []string
		parser  string
	}
	cases := []newCase{
		{name: "a nil Resource is refused", config: func(c *handler.Config) { c.Resource = nil }, refused: true},
		{name: "an empty JWKSURL is refused", config: func(c *handler.Config) { c.JWKSURL = "" }, refused: true, naming: []string{"JWKSURL"}},
		{name: "an empty JWKSURL is refused even with a PublicKey override", config: func(c *handler.Config) {
			c.JWKSURL = ""
			c.PublicKey = func(string) (ed25519.PublicKey, error) { return nil, nil }
		}, refused: true, naming: []string{"JWKSURL"}},
		{name: "a v1beta manifest with an observer for its resource is accepted", config: plainObserved},
		{name: "a v1beta manifest declaring hooks is refused", config: withManifest(withManifestEdit(t, plainV1Beta(t), "    contract: kit/v1", "    contract: kit/v1\n    hooks: [beforeEnsure]")),
			refused: true, parser: "spec.lifecycle.hooks"},
		{name: "a v1beta manifest declaring retainsOnUnsubscribe is refused", config: withManifest(withManifestEdit(t, plainV1Beta(t), "    contract: kit/v1", "    contract: kit/v1\n    retainsOnUnsubscribe: true")),
			refused: true, parser: "spec.lifecycle.retainsOnUnsubscribe"},
		{name: "a manifest in the old version is refused with the parser's message", config: then(plainObserved, withManifest(oldVersionManifest)),
			refused: true, parser: "apiVersion"},
		{name: "an observer for every declared resource is accepted", config: withBlockLevelObservers(unchanged)},
		{name: "a declared resource with no observer is refused naming it", config: withBlockLevelObservers(func(o map[string]handler.Observer) { delete(o, "b") }),
			refused: true, naming: []string{`"b"`, "observe"}},
		{name: "an observer for an undeclared resource is refused naming it", config: withBlockLevelObservers(func(o map[string]handler.Observer) { o["c"] = &observer{} }),
			refused: true, naming: []string{`"c"`}},
		{name: "observers with no manifest are refused", config: then(withBlockLevelObservers(unchanged), withManifest("")), refused: true},
		{name: "a type observer for a parsable type on a block-level block is accepted", config: withTypeObservers(func(map[string]handler.Observer) {})},
		{name: "a type observer under a key that is not a resource type id is refused", config: withTypeObservers(func(o map[string]handler.Observer) { o["Not A Type"] = &observer{} }),
			refused: true, naming: []string{`"Not A Type"`, "Config.TypeObservers"}},
		{name: "a nil type observer is refused", config: withTypeObservers(func(o map[string]handler.Observer) { o["aws.vpc"] = nil }),
			refused: true, naming: []string{`"aws.vpc"`, "Config.TypeObservers"}},
		{name: "type observers with no manifest are refused", config: then(withTypeObservers(unchanged), func(c *handler.Config) { c.Observers, c.Manifest = nil, nil }),
			refused: true, naming: []string{"Config.TypeObservers", "Config.Manifest"}},
	}
	for _, edit := range []struct{ path, old, new string }{
		{"bogus", "metadata:\n", "bogus: 1\nmetadata:\n"},
		{"metadata.bogus", "  name: Plain Tracker\n", "  name: Plain Tracker\n  bogus: 1\n"},
		{"spec.bogus", "  version: 3.0.0\n", "  version: 3.0.0\n  bogus: 1\n"},
		{"spec.lifecycle.bogus", "  lifecycle:\n", "  lifecycle:\n    bogus: 1\n"},
		{"apiVersion", "apiVersion: mitstack/v1beta\n", ""},
	} {
		cases = append(cases, newCase{name: "a manifest refused at " + edit.path + " is refused with the parser's message",
			config: then(plainObserved, withManifest(withManifestEdit(t, plainV1Beta(t), edit.old, edit.new))), refused: true, parser: edit.path})
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

func TestConfig_CarriesNoOBOEscapeHatch(t *testing.T) {
	typ := reflect.TypeOf(handler.Config{})

	for i := 0; i < typ.NumField(); i++ {
		if name := typ.Field(i).Name; strings.Contains(strings.ToLower(name), "obo") {
			t.Errorf("handler.Config carries field %q, want no OBO-related escape hatch on Config", name)
		}
	}
}
