package contract_test

// manifest_schema_test.go pins the JSON Schema the kit ships for a
// mitstack/v1beta manifest: the committed manifest.schema.json is exactly
// what contract.GenerateManifestSchema builds from the manifest types, so
// the schema and the types cannot drift apart, and it describes requirements
// and resources with exactly the keys the types name.

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"sort"
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

func schemaDoc(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(contract.ManifestSchema, &doc); err != nil {
		t.Fatalf("contract.ManifestSchema is not a JSON object: %v", err)
	}
	return doc
}

// schemaAt follows path from node through the schema: each element names a
// key, and a node carrying a local "$ref" is replaced by what it points to
// in root before the next key is read.
func schemaAt(t *testing.T, root map[string]any, node map[string]any, path ...string) map[string]any {
	t.Helper()
	for i := 0; ; i++ {
		for hops := 0; ; hops++ {
			ref, ok := node["$ref"].(string)
			if !ok {
				break
			}
			if hops > 8 || !strings.HasPrefix(ref, "#/") {
				t.Fatalf("schema $ref %q is not a local reference the test can follow", ref)
			}
			target := any(root)
			for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
				m, _ := target.(map[string]any)
				target = m[part]
			}
			next, ok := target.(map[string]any)
			if !ok {
				t.Fatalf("schema $ref %q points at nothing", ref)
			}
			node = next
		}
		if i == len(path) {
			return node
		}
		next, ok := node[path[i]].(map[string]any)
		if !ok {
			t.Fatalf("schema has no object at %s", strings.Join(path[:i+1], "."))
		}
		node = next
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedStrings(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, _ := item.(string)
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func TestManifestSchema_DeclaresDraft2020_12AndAnID(t *testing.T) {
	doc := schemaDoc(t)
	if got, want := doc["$schema"], "https://json-schema.org/draft/2020-12/schema"; got != want {
		t.Errorf("ManifestSchema $schema = %v, want %q", got, want)
	}
	if id, _ := doc["$id"].(string); id == "" {
		t.Errorf("ManifestSchema $id = %v, want a non-empty string", doc["$id"])
	}
}

func alternatives(t *testing.T, root, node map[string]any) []map[string]any {
	t.Helper()
	node = schemaAt(t, root, node)
	list, ok := node["oneOf"].([]any)
	if !ok {
		list, ok = node["anyOf"].([]any)
	}
	if !ok {
		return []map[string]any{node}
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		m, _ := item.(map[string]any)
		out = append(out, schemaAt(t, root, m))
	}
	return out
}

func alternativeOfType(t *testing.T, root, node map[string]any, typ string) (map[string]any, bool) {
	t.Helper()
	for _, alt := range alternatives(t, root, node) {
		if alt["type"] == typ {
			return alt, true
		}
	}
	return nil, false
}

// An entry under requires or resources is a type id, or a closed object
// with exactly the keys ADR 0085 gives it, whose type is one id or a list of
// ids and is the one key required.
func TestManifestSchema_RequirementAndResourceCarryExactlyTheirKeys(t *testing.T) {
	doc := schemaDoc(t)
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
			if _, ok := alternativeOfType(t, doc, items, "string"); !ok {
				t.Errorf("spec.%s items take no type id alone, want a string form", tc.list)
			}
			entry, ok := alternativeOfType(t, doc, items, "object")
			if !ok {
				t.Fatalf("spec.%s items take no mapping form", tc.list)
			}
			props, _ := entry["properties"].(map[string]any)
			if got := sortedKeys(props); !reflect.DeepEqual(got, tc.properties) {
				t.Errorf("spec.%s entries carry properties %q, want exactly %q", tc.list, got, tc.properties)
			}
			if got := sortedStrings(entry["required"]); !reflect.DeepEqual(got, []string{"type"}) {
				t.Errorf("spec.%s entries require %q, want [type]", tc.list, got)
			}
			if entry["additionalProperties"] != false {
				t.Errorf("spec.%s entries additionalProperties = %v, want false", tc.list, entry["additionalProperties"])
			}
			typeProp, _ := props["type"].(map[string]any)
			if typeProp == nil {
				t.Fatalf("spec.%s entries declare no type property", tc.list)
			}
			if _, ok := alternativeOfType(t, doc, typeProp, "string"); !ok {
				t.Errorf("spec.%s entries' type takes no single type id", tc.list)
			}
			if _, ok := alternativeOfType(t, doc, typeProp, "array"); !ok {
				t.Errorf("spec.%s entries' type takes no list of type ids", tc.list)
			}
		})
	}

	// A block that creates nothing, or answers no operation, is not a block.
	for _, path := range [][]string{
		{"properties", "spec", "properties", "resources"},
		{"properties", "spec", "properties", "lifecycle", "properties", "operations"},
	} {
		list := schemaAt(t, doc, doc, path...)
		if list["minItems"] != float64(1) {
			t.Errorf("%s minItems = %v, want 1", strings.Join(path, "."), list["minItems"])
		}
	}
}

