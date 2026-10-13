package handler_test

import (
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

func requireNewRefusesWithTheParsersMessage(t *testing.T, cfg handler.Config, path string) {
	t.Helper()
	_, perr := contract.ParseManifest(cfg.Manifest)
	if perr == nil {
		t.Fatalf("contract.ParseManifest accepts the manifest, which must be refused naming %s:\n%s", path, cfg.Manifest)
	}
	if !strings.Contains(perr.Error(), path) {
		t.Errorf("contract.ParseManifest error = %q, want it to name %s", perr, path)
	}
	_, err := handler.New(cfg)
	if err == nil {
		t.Fatalf("handler.New accepted a manifest contract.ParseManifest refuses (%v):\n%s", perr, cfg.Manifest)
	}
	if !strings.Contains(err.Error(), perr.Error()) {
		t.Errorf("handler.New error = %q, want it to carry the parser's message %q", err, perr)
	}
}

func withManifestEdit(t *testing.T, raw []byte, old, new string) []byte {
	t.Helper()
	if !strings.Contains(string(raw), old) {
		t.Fatalf("fixture edit: %q is not in the manifest:\n%s", old, raw)
	}
	return []byte(strings.Replace(string(raw), old, new, 1))
}

func TestNew_RefusesAManifestTheParserRefusesWithTheParsersMessage(t *testing.T) {
	declaring := manifestDeclaring(handler.HookBeforeEnsure)
	cases := []struct {
		path     string
		manifest []byte
	}{
		{"bogus", withManifestEdit(t, declaring, "spec:\n", "bogus: 1\nspec:\n")},
		{"metadata.bogus", withManifestEdit(t, declaring, "  name: Test Block\n", "  name: Test Block\n  bogus: 1\n")},
		{"spec.bogus", withManifestEdit(t, declaring, "  layer: infra\n", "  layer: infra\n  bogus: 1\n")},
		{"spec.lifecycle.bogus", withManifestEdit(t, declaring, "  lifecycle:\n", "  lifecycle:\n    bogus: 1\n")},
		{"apiVersion", withManifestEdit(t, declaring, "apiVersion: mitstack/v1alpha\n", "")},
	}
	for _, tc := range cases {
		for label, hooks := range map[string]map[string]handler.Hook{
			"no hook registered":           nil,
			"the declared hook registered": {handler.HookBeforeEnsure: noopHook},
		} {
			t.Run(tc.path+", "+label, func(t *testing.T) {
				s := kittest.NewSigner(t, "kid-1", 7)
				cfg := baseConfig(t, &kittest.FakeResource{}, s)
				cfg.Manifest = tc.manifest
				cfg.Hooks = hooks
				requireNewRefusesWithTheParsersMessage(t, cfg, tc.path)
			})
		}
	}
}

func plainV1Beta(t *testing.T) []byte {
	t.Helper()
	raw, err := contract.ManifestGoldens.ReadFile(contract.ManifestGoldensDir + "/plain-v1beta.yaml")
	if err != nil {
		t.Fatalf("reading plain-v1beta.yaml from contract.ManifestGoldens: %v", err)
	}
	return raw
}

// The worked plain manifest declares one resource, aws.ec2-container, which
// a block served over Config.Resource observes through Config.Observers.
func TestNew_AcceptsAV1BetaManifestAndReadsItAsDeclaringNoHook(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cfg := baseConfig(t, &kittest.FakeResource{}, s)
	cfg.Manifest = plainV1Beta(t)
	cfg.Observers = map[string]handler.Observer{"aws.ec2-container": &fakeObserver{name: "aws.ec2-container", cloud: newFakeCloud()}}
	if _, err := handler.New(cfg); err != nil {
		t.Fatalf("handler.New refused the worked v1beta manifest with no hook registered: %v", err)
	}

	cfg.Hooks = map[string]handler.Hook{handler.HookBeforeEnsure: noopHook}
	_, err := handler.New(cfg)
	if err == nil {
		t.Fatal("handler.New accepted a hook registered against a v1beta manifest, which declares none")
	}
	if !strings.Contains(err.Error(), handler.HookBeforeEnsure) {
		t.Errorf("error %q does not name the undeclared hook %q", err.Error(), handler.HookBeforeEnsure)
	}
}

func TestNew_RefusesAV1BetaManifestDeclaringHooksOrRetainsOnUnsubscribe(t *testing.T) {
	cases := map[string]string{
		contract.LifecycleHooksField:          "    contract: kit/v1\n    hooks: [beforeEnsure]",
		"spec.lifecycle.retainsOnUnsubscribe": "    contract: kit/v1\n    retainsOnUnsubscribe: true",
	}
	for path, lines := range cases {
		t.Run(path, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			cfg := baseConfig(t, &kittest.FakeResource{}, s)
			cfg.Manifest = withManifestEdit(t, plainV1Beta(t), "    contract: kit/v1", lines)
			requireNewRefusesWithTheParsersMessage(t, cfg, path)
		})
	}
}
