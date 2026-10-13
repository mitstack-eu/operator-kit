package contract_test

// manifest_unknown_field_test.go pins that contract.ParseManifest is strict
// under both manifest versions: a key no field of the version names is
// refused, at the top level and at every depth below it, and the error
// names the key's path.

import (
	"strings"
	"testing"
)

// alphaWith returns alphaHead with each old, new pair of replacements
// applied once, failing the test when an old text is not in it.
func alphaWith(t *testing.T, pairs ...string) []byte {
	t.Helper()
	out := alphaHead
	for i := 0; i+1 < len(pairs); i += 2 {
		if !strings.Contains(out, pairs[i]) {
			t.Fatalf("fixture edit: %q is not in the manifest", pairs[i])
		}
		out = strings.Replace(out, pairs[i], pairs[i+1], 1)
	}
	return []byte(out)
}

// v1alphaUnknownFields are manifests that are valid v1alpha but for one key
// no v1alpha field names, by the path the refusal must name.
func v1alphaUnknownFields(t *testing.T) map[string][]byte {
	spec := func(lines string) []byte { return []byte(alphaHead + lines) }
	return map[string][]byte{
		"bogus":              alphaWith(t, "spec:\n", "bogus: 1\nspec:\n"),
		"metadata.bogus":     alphaWith(t, "  owner: fixture-org\n", "  owner: fixture-org\n  bogus: 1\n"),
		"spec.bogus":         spec("  bogus: 1\n"),
		"spec.futureSection": spec("  futureSection:\n    outputs: not-the-outputs\n"),
		"spec.requires[1].bogus": spec("  requires:\n    - capability: database\n" +
			"    - capability: github\n      bogus: 1\n"),
		"spec.requires[0].tokenSchema[0].bogus": spec("  requires:\n    - capability: github\n" +
			"      tokenSchema:\n        - name: api_token\n          bogus: 1\n"),
		"spec.requires[0].verify.bogus": spec("  requires:\n    - capability: github\n" +
			"      verify:\n        urlTemplate: https://api.example.test/user\n        bogus: 1\n"),
		"spec.resources[0].bogus": spec("  resources:\n    - name: Database\n      bogus: 1\n"),
		"spec.providedContracts[0].bogus": spec("  provides: [app-host]\n  providedContracts:\n" +
			"    - capability: app-host\n      bogus: 1\n"),
		"spec.lifecycle.bogus":     spec("  lifecycle:\n    endpoint: https://fixture.example.test/hook\n    bogus: 1\n"),
		"spec.lifecycle.extra":     spec("  lifecycle:\n    extra:\n      hooks:\n        - afterEnsure\n"),
		"spec.params[0].bogus":     spec("  params:\n    - name: image\n      bogus: 1\n"),
		"spec.observability.bogus": spec("  observability:\n    bogus: 1\n"),
		"spec.observability.resources[0].priced.bogus": spec("  observability:\n    resources:\n" +
			"      - id: app-instance\n        type: aws.ec2-instance\n        from: outputs.instanceId\n" +
			"        priced:\n          service: AmazonEC2\n          bogus: 1\n"),
		"spec.observability.metrics[0].series[0].bogus": spec("  observability:\n    metrics:\n" +
			"      - resource: app-instance\n        namespace: AWS/EC2\n        series:\n" +
			"          - name: cpu\n            metric: CPUUtilization\n            stat: Average\n            bogus: 1\n"),
		"spec.observability.cost.bogus": spec("  observability:\n    cost:\n      groupBy: [app]\n      bogus: 1\n"),
		"spec.docs.source.bogus": spec("  docs:\n    source:\n      git: https://example.test/fixture.git\n" +
			"      ref: v1.0.0\n      bogus: 1\n"),
		"spec.docs.pages[0].bogus":      spec("  docs:\n    pages:\n      - title: Start\n        path: start.md\n        bogus: 1\n"),
		"spec.docs.links[0].bogus":      spec("  docs:\n    links:\n      - rel: source\n        url: https://example.test\n        bogus: 1\n"),
		"spec.troubleshooting[0].bogus": spec("  troubleshooting:\n    - signature: quota-exceeded\n      bogus: 1\n"),
		"spec.continuity[0].bogus":      spec("  continuity:\n    - name: app-data\n      stateful: false\n      bogus: 1\n"),
		"spec.naming.bogus":             spec("  naming:\n    bogus:\n      maxLength: 10\n"),
		"spec.naming.appName.bogus":     spec("  naming:\n    appName:\n      maxLength: 27\n      bogus: 1\n"),
		"spec.naming.appName.derivedNames[0].bogus": spec("  naming:\n    appName:\n      maxLength: 27\n" +
			"      derivedNames:\n        - template: \"{appName}-db\"\n          limit: 30\n          bogus: 1\n"),
		"spec.lifecycle.settleBudget": spec("  lifecycle:\n    endpoint: https://fixture.example.test/hook\n    settleBudget: 75m\n"),
	}
}