func TestManifestSchema_SpecListsReceivesAppSecretsAsABoolean(t *testing.T) {
	doc := schemaDoc(t)
	generated, err := contract.GenerateManifestSchema()
	if err != nil {
		t.Fatalf("contract.GenerateManifestSchema(): %v", err)
	}
	var gen map[string]any
	if err := json.Unmarshal(generated, &gen); err != nil {
		t.Fatalf("generated schema is not a JSON object: %v", err)
	}

	for label, d := range map[string]map[string]any{"committed": doc, "generated": gen} {
		props := schemaAt(t, d, d, "properties", "spec", "properties")
		prop, ok := props["receivesAppSecrets"].(map[string]any)
		if !ok {
			t.Errorf("%s schema spec properties = %q, want receivesAppSecrets among them", label, sortedKeys(props))
			continue
		}
		if prop["type"] != "boolean" {
			t.Errorf("%s schema spec.receivesAppSecrets type = %v, want boolean", label, prop["type"])
		}
	}
}

func TestManifestSchema_LifecycleListsNeedsCredentialRefreshAsAnOptionalBoolean(t *testing.T) {
	doc := schemaDoc(t)
	generated, err := contract.GenerateManifestSchema()
	if err != nil {
		t.Fatalf("contract.GenerateManifestSchema(): %v", err)
	}
	var gen map[string]any
	if err := json.Unmarshal(generated, &gen); err != nil {
		t.Fatalf("generated schema is not a JSON object: %v", err)
	}

	for label, d := range map[string]map[string]any{"committed": doc, "generated": gen} {
		lifecycle := schemaAt(t, d, d, "properties", "spec", "properties", "lifecycle")
		props, _ := lifecycle["properties"].(map[string]any)
		prop, ok := props["needsCredentialRefresh"].(map[string]any)
		if !ok {
			t.Errorf("%s schema spec.lifecycle properties = %q, want needsCredentialRefresh among them", label, sortedKeys(props))
			continue
		}
		if prop["type"] != "boolean" {
			t.Errorf("%s schema spec.lifecycle.needsCredentialRefresh type = %v, want boolean", label, prop["type"])
		}
		if required := sortedStrings(lifecycle["required"]); slices.Contains(required, "needsCredentialRefresh") {
			t.Errorf("%s schema spec.lifecycle requires %q, want needsCredentialRefresh optional", label, required)
		}
		if lifecycle["additionalProperties"] != false {
			t.Errorf("%s schema spec.lifecycle additionalProperties = %v, want false", label, lifecycle["additionalProperties"])
		}
	}
}

func TestManifestSchema_LifecycleListsSettleBudgetAsAnOptionalString(t *testing.T) {
	doc := schemaDoc(t)

	lifecycle := schemaAt(t, doc, doc, "properties", "spec", "properties", "lifecycle")
	budget := schemaAt(t, doc, lifecycle, "properties", "settleBudget")

	if budget["type"] != "string" || slices.Contains(sortedStrings(lifecycle["required"]), "settleBudget") {
		t.Errorf("schema spec.lifecycle.settleBudget = %v with required %v, want an optional string", budget, lifecycle["required"])
	}
}
