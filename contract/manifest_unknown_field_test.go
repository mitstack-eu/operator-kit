package contract_test

// contract.ParseManifest refuses a key no field of the manifest's version names, at any depth, naming its path.

import (
	"strings"
	"testing"
)

// mitstack/v1beta has no lifecycle hooks, retainsOnUnsubscribe, spec.naming, spec.dependsOn or optional requirement, and no retired entry key, so each is unknown.
func v1betaUnknownFields(t *testing.T) map[string][]byte {
	beta := func(old, new string) []byte { return edit(t, betaManifest, old, new) }
	const hostType, containerType = "      type: aws.ec2-instance\n", "      type: aws.ec2-container\n"
	retired := func(old, added string) []byte { return edit(t, retiredKeyManifest, old, old+added) }
	return map[string][]byte{
		"bogus":                          beta("metadata:\n", "bogus: 1\nmetadata:\n"),
		"metadata.bogus":                 beta("  name: Fixture Block\n", "  name: Fixture Block\n  bogus: 1\n"),
		"metadata.labels":                beta("  name: Fixture Block\n", "  name: Fixture Block\n  labels: none\n"),
		"spec.bogus":                     beta("  version: 1.0.0\n", "  version: 1.0.0\n  bogus: 1\n"),
		"spec.outputs":                   beta("  requires:\n", "  outputs: [host]\n  requires:\n"),
		"spec.dependsOn":                 beta("  requires:\n", "  dependsOn:\n    - weather-app\n  requires:\n"),
		"spec.params[0].bogus":           beta("      label: Container image\n", "      label: Container image\n      bogus: 1\n"),
		"spec.params[0].type":            beta("      label: Container image\n", "      label: Container image\n      type: string\n"),
		"spec.requires[0].bogus":         beta("    - type: aws.ec2-instance\n", "    - type: aws.ec2-instance\n      bogus: 1\n"),
		"spec.requires[0].capability":    beta("    - type: aws.ec2-instance\n", "    - type: aws.ec2-instance\n      capability: app-host\n"),
		"spec.requires[0].optional":      beta("    - type: aws.ec2-instance\n", "    - type: aws.ec2-instance\n      optional: true\n"),
		"spec.requires[0].reads":         beta("    - type: aws.ec2-instance\n", "    - type: aws.ec2-instance\n      reads: [region]\n"),
		"spec.resources[1].bogus":        beta("      type: aws.security-group\n", "      type: aws.security-group\n      bogus: 1\n"),
		"spec.resources[1].ownership":    beta("      type: aws.security-group\n", "      type: aws.security-group\n      ownership: created\n"),
		"spec.lifecycle.bogus":           beta("    contract: kit/v1\n", "    contract: kit/v1\n    bogus: 1\n"),
		"spec.lifecycle.hooks":           beta("    contract: kit/v1\n", "    contract: kit/v1\n    hooks: [beforeEnsure]\n"),
		"spec.lifecycle.upgradeStrategy": beta("    contract: kit/v1\n", "    contract: kit/v1\n    upgradeStrategy: rolling\n"),
		"spec.lifecycle.verbConnections.subscribe": beta("    contract: kit/v1\n",
			"    contract: kit/v1\n    verbConnections:\n      subscribe: [github]\n"),
		"spec.lifecycle.retainsOnUnsubscribe": beta("    contract: kit/v1\n", "    contract: kit/v1\n    retainsOnUnsubscribe: true\n"),
		"spec.naming":                         beta("  version: 1.0.0\n", "  version: 1.0.0\n  naming:\n    appName:\n      maxLength: 27\n"),
		"spec.troubleshooting":                beta("  version: 1.0.0\n", "  version: 1.0.0\n  troubleshooting:\n    - signature: quota-exceeded\n"),
		"spec.observability.resources": beta("  lifecycle:\n",
			"  observability:\n    resources:\n      - name: fallback-network\n  lifecycle:\n"),
		"spec.observability.metrics[0].series[0].bogus": beta("  lifecycle:\n",
			"  observability:\n    metrics:\n      - resource: app-security-group\n        namespace: AWS/EC2\n"+
				"        series:\n          - name: cpu\n            metric: CPUUtilization\n            stat: Average\n"+
				"            bogus: 1\n  lifecycle:\n"),
		"spec.observability.cost.bogus": beta("  lifecycle:\n", "  observability:\n    cost:\n      groupBy: [app]\n      bogus: 1\n  lifecycle:\n"),
		"kind":                          beta("metadata:\n", "kind: Block\nmetadata:\n"),
		"spec.layer":                    beta("  version: 1.0.0\n", "  version: 1.0.0\n  layer: backend\n"),
		"spec.providedContracts": beta("  version: 1.0.0\n",
			"  version: 1.0.0\n  providedContracts:\n    - capability: app-host\n      kind: http\n"),
		"spec.continuity":          beta("  version: 1.0.0\n", "  version: 1.0.0\n  continuity:\n    - name: app-data\n      kind: postgres\n"),
		"spec.requires[1].kind":    retired(hostType, "      kind: host\n"),
		"spec.requires[1].in":      retired(hostType, "      in: aws\n"),
		"spec.requires[1].on":      retired(hostType, "      on: requires.github.repository\n"),
		"spec.requires[1].inputs":  retired(hostType, "      inputs: { region: params.image }\n"),
		"spec.requires[0].kind":    edit(t, retiredKeyManifest, "    - github.repository\n", "    - kind: git-repository\n"),
		"spec.resources[1].inputs": retired(containerType, "      inputs: { image: params.image }\n"),
		"spec.resources[1].on":     retired(containerType, "      on: requires.host\n"),
		"spec.resources[1].kind":   retired(containerType, "      kind: container\n"),
		"spec.resources[1].in":     retired(containerType, "      in: aws\n"),
	}
}

// retiredListForms are the key a retired entry carried written in a second form, so each refusal is a row of its own.
func retiredListForms(t *testing.T) map[string][]byte {
	const hostType, containerType = "      type: aws.ec2-instance\n", "      type: aws.ec2-container\n"
	return map[string][]byte{
		"spec.requires[1].in as a list": edit(t, retiredKeyManifest, hostType, hostType+"      in: [aws, hetzner]\n"),
		"spec.resources[1].inputs nested": edit(t, retiredKeyManifest, containerType,
			containerType+"      inputs:\n        env:\n          DB_HOST: requires.host.outputs.hostId\n"),
	}
}

func TestParseManifest_RefusesAnUnknownFieldNamingItsPath(t *testing.T) {
	for path, raw := range v1betaUnknownFields(t) {
		t.Run(path, func(t *testing.T) {
			requireRefusedNaming(t, raw, path+" is not a field of a mitstack/v1beta manifest")
		})
	}
	for label, raw := range retiredListForms(t) {
		t.Run(label, func(t *testing.T) {
			path, _, _ := strings.Cut(label, " ")

			requireRefusedNaming(t, raw, path+" is not a field of a mitstack/v1beta manifest")
		})
	}
}
