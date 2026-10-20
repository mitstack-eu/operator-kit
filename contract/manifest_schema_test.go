package contract_test

// The committed manifest.schema.json is exactly what contract.GenerateManifestSchema builds from the manifest types, and it names exactly the keys the types do.

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

func TestManifestSchema_IsWhatTheTypesGenerate(t *testing.T) {
	if len(bytes.TrimSpace(contract.ManifestSchema)) == 0 {
		t.Fatal("contract.ManifestSchema is empty, so comparing it with the types proves nothing")
	}

	generated, err := contract.GenerateManifestSchema()

	if err != nil {
		t.Fatalf("contract.GenerateManifestSchema(): %v", err)
	}
	if !bytes.Equal(generated, contract.ManifestSchema) {
		t.Errorf("contract/manifest.schema.json differs from what the manifest types generate; "+
			"run go run ./cmd/manifest-schema > contract/manifest.schema.json\ngenerated:\n%s\ncommitted:\n%s",
			generated, contract.ManifestSchema)
	}
}

func TestManifestSchema_DeclaresDraft2020_12AndAnID(t *testing.T) {
	doc := jsonObject(t, contract.ManifestSchema)

	if got, want := doc["$schema"], "https://json-schema.org/draft/2020-12/schema"; got != want {
		t.Errorf("ManifestSchema $schema = %v, want %q", got, want)
	}
	if id, _ := doc["$id"].(string); id == "" {
		t.Errorf("ManifestSchema $id = %v, want a non-empty string", doc["$id"])
	}
}

func alternativeOfType(t *testing.T, root, node map[string]any, typ string) (map[string]any, bool) {
	t.Helper()
	node = schemaAt(t, root, node)
	list, ok := node["oneOf"].([]any)
	if !ok {
		list, ok = node["anyOf"].([]any)
	}
	alts := []map[string]any{node}
	if ok {
		alts = alts[:0]
		for _, item := range list {
			m, _ := item.(map[string]any)
			alts = append(alts, schemaAt(t, root, m))
		}
	}
	for _, alt := range alts {
		if alt["type"] == typ {
			return alt, true
		}
	}
	return nil, false
}

// An entry under requires or resources is a type id, or a closed object whose type is one id or a list of ids and is the one key required.
func TestManifestSchema_RequirementAndResourceCarryExactlyTheirKeys(t *testing.T) {
	doc := jsonObject(t, contract.ManifestSchema)
	cases := []struct {
		list       string
		properties []string
	}{
		{"requires", []string{"many", "name", "type"}},
		{"resources", []string{"name", "type"}},
	}
	for _, tc := range cases {
		t.Run(tc.list, func(t *testing.T) {
			items := schemaAt(t, doc, doc, "properties", "spec", "properties", tc.list, "items")

			entry, ok := alternativeOfType(t, doc, items, "object")

			if _, ok := alternativeOfType(t, doc, items, "string"); !ok {
				t.Errorf("spec.%s items take no type id alone, want a string form", tc.list)
			}
			if !ok {
				t.Fatalf("spec.%s items take no mapping form", tc.list)
			}
			props, _ := entry["properties"].(map[string]any)
			wantField(t, "spec."+tc.list+" entry properties", sortedKeys(props), tc.properties)
			wantField(t, "spec."+tc.list+" entry required", sortedStrings(entry["required"]), []string{"type"})
			wantField(t, "spec."+tc.list+" entry additionalProperties", entry["additionalProperties"], false)
			typeProp, _ := props["type"].(map[string]any)
			if typeProp == nil {
				t.Fatalf("spec.%s entries declare no type property", tc.list)
			}
			for _, form := range []string{"string", "array"} {
				if _, ok := alternativeOfType(t, doc, typeProp, form); !ok {
					t.Errorf("spec.%s entries' type takes no %s form", tc.list, form)
				}
			}
		})
	}
	// A block that creates nothing, or answers no operation, is not a block.
	for _, path := range [][]string{
		{"properties", "spec", "properties", "resources"},
		{"properties", "spec", "properties", "lifecycle", "properties", "operations"},
	} {
		t.Run(strings.Join(path, ".")+" minItems", func(t *testing.T) {
			if list := schemaAt(t, doc, doc, path...); list["minItems"] != float64(1) {
				t.Errorf("%s minItems = %v, want 1", strings.Join(path, "."), list["minItems"])
			}
		})
	}
}

func TestManifestSchema_ListsEachOptionalFieldWithItsType(t *testing.T) {
	generated, err := contract.GenerateManifestSchema()
	if err != nil {
		t.Fatalf("contract.GenerateManifestSchema(): %v", err)
	}
	cases := []struct {
		field, typ string
		parent     []string
		closed     bool
	}{
		{"receivesAppSecrets", "boolean", []string{"properties", "spec"}, false},
		{"needsCredentialRefresh", "boolean", []string{"properties", "spec", "properties", "lifecycle"}, true},
		{"settleBudget", "string", []string{"properties", "spec", "properties", "lifecycle"}, false},
	}
	for label, raw := range map[string][]byte{"committed": contract.ManifestSchema, "generated": generated} {
		for _, tc := range cases {
			t.Run(label+"/"+tc.field, func(t *testing.T) {
				d := jsonObject(t, raw)

				parent := schemaAt(t, d, d, tc.parent...)

				props, _ := parent["properties"].(map[string]any)
				prop, ok := props[tc.field].(map[string]any)
				if !ok {
					t.Fatalf("%s schema properties = %q, want %s among them", label, sortedKeys(props), tc.field)
				}
				if prop["type"] != tc.typ {
					t.Errorf("%s schema %s type = %v, want %s", label, tc.field, prop["type"], tc.typ)
				}
				if required := sortedStrings(parent["required"]); slices.Contains(required, tc.field) {
					t.Errorf("%s schema requires %q, want %s optional", label, required, tc.field)
				}
				if tc.closed && parent["additionalProperties"] != false {
					t.Errorf("%s schema additionalProperties = %v, want false", label, parent["additionalProperties"])
				}
			})
		}
	}
}
