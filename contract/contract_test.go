// Package contract_test pins the schemas and golden sets this repository owns.
package contract_test

import (
	"encoding/json"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// --- schemas ---------------------------------------------------------------

func TestEnvelopeSchema_NonEmptyValidJSON(t *testing.T) {
	if len(contract.EnvelopeSchema) == 0 {
		t.Fatal("contract.EnvelopeSchema is empty")
	}
	var doc map[string]any
	if err := json.Unmarshal(contract.EnvelopeSchema, &doc); err != nil {
		t.Fatalf("contract.EnvelopeSchema is not valid JSON: %v", err)
	}
}

func TestEnvelopeSchema_DeclaresDraft2020_12(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contract.EnvelopeSchema, &doc); err != nil {
		t.Fatalf("contract.EnvelopeSchema is not valid JSON: %v", err)
	}
	got, _ := doc["$schema"].(string)
	want := "https://json-schema.org/draft/2020-12/schema"
	if got != want {
		t.Errorf("EnvelopeSchema $schema = %q, want %q", got, want)
	}
}

func TestEnvelopeSchema_DeclaresNonEmptyVersion(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contract.EnvelopeSchema, &doc); err != nil {
		t.Fatalf("contract.EnvelopeSchema is not valid JSON: %v", err)
	}
	v, ok := doc["version"].(string)
	if !ok || v == "" {
		t.Errorf("EnvelopeSchema top-level version = %q (present=%v), want a non-empty string", v, ok)
	}
}

func TestResponseSchema_NonEmptyValidJSON(t *testing.T) {
	if len(contract.ResponseSchema) == 0 {
		t.Fatal("contract.ResponseSchema is empty")
	}
	var doc map[string]any
	if err := json.Unmarshal(contract.ResponseSchema, &doc); err != nil {
		t.Fatalf("contract.ResponseSchema is not valid JSON: %v", err)
	}
}

func TestResponseSchema_DeclaresDraft2020_12(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contract.ResponseSchema, &doc); err != nil {
		t.Fatalf("contract.ResponseSchema is not valid JSON: %v", err)
	}
	got, _ := doc["$schema"].(string)
	want := "https://json-schema.org/draft/2020-12/schema"
	if got != want {
		t.Errorf("ResponseSchema $schema = %q, want %q", got, want)
	}
}

func TestResponseSchema_DeclaresNonEmptyVersion(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contract.ResponseSchema, &doc); err != nil {
		t.Fatalf("contract.ResponseSchema is not valid JSON: %v", err)
	}
	v, ok := doc["version"].(string)
	if !ok || v == "" {
		t.Errorf("ResponseSchema top-level version = %q (present=%v), want a non-empty string", v, ok)
	}
}

// --- request goldens ---------------------------------------------------

// wantGoldenNames are the exact file names (without .json) the platform's
// api/internal/lifecycle/contract/goldens/ carries today. The contract
// package is this repository's copy of that same set, so the names must
// match exactly — no more, no fewer.
var wantGoldenNames = []string{
	"subscribe", "unsubscribe", "upgrade", "validate",
	"invalid-missing-nonce", "invalid-unknown-verb",
}