// v1betaUnknownFields are betaManifest with one key no v1beta field names,
// by the path the refusal must name. ADR 0083 takes lifecycle hooks,
// retainsOnUnsubscribe and spec.naming out of v1beta, so each is unknown
// there.
func v1betaUnknownFields(t *testing.T) map[string][]byte {
	return map[string][]byte{
		"bogus":                   editBeta(t, "metadata:\n", "bogus: 1\nmetadata:\n"),
		"metadata.bogus":          editBeta(t, "  name: Fixture Block\n", "  name: Fixture Block\n  bogus: 1\n"),
		"spec.bogus":              editBeta(t, "  version: 1.0.0\n", "  version: 1.0.0\n  bogus: 1\n"),
		"spec.params[0].bogus":    editBeta(t, "      label: Container image\n", "      label: Container image\n      bogus: 1\n"),
		"spec.requires[0].bogus":  editBeta(t, "    - type: aws.ec2-instance\n", "    - type: aws.ec2-instance\n      bogus: 1\n"),
		"spec.resources[1].bogus": editBeta(t, "      type: aws.security-group\n", "      type: aws.security-group\n      bogus: 1\n"),
		"spec.lifecycle.bogus":    editBeta(t, "    contract: kit/v1\n", "    contract: kit/v1\n    bogus: 1\n"),
		"spec.lifecycle.hooks":    editBeta(t, "    contract: kit/v1\n", "    contract: kit/v1\n    hooks: [beforeEnsure]\n"),
		"spec.lifecycle.retainsOnUnsubscribe": editBeta(t, "    contract: kit/v1\n",
			"    contract: kit/v1\n    retainsOnUnsubscribe: true\n"),
		"spec.naming": editBeta(t, "  version: 1.0.0\n",
			"  version: 1.0.0\n  naming:\n    appName:\n      maxLength: 27\n"),
		"spec.troubleshooting": editBeta(t, "  version: 1.0.0\n",
			"  version: 1.0.0\n  troubleshooting:\n    - signature: quota-exceeded\n"),
		"spec.observability.metrics[0].series[0].bogus": editBeta(t, "  lifecycle:\n",
			"  observability:\n    metrics:\n      - resource: app-security-group\n        namespace: AWS/EC2\n"+
				"        series:\n          - name: cpu\n            metric: CPUUtilization\n            stat: Average\n"+
				"            bogus: 1\n  lifecycle:\n"),
		"spec.observability.cost.bogus": editBeta(t, "  lifecycle:\n",
			"  observability:\n    cost:\n      groupBy: [app]\n      bogus: 1\n  lifecycle:\n"),
	}
}

func TestParseManifest_RefusesAnUnknownFieldNamingItsPathUnderBothVersions(t *testing.T) {
	for version, cases := range map[string]map[string][]byte{
		"mitstack/v1alpha": v1alphaUnknownFields(t),
		"mitstack/v1beta":  v1betaUnknownFields(t),
	} {
		t.Run(version, func(t *testing.T) {
			for path, raw := range cases {
				t.Run(path, func(t *testing.T) {
					requireRefusedNaming(t, raw, path)
				})
			}
		})
	}
}
