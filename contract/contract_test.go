package contract_test

import (
	"encoding/json"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
)

type goldenSet struct {
	name                   string
	fsys                   fs.FS
	dir                    string
	valid, invalid         func() (map[string][]byte, error)
	wantValid, wantInvalid []string
}

var goldenSets = []goldenSet{
	{"request goldens", contract.Goldens, contract.GoldensDir, contract.ValidGoldens, contract.InvalidGoldens,
		[]string{"subscribe", "unsubscribe", "upgrade", "validate"},
		[]string{"invalid-missing-nonce", "invalid-unknown-verb"}},
	{"response goldens", contract.ResponseGoldens, contract.ResponseGoldensDir, contract.ValidResponseGoldens, contract.InvalidResponseGoldens,
		[]string{"absent", "error", "last-error", "null-outputs", "outputs", "pending", "pending-redrive", "secret-outputs", "still-present"},
		[]string{"invalid-absent-not-bool", "invalid-outputs-not-object", "invalid-outputs-not-strings", "invalid-pending-not-bool", "invalid-pending-redrive-not-bool",
			"invalid-resource-secret-outputs-entry-not-object", "invalid-resource-secret-outputs-not-object",
			"invalid-resource-secret-outputs-value-not-string", "invalid-secret-outputs-not-strings"}},
}

type goldenSplit struct {
	files          []fs.DirEntry
	valid, invalid map[string][]byte
}

func splitOf(t *testing.T, set goldenSet) goldenSplit {
	t.Helper()
	files, err := fs.ReadDir(set.fsys, set.dir)
	if err != nil {
		t.Fatalf("fs.ReadDir(%s): %v", set.dir, err)
	}
	valid, err := set.valid()
	if err != nil {
		t.Fatalf("the valid %s: %v", set.name, err)
	}
	invalid, err := set.invalid()
	if err != nil {
		t.Fatalf("the invalid %s: %v", set.name, err)
	}
	return goldenSplit{files, valid, invalid}
}

func TestGoldenSets_HoldExactlyTheirPinnedGoldensSplitByPrefix(t *testing.T) {
	cases := []struct {
		behaviour string
		check     func(t *testing.T, set goldenSet, s goldenSplit)
	}{
		{"the directory holds exactly the pinned names", func(t *testing.T, set goldenSet, s goldenSplit) {
			var got []string
			for _, e := range s.files {
				if !e.IsDir() {
					got = append(got, strings.TrimSuffix(e.Name(), ".json"))
				}
			}
			want := append(append([]string(nil), set.wantValid...), set.wantInvalid...)
			sort.Strings(got)
			sort.Strings(want)
			if !slices.Equal(got, want) {
				t.Errorf("%s holds %v, want exactly %v", set.dir, got, want)
			}
		}},
		{"every file is valid JSON", func(t *testing.T, set goldenSet, s goldenSplit) {
			for _, e := range s.files {
				if e.IsDir() {
					continue
				}
				raw, err := fs.ReadFile(set.fsys, set.dir+"/"+e.Name())
				if err != nil {
					t.Fatalf("reading %s: %v", e.Name(), err)
				}
				var v any
				if err := json.Unmarshal(raw, &v); err != nil {
					t.Errorf("golden %s is not valid JSON: %v", e.Name(), err)
				}
			}
		}},
		{"the valid half is every unprefixed golden", func(t *testing.T, set goldenSet, s goldenSplit) {
			if got := sortedKeys(s.valid); !slices.Equal(got, set.wantValid) {
				t.Errorf("valid %s = %v, want exactly %v", set.name, got, set.wantValid)
			}
			for name := range s.valid {
				if strings.HasPrefix(name, contract.InvalidPrefix) {
					t.Errorf("valid %s holds %q, which carries the %q prefix", set.name, name, contract.InvalidPrefix)
				}
			}
		}},
		{"the invalid half is every prefixed golden", func(t *testing.T, set goldenSet, s goldenSplit) {
			if got := sortedKeys(s.invalid); !slices.Equal(got, set.wantInvalid) {
				t.Errorf("invalid %s = %v, want exactly %v", set.name, got, set.wantInvalid)
			}
			for name := range s.invalid {
				if !strings.HasPrefix(name, contract.InvalidPrefix) {
					t.Errorf("invalid %s holds %q, which lacks the %q prefix", set.name, name, contract.InvalidPrefix)
				}
			}
		}},
		{"the two halves partition the directory", func(t *testing.T, set goldenSet, s goldenSplit) {
			if len(s.valid)+len(s.invalid) != len(s.files) {
				t.Errorf("valid (%d) + invalid (%d) = %d, want %d, the directory's file count",
					len(s.valid), len(s.invalid), len(s.valid)+len(s.invalid), len(s.files))
			}
			for name := range s.valid {
				if _, dup := s.invalid[name]; dup {
					t.Errorf("%q is in both halves of the %s", name, set.name)
				}
			}
		}},
	}
	for _, set := range goldenSets {
		for _, tc := range cases {
			t.Run(set.name+"/"+tc.behaviour, func(t *testing.T) {
				s := splitOf(t, set)

				tc.check(t, set, s)
			})
		}
	}
}

func TestWireSchemas_AreDraft2020_12DocumentsWithAVersion(t *testing.T) {
	schemas := map[string][]byte{"envelope.schema.json": contract.EnvelopeSchema, "response.schema.json": contract.ResponseSchema}
	cases := []struct {
		behaviour string
		check     func(t *testing.T, name string, doc map[string]any)
	}{
		{"declares draft 2020-12", func(t *testing.T, name string, doc map[string]any) {
			if got, want := doc["$schema"], "https://json-schema.org/draft/2020-12/schema"; got != want {
				t.Errorf("%s $schema = %v, want %q", name, got, want)
			}
		}},
		{"declares a non-empty version", func(t *testing.T, name string, doc map[string]any) {
			if v, ok := doc["version"].(string); !ok || v == "" {
				t.Errorf("%s version = %v, want a non-empty string", name, doc["version"])
			}
		}},
	}
	for name, raw := range schemas {
		for _, tc := range cases {
			t.Run(name+"/"+tc.behaviour, func(t *testing.T) {
				if len(raw) == 0 {
					t.Fatalf("%s is empty", name)
				}

				doc := jsonObject(t, raw)

				tc.check(t, name, doc)
			})
		}
	}
}

func TestContract_NamesItsConstantsAsThePlatformDoes(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"InvalidPrefix", contract.InvalidPrefix, "invalid-"},
		{"ManifestAPIVersion", contract.ManifestAPIVersion, "mitstack/v1beta"},
		{"ManifestGoldensDir", contract.ManifestGoldensDir, "manifest-goldens"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("contract.%s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}