func TestGoldensDir_ListsExpectedFileNames(t *testing.T) {
	entries, err := fs.ReadDir(contract.Goldens, contract.GoldensDir)
	if err != nil {
		t.Fatalf("fs.ReadDir(contract.Goldens, contract.GoldensDir): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("contract.Goldens has no entries under contract.GoldensDir — a vacuous contract")
	}
	var got []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		got = append(got, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(got)
	want := append([]string(nil), wantGoldenNames...)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("contract.GoldensDir has %d file(s) %v, want exactly %v", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("contract.GoldensDir file set = %v, want %v", got, want)
			break
		}
	}
}

func TestGoldens_EveryFileIsValidJSON(t *testing.T) {
	entries, err := fs.ReadDir(contract.Goldens, contract.GoldensDir)
	if err != nil {
		t.Fatalf("fs.ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := fs.ReadFile(contract.Goldens, contract.GoldensDir+"/"+e.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Errorf("golden %s is not valid JSON: %v", e.Name(), err)
		}
	}
}

func TestInvalidPrefix_IsInvalidDash(t *testing.T) {
	if contract.InvalidPrefix != "invalid-" {
		t.Errorf("contract.InvalidPrefix = %q, want %q", contract.InvalidPrefix, "invalid-")
	}
}

func TestValidGoldens_ExcludesInvalidPrefixedAndIsNonEmpty(t *testing.T) {
	got, err := contract.ValidGoldens()
	if err != nil {
		t.Fatalf("ValidGoldens(): %v", err)
	}
	if len(got) == 0 {
		t.Fatal("ValidGoldens() returned nothing — the acceptance half of the contract would be vacuous")
	}
	for name := range got {
		if strings.HasPrefix(name, contract.InvalidPrefix) {
			t.Errorf("ValidGoldens() returned %q, which carries the %q prefix", name, contract.InvalidPrefix)
		}
	}
	want := []string{"subscribe", "unsubscribe", "upgrade", "validate"}
	if len(got) != len(want) {
		t.Fatalf("ValidGoldens() returned %d golden(s) %v, want exactly %v", len(got), keysOf(got), want)
	}
	for _, name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("ValidGoldens() is missing %q", name)
		}
	}
}

func TestInvalidGoldens_OnlyInvalidPrefixedAndIsNonEmpty(t *testing.T) {
	got, err := contract.InvalidGoldens()
	if err != nil {
		t.Fatalf("InvalidGoldens(): %v", err)
	}
	if len(got) == 0 {
		t.Fatal("InvalidGoldens() returned nothing — the rejection half of the contract would be vacuous")
	}
	for name := range got {
		if !strings.HasPrefix(name, contract.InvalidPrefix) {
			t.Errorf("InvalidGoldens() returned %q, which does not carry the %q prefix", name, contract.InvalidPrefix)
		}
	}
	want := []string{"invalid-missing-nonce", "invalid-unknown-verb"}
	if len(got) != len(want) {
		t.Fatalf("InvalidGoldens() returned %d golden(s) %v, want exactly %v", len(got), keysOf(got), want)
	}
}

func TestValidAndInvalidGoldens_PartitionTheDirectoryExactly(t *testing.T) {
	valid, err := contract.ValidGoldens()
	if err != nil {
		t.Fatalf("ValidGoldens(): %v", err)
	}
	invalid, err := contract.InvalidGoldens()
	if err != nil {
		t.Fatalf("InvalidGoldens(): %v", err)
	}
	entries, err := fs.ReadDir(contract.Goldens, contract.GoldensDir)
	if err != nil {
		t.Fatalf("fs.ReadDir: %v", err)
	}
	if len(valid)+len(invalid) != len(entries) {
		t.Fatalf("ValidGoldens (%d) + InvalidGoldens (%d) = %d, want %d (the directory's file count)",
			len(valid), len(invalid), len(valid)+len(invalid), len(entries))
	}
	for name := range valid {
		if _, dup := invalid[name]; dup {
			t.Errorf("%q appears in both ValidGoldens and InvalidGoldens", name)
		}
	}
}

// --- response goldens ----------------------------------------------------

var wantResponseGoldenNames = []string{
	"absent", "error", "last-error", "null-outputs", "outputs", "pending",
	"secret-outputs", "still-present",
	"invalid-absent-not-bool", "invalid-outputs-not-object",
	"invalid-outputs-not-strings", "invalid-pending-not-bool",
	"invalid-secret-outputs-not-strings",
	"invalid-resource-secret-outputs-not-object",
	"invalid-resource-secret-outputs-entry-not-object",
	"invalid-resource-secret-outputs-value-not-string",
}

