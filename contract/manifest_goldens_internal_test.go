package contract

// The hooks goldens are pinned by the keys their YAML spells, which the manifest parser folds away.

import "testing"

func goldenTree(t *testing.T, raw []byte) *yamlNode {
	t.Helper()
	root, err := parseYAML(string(raw))
	if err != nil || root.kind != yamlMap {
		t.Fatalf("the golden is not a YAML mapping (%v):\n%s", err, raw)
	}
	return root
}

func anyItemHas(list *yamlNode, key string) bool {
	if list == nil {
		return false
	}
	for _, item := range list.items {
		if item.get(key) != nil {
			return true
		}
	}
	return false
}

func TestManifestGoldens_SpellTheirKeysAsPinned(t *testing.T) {
	cases := []struct {
		label  string
		golden []byte
		holds  func(root *yamlNode) bool
	}{
		{"the hooks golden nests hooks under spec.lifecycle", HooksManifestGolden(),
			func(root *yamlNode) bool { return root.get("spec").get("lifecycle").get("hooks") != nil }},
		{"the twin names its document with type: block", TypeBlockHooksManifestGolden(),
			func(root *yamlNode) bool { return root.get("type") != nil && root.get("type").value == "block" }},
		{"the twin carries no top-level kind", TypeBlockHooksManifestGolden(), func(root *yamlNode) bool { return root.get("kind") == nil }},
		{"the twin carries no layer under spec", TypeBlockHooksManifestGolden(),
			func(root *yamlNode) bool { return root.get("spec").get("layer") == nil }},
		{"the twin types a requirement or provided contract with format", TypeBlockHooksManifestGolden(), func(root *yamlNode) bool {
			spec := root.get("spec")
			return anyItemHas(spec.get("requires"), "format") || anyItemHas(spec.get("providedContracts"), "format")
		}},
		{"the twin declares a continuity unit with store", TypeBlockHooksManifestGolden(),
			func(root *yamlNode) bool { return anyItemHas(root.get("spec").get("continuity"), "store") }},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			root := goldenTree(t, tc.golden)

			holds := tc.holds(root)

			if !holds {
				t.Errorf("%s: it does not:\n%s", tc.label, tc.golden)
			}
		})
	}
}
