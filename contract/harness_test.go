// Package contract_test pins the wire contract, the manifest parser and the registries the contract package owns.
package contract_test

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

func readManifestGolden(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := contract.ManifestGoldens.ReadFile(contract.ManifestGoldensDir + "/" + name)
	if err != nil {
		t.Fatalf("reading %s from contract.ManifestGoldens: %v", name, err)
	}
	return raw
}

// edit applies each old, new pair to base once and fails on an old text base lacks, so an edit never silently does nothing.
func edit(t *testing.T, base string, pairs ...string) []byte {
	t.Helper()
	for i := 0; i+1 < len(pairs); i += 2 {
		before, after, found := strings.Cut(base, pairs[i])
		if !found {
			t.Fatalf("fixture edit: %q is not in the manifest", pairs[i])
		}
		base = before + pairs[i+1] + after
	}
	return []byte(base)
}

func parseBeta(t *testing.T, raw []byte) *contract.BlockManifest {
	t.Helper()
	m, err := contract.ParseManifest(raw)
	if err != nil {
		t.Fatalf("ParseManifest: %v\n%s", err, raw)
	}
	if m.Block == nil {
		t.Fatalf("ParseManifest gave no Block for a %s manifest: %+v", contract.ManifestAPIVersion, m)
	}
	return m.Block
}

func parseAlpha(t *testing.T, raw string) *contract.V1AlphaManifest {
	t.Helper()
	m, err := contract.ParseManifest([]byte(raw))
	if err != nil {
		t.Fatalf("ParseManifest: %v\n%s", err, raw)
	}
	if m.V1Alpha == nil {
		t.Fatalf("ParseManifest gave no V1Alpha for a %s manifest: %+v", contract.ManifestAPIVersionV1Alpha, m)
	}
	return m.V1Alpha
}

func alphaSpec(t *testing.T, specLines string) *contract.V1AlphaManifest {
	t.Helper()
	return parseAlpha(t, alphaHead+specLines)
}

func requireErrorNaming(t *testing.T, what string, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s accepted, want an error naming %q", what, wants)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s error = %q, want it to name %q", what, err, want)
		}
	}
}

func requireRefusedNaming(t *testing.T, raw []byte, wants ...string) {
	t.Helper()
	m, err := contract.ParseManifest(raw)
	if err == nil {
		t.Fatalf("ParseManifest accepted the manifest (%+v), want an error naming %q:\n%s", m, wants, raw)
	}
	requireErrorNaming(t, "ParseManifest", err, wants...)
}

func wantField(t *testing.T, field string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", field, got, want)
	}
}

func requireLen(t *testing.T, field string, v any, n int) {
	t.Helper()
	if got := reflect.ValueOf(v).Len(); got != n {
		t.Fatalf("%s has %d items, want %d", field, got, n)
	}
}

func decodeGolden(t *testing.T, name string) contract.Request {
	t.Helper()
	valid, err := contract.ValidGoldens()
	if err != nil {
		t.Fatalf("contract.ValidGoldens(): %v", err)
	}
	raw, ok := valid[name]
	if !ok {
		t.Fatalf("contract.ValidGoldens() has no %q entry", name)
	}
	req, err := contract.DecodeRequest(raw)
	if err != nil {
		t.Fatalf("DecodeRequest(%s): %v", name, err)
	}
	return req
}

// mutateSubscribeGolden decodes the subscribe golden afresh on each call, so an edit to a nested map never leaks into another test's copy.
func mutateSubscribeGolden(t *testing.T, mutate func(m map[string]any)) []byte {
	t.Helper()
	valid, err := contract.ValidGoldens()
	if err != nil {
		t.Fatalf("contract.ValidGoldens(): %v", err)
	}
	m := jsonObject(t, valid["subscribe"])
	mutate(m)
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("re-marshal mutated subscribe golden: %v", err)
	}
	return raw
}

func jsonObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s is not a JSON object: %v", raw, err)
	}
	return m
}

// marshalObject encodes v and decodes it back as a JSON object, so a test compares what goes on the wire by key.
func marshalObject(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal(%+v): %v", v, err)
	}
	return jsonObject(t, raw)
}

// schemaAt follows path from node, replacing a node that carries a local "$ref" by what it points to in root before reading the next key.
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

// schemaTypes reads a node's "type" as a sorted list, whether the schema gives one type or several.
func schemaTypes(node map[string]any) []string {
	switch v := node["type"].(type) {
	case string:
		return []string{v}
	case []any:
		return sortedStrings(v)
	}
	return nil
}

func hasType(node map[string]any, want string) bool {
	for _, ty := range schemaTypes(node) {
		if ty == want {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
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

func platformSnapshot(t *testing.T) contract.TypeSnapshot {
	t.Helper()
	raw, err := os.ReadFile("testdata/type-snapshot.json")
	if err != nil {
		t.Fatalf("reading the type snapshot fixture: %v", err)
	}
	snap, err := contract.ParseTypeSnapshot(raw)
	if err != nil {
		t.Fatalf("ParseTypeSnapshot(testdata/type-snapshot.json): %v", err)
	}
	return snap
}

// registry fails on an empty registry, so a check over every entry cannot pass by having none to check.
func registry(t *testing.T) []contract.ResourceEntry {
	t.Helper()
	rs := contract.Resources()
	if len(rs) == 0 {
		t.Fatal("contract.Resources() is empty")
	}
	return rs
}