func TestResponseGoldensDir_ListsExpectedFileNames(t *testing.T) {
	entries, err := fs.ReadDir(contract.ResponseGoldens, contract.ResponseGoldensDir)
	if err != nil {
		t.Fatalf("fs.ReadDir(contract.ResponseGoldens, contract.ResponseGoldensDir): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("contract.ResponseGoldens has no entries under contract.ResponseGoldensDir — a vacuous contract")
	}
	var got []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		got = append(got, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(got)
	want := append([]string(nil), wantResponseGoldenNames...)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("contract.ResponseGoldensDir has %d file(s) %v, want exactly %v", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("contract.ResponseGoldensDir file set = %v, want %v", got, want)
			break
		}
	}
}

func TestResponseGoldens_EveryFileIsValidJSON(t *testing.T) {
	entries, err := fs.ReadDir(contract.ResponseGoldens, contract.ResponseGoldensDir)
	if err != nil {
		t.Fatalf("fs.ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := fs.ReadFile(contract.ResponseGoldens, contract.ResponseGoldensDir+"/"+e.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Errorf("response golden %s is not valid JSON: %v", e.Name(), err)
		}
	}
}

func TestValidResponseGoldens_ExcludesInvalidPrefixedAndIsNonEmpty(t *testing.T) {
	got, err := contract.ValidResponseGoldens()
	if err != nil {
		t.Fatalf("ValidResponseGoldens(): %v", err)
	}
	if len(got) == 0 {
		t.Fatal("ValidResponseGoldens() returned nothing — the acceptance half of the contract would be vacuous")
	}
	for name := range got {
		if strings.HasPrefix(name, contract.InvalidPrefix) {
			t.Errorf("ValidResponseGoldens() returned %q, which carries the %q prefix", name, contract.InvalidPrefix)
		}
	}
	want := []string{
		"absent", "error", "last-error", "null-outputs", "outputs", "pending",
		"secret-outputs", "still-present",
	}
	if len(got) != len(want) {
		t.Fatalf("ValidResponseGoldens() returned %d golden(s) %v, want exactly %v", len(got), keysOf(got), want)
	}
	for _, name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("ValidResponseGoldens() is missing %q", name)
		}
	}
}

func TestInvalidResponseGoldens_OnlyInvalidPrefixedAndIsNonEmpty(t *testing.T) {
	got, err := contract.InvalidResponseGoldens()
	if err != nil {
		t.Fatalf("InvalidResponseGoldens(): %v", err)
	}
	if len(got) == 0 {
		t.Fatal("InvalidResponseGoldens() returned nothing — the rejection half of the contract would be vacuous")
	}
	for name := range got {
		if !strings.HasPrefix(name, contract.InvalidPrefix) {
			t.Errorf("InvalidResponseGoldens() returned %q, which does not carry the %q prefix", name, contract.InvalidPrefix)
		}
	}
	want := []string{
		"invalid-absent-not-bool", "invalid-outputs-not-object",
		"invalid-outputs-not-strings", "invalid-pending-not-bool",
		"invalid-secret-outputs-not-strings",
		"invalid-resource-secret-outputs-not-object",
		"invalid-resource-secret-outputs-entry-not-object",
		"invalid-resource-secret-outputs-value-not-string",
	}
	if len(got) != len(want) {
		t.Fatalf("InvalidResponseGoldens() returned %d golden(s) %v, want exactly %v", len(got), keysOf(got), want)
	}
}

func TestValidAndInvalidResponseGoldens_PartitionTheDirectoryExactly(t *testing.T) {
	valid, err := contract.ValidResponseGoldens()
	if err != nil {
		t.Fatalf("ValidResponseGoldens(): %v", err)
	}
	invalid, err := contract.InvalidResponseGoldens()
	if err != nil {
		t.Fatalf("InvalidResponseGoldens(): %v", err)
	}
	entries, err := fs.ReadDir(contract.ResponseGoldens, contract.ResponseGoldensDir)
	if err != nil {
		t.Fatalf("fs.ReadDir: %v", err)
	}
	if len(valid)+len(invalid) != len(entries) {
		t.Fatalf("ValidResponseGoldens (%d) + InvalidResponseGoldens (%d) = %d, want %d (the directory's file count)",
			len(valid), len(invalid), len(valid)+len(invalid), len(entries))
	}
	for name := range valid {
		if _, dup := invalid[name]; dup {
			t.Errorf("%q appears in both ValidResponseGoldens and InvalidResponseGoldens", name)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
