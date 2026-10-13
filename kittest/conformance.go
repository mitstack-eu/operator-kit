package kittest

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// goldenEntries fails the test rather than skipping, because a harness that finds nothing passes vacuously.
func goldenEntries(t *testing.T) []fs.DirEntry {
	t.Helper()
	entries, err := fs.ReadDir(contract.Goldens, contract.GoldensDir)
	if err != nil {
		t.Fatalf("kittest: cannot read embedded contract goldens: %v\n"+
			"The goldens are the contract; a conformance test that cannot find them proves nothing.", err)
	}
	if len(entries) == 0 {
		t.Fatalf("kittest: embedded contract goldens are empty — this harness would pass vacuously")
	}
	return entries
}

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := contract.Goldens.ReadFile(contract.GoldensDir + "/" + name)
	if err != nil {
		t.Fatalf("kittest: reading embedded golden %q: %v", name, err)
	}
	return raw
}

// GoldensDir materializes the embedded contract goldens into a t.TempDir() for callers that read the directory directly.
func GoldensDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, e := range goldenEntries(t) {
		if e.IsDir() {
			continue
		}
		raw := readGolden(t, e.Name())
		if err := os.WriteFile(filepath.Join(dir, e.Name()), raw, 0o600); err != nil {
			t.Fatalf("kittest: materializing embedded golden %s to %s: %v", e.Name(), dir, err)
		}
	}
	return dir
}

// Golden reads one golden envelope by name (without the .json suffix).
func Golden(t *testing.T, name string) []byte {
	t.Helper()
	return readGolden(t, name+".json")
}

// ValidGoldens returns the well-formed envelopes an operator must accept, excluding names starting with "invalid-".
func ValidGoldens(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, e := range goldenEntries(t) {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, contract.InvalidPrefix) {
			continue
		}
		out[strings.TrimSuffix(name, ".json")] = readGolden(t, name)
	}
	if len(out) == 0 {
		t.Fatal("kittest: no valid goldens found — this harness would pass vacuously")
	}
	return out
}

// AssertDecodesEveryValidGolden checks require against the golden's own JSON, because a dropped field decodes to the zero value and looks like an absent one.
func AssertDecodesEveryValidGolden(t *testing.T, decode func([]byte) error, require ...string) {
	t.Helper()

	for name, raw := range ValidGoldens(t) {
		t.Run(name, func(t *testing.T) {
			if err := decode(raw); err != nil {
				t.Fatalf("operator cannot decode the %s golden — the engine emits this: %v", name, err)
			}

			var envelope map[string]any
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatalf("golden %s is not valid JSON: %v", name, err)
			}
			for _, field := range require {
				if _, ok := envelope[field]; !ok {
					t.Errorf("golden %s has no %q field, but this operator requires it — "+
						"either the operator depends on something the contract does not promise, "+
						"or the golden is stale", name, field)
				}
			}
		})
	}
}

// AssertRejectsMalformed fails when the operator accepts an envelope the contract marks invalid.
func AssertRejectsMalformed(t *testing.T, accepts func([]byte) bool) {
	t.Helper()

	entries := goldenEntries(t)
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, contract.InvalidPrefix) {
			continue
		}
		checked++
		raw := readGolden(t, name)
		t.Run(strings.TrimSuffix(name, ".json"), func(t *testing.T) {
			if accepts(raw) {
				t.Errorf("operator ACCEPTED %s — the contract marks this envelope invalid, "+
					"so acting on it means acting on a request the engine considers malformed", name)
			}
		})
	}
	if checked == 0 {
		t.Fatal("kittest: no invalid- goldens found — the rejection half of this harness would pass vacuously")
	}
}

// Describe renders a short summary of the valid goldens found.
func Describe(t *testing.T) string {
	t.Helper()
	valid := ValidGoldens(t)
	names := make([]string, 0, len(valid))
	for n := range valid {
		names = append(names, n)
	}
	return fmt.Sprintf("%d valid golden(s): %s", len(names), strings.Join(names, ", "))
}
