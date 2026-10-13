package main

// manifest_strict_test.go pins that every blockverify suite reading the
// target's manifest reads it through contract.ParseManifest: the lifecycle
// contract that picks the trust-plumbing and contract rules, and the
// spec.naming.appName declaration the naming suite checks. A manifest the
// parser refuses fails the suite that reads it, with the parser's message
// in the detail, and is never read as declaring nothing.

import (
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// parserRefusal returns the error contract.ParseManifest gives content,
// failing the test when it accepts it.
func parserRefusal(t *testing.T, content, path string) string {
	t.Helper()
	_, err := contract.ParseManifest([]byte(content))
	if err == nil {
		t.Fatalf("contract.ParseManifest accepts the manifest, which must be refused naming %s:\n%s", path, content)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("contract.ParseManifest error = %q, want it to name %s", err, path)
	}
	return err.Error()
}

func TestContractRule_FailsAManifestTheParserRefusesWithItsMessage(t *testing.T) {
	cases := map[string]string{
		"spec.frobnicate":           manifestYAML + "  frobnicate: true\n",
		"spec.lifecycle.frobnicate": manifestYAML + "  lifecycle:\n    contract: kit/v1\n    frobnicate: true\n",
		"metadata.frobnicate": strings.Replace(manifestYAML, "  owner: blockverify-tests\n",
			"  owner: blockverify-tests\n  frobnicate: true\n", 1),
		contract.LifecycleHooksField: strings.Replace(plainV1Beta(t), "    contract: kit/v1",
			"    contract: kit/v1\n    hooks: [beforeEnsure]", 1),
	}
	for path, content := range cases {
		t.Run(path, func(t *testing.T) {
			msg := parserRefusal(t, content, path)
			dir := manifestDir(t, content)
			c, detail, ok := contractRule(dir)
			if ok {
				t.Fatalf("contractRule = %q, ok, for a manifest the parser refuses; want a failure carrying %q", c, msg)
			}
			for _, want := range []string{"manifest.yaml", msg} {
				if !strings.Contains(detail, want) {
					t.Errorf("contractRule detail = %q, want it to contain %q", detail, want)
				}
			}
		})
	}
}

// The lifecycle contract is read from the parsed manifest under either
// version and in either YAML form.
func TestContractRule_ReadsTheDeclaredContractThroughTheParser(t *testing.T) {
	cases := map[string]string{
		"a v1alpha block mapping": manifestYAML + "  lifecycle:\n    contract: kit/v1\n",
		"a v1alpha flow mapping":  manifestYAML + "  lifecycle: {contract: kit/v1}\n",
		"the worked v1beta block": plainV1Beta(t),
	}
	for label, content := range cases {
		t.Run(label, func(t *testing.T) {
			c, detail, ok := contractRule(manifestDir(t, content))
			if !ok || c != kitV1Contract {
				t.Errorf("contractRule = %q, %v (detail %q), want %q, true", c, ok, detail, kitV1Contract)
			}
		})
	}
}

func TestNamingSuite_FailsAManifestTheParserRefusesWithItsMessage(t *testing.T) {
	cases := map[string]string{
		"an unknown spec field beside the declaration": manifestHead + "  frobnicate: true\n" + awsAppInfraNamingSpec,
		"an unknown spec field and no declaration":     manifestHead + "  frobnicate: true\n",
	}
	for label, content := range cases {
		t.Run(label, func(t *testing.T) {
			msg := parserRefusal(t, content, "spec.frobnicate")
			requireNamingFail(t, namingSuite(manifestDir(t, content)), "manifest.yaml", msg)
		})
	}
	t.Run("a key under spec.naming other than appName", func(t *testing.T) {
		content := manifestHead + "  naming:\n    frobnicate:\n      maxLength: 10\n"
		msg := parserRefusal(t, content, "spec.naming.frobnicate")
		requireNamingFail(t, namingSuite(manifestDir(t, content)), "manifest.yaml", msg)
	})
}
