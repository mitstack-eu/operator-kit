package contract_test

// contract.ParseManifest refuses a key no field of the manifest's version names, at any depth, naming its path.

import (
	"strings"
	"testing"
)

func v1alphaUnknownFields(t *testing.T) map[string][]byte {
	spec := func(lines string) []byte { return []byte(alphaHead + lines) }
	return map[string][]byte{
		"bogus":              edit(t, alphaHead, "spec:\n", "bogus: 1\nspec:\n"),
		"metadata.bogus":     edit(t, alphaHead, "  owner: fixture-org\n", "  owner: fixture-org\n  bogus: 1\n"),
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
		"spec.lifecycle.bogus":           spec("  lifecycle:\n    endpoint: https://fixture.example.test/hook\n    bogus: 1\n"),
		"spec.lifecycle.extra":           spec("  lifecycle:\n    extra:\n      hooks:\n        - afterEnsure\n"),
		"spec.params[0].bogus":           spec("  params:\n    - name: image\n      bogus: 1\n"),
		"spec.observability.bogus":       spec("  observability:\n    bogus: 1\n"),
		"spec.lifecycle.verbConnections": spec("  lifecycle:\n    verbConnections:\n      validate: [github]\n"),
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
	}
}

// mitstack/v1beta has no lifecycle hooks, retainsOnUnsubscribe or spec.naming, and no entry key a requirement or resource of mitstack/v1alpha carried, so each is unknown there.
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

func TestParseManifest_RefusesAnUnknownFieldNamingItsPathUnderBothVersions(t *testing.T) {
	for version, cases := range map[string]map[string][]byte{
		"mitstack/v1alpha": v1alphaUnknownFields(t),
		"mitstack/v1beta":  v1betaUnknownFields(t),
	} {
		for path, raw := range cases {
			t.Run(version+"/"+path, func(t *testing.T) {
				requireRefusedNaming(t, raw, path+" is not a field of a "+version+" manifest")
			})
		}
	}
	for label, raw := range retiredListForms(t) {
		t.Run("mitstack/v1beta/"+label, func(t *testing.T) {
			path, _, _ := strings.Cut(label, " ")

			requireRefusedNaming(t, raw, path+" is not a field of a mitstack/v1beta manifest")
		})
	}
}
