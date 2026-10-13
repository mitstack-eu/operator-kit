package kittest_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// Every function below is exercised from a working directory unrelated to this repository, because the goldens are embedded rather than read from a path relative to the caller.

// chdirOutsideMonorepo moves the test process's cwd to a fresh temp
// directory with no ancestor relationship to this repo, and restores the
// original cwd when the test ends. t.TempDir() lives under the OS temp root,
// never under the mitstack-workspace checkout, so "../../../api/..." from
// there cannot resolve to anything real.
func chdirOutsideMonorepo(t *testing.T) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("os.Chdir(%s): %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func decodeOperation(t *testing.T, raw []byte) string {
	t.Helper()
	var envelope struct {
		Operation string `json:"operation"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("golden envelope is not valid JSON: %v\n%s", err, raw)
	}
	return envelope.Operation
}

// --- Portability (the core red) ---------------------------------------

func TestValidGoldens_PortableOutsideMonorepo(t *testing.T) {
	chdirOutsideMonorepo(t)

	got := kittest.ValidGoldens(t)

	want := []string{"subscribe", "unsubscribe", "upgrade", "validate"}
	if len(got) != len(want) {
		t.Fatalf("ValidGoldens() from outside the monorepo returned %d golden(s), want %d: %v",
			len(got), len(want), got)
	}
	for _, name := range want {
		raw, ok := got[name]
		if !ok {
			t.Errorf("ValidGoldens() from outside the monorepo is missing %q", name)
			continue
		}
		if op := decodeOperation(t, raw); op != name {
			t.Errorf("golden %q has operation %q, want %q", name, op, name)
		}
	}
}

func TestGolden_PortableOutsideMonorepo(t *testing.T) {
	chdirOutsideMonorepo(t)

	raw := kittest.Golden(t, "subscribe")
	if op := decodeOperation(t, raw); op != "subscribe" {
		t.Errorf("Golden(t, \"subscribe\") from outside the monorepo has operation %q, want %q", op, "subscribe")
	}
}

func TestAssertDecodesEveryValidGolden_PortableOutsideMonorepo(t *testing.T) {
	chdirOutsideMonorepo(t)

	// A trivial decoder + the contract fields every operator depends on
	// (mirrors examples/web-app-aws/aws-app-infra-cfn/contract_conformance_test.go).
	// The point here is not the decode itself — it is that this call, made
	// from outside the monorepo, must not abort the test the way
	// ValidGoldens does today.
	kittest.AssertDecodesEveryValidGolden(t, func(raw []byte) error {
		var v map[string]any
		return json.Unmarshal(raw, &v)
	}, "operation", "appId")
}

func TestAssertRejectsMalformed_PortableOutsideMonorepo(t *testing.T) {
	chdirOutsideMonorepo(t)

	kittest.AssertRejectsMalformed(t, func([]byte) bool { return false })
}

// TestGoldensDir_MaterializesReadableGoldensOutsideMonorepo pins the design
// decision for GoldensDir's post-embed behaviour: rather than deprecating an
// exported symbol with unknown external callers, it keeps working by
// materializing the embedded fixtures to a real directory (e.g. t.TempDir())
// and returning that path — so any caller still doing os.ReadDir /
// os.ReadFile against the returned directory keeps working unmodified, from
// anywhere.
func TestGoldensDir_MaterializesReadableGoldensOutsideMonorepo(t *testing.T) {
	chdirOutsideMonorepo(t)

	dir := kittest.GoldensDir(t)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("GoldensDir() returned %q but it cannot be listed: %v", dir, err)
	}
	if len(entries) == 0 {
		t.Fatalf("GoldensDir() returned %q but it is empty", dir)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "subscribe.json"))
	if err != nil {
		t.Fatalf("reading %s/subscribe.json: %v", dir, err)
	}
	if op := decodeOperation(t, raw); op != "subscribe" {
		t.Fatalf("GoldensDir()/subscribe.json has operation %q, want %q", op, "subscribe")
	}
}

// --- Content parity ------------------------------------------------------

// TestKittestGoldens_MatchContractPackage proves kittest carries no goldens
// of its own: every file the contract package embeds (valid AND
// invalid-prefixed) must be byte-identical to what kittest.Golden returns for
// the same name, read from OUTSIDE the monorepo, so this also re-proves
// portability for every golden, not just the 4 valid ones.
func TestKittestGoldens_MatchContractPackage(t *testing.T) {
	srcEntries, err := fs.ReadDir(contract.Goldens, contract.GoldensDir)
	if err != nil {
		t.Fatalf("fs.ReadDir(contract.Goldens, contract.GoldensDir): %v", err)
	}

	type fixture struct {
		name string
		want []byte
	}
	var fixtures []fixture
	for _, e := range srcEntries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		want, err := fs.ReadFile(contract.Goldens, contract.GoldensDir+"/"+name)
		if err != nil {
			t.Fatalf("reading contract package golden %s: %v", name, err)
		}
		fixtures = append(fixtures, fixture{name: name, want: want})
	}
	if len(fixtures) == 0 {
		t.Fatal("contract package goldens contained no .json files — this parity check would pass vacuously")
	}

	chdirOutsideMonorepo(t)

	for _, f := range fixtures {
		got := kittest.Golden(t, strings.TrimSuffix(f.name, ".json"))
		if string(got) != string(f.want) {
			t.Errorf("kittest.Golden(%q) does not match contract.Goldens/%s byte-for-byte",
				strings.TrimSuffix(f.name, ".json"), f.name)
		}
	}
}

// --- The valid/malformed split is preserved -------------------------------

func TestValidGoldens_ExcludesInvalidPrefixedNames(t *testing.T) {
	chdirOutsideMonorepo(t)

	got := kittest.ValidGoldens(t)
	for name := range got {
		if strings.HasPrefix(name, "invalid-") {
			t.Errorf("ValidGoldens() returned %q — names prefixed invalid- are the malformed set, not the valid one", name)
		}
	}
	if _, present := got["invalid-missing-nonce"]; present {
		t.Error("ValidGoldens() must not include invalid-missing-nonce")
	}
	if _, present := got["invalid-unknown-verb"]; present {
		t.Error("ValidGoldens() must not include invalid-unknown-verb")
	}
}

func TestAssertRejectsMalformed_ChecksOnlyInvalidPrefixedGoldens(t *testing.T) {
	chdirOutsideMonorepo(t)

	var checked []map[string]any
	kittest.AssertRejectsMalformed(t, func(raw []byte) bool {
		var envelope map[string]any
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("an envelope AssertRejectsMalformed exercised is not valid JSON: %v", err)
		}
		checked = append(checked, envelope)
		return false
	})

	// Today's fixture set has exactly 2 invalid- goldens
	// (invalid-missing-nonce, invalid-unknown-verb). This pins that
	// AssertRejectsMalformed drives its accepts() callback from the
	// invalid- set specifically, not e.g. every golden in the directory.
	if len(checked) != 2 {
		t.Fatalf("AssertRejectsMalformed exercised %d envelope(s), want exactly 2 (the invalid- goldens): %v", len(checked), checked)
	}

	for _, envelope := range checked {
		_, hasNonce := envelope["nonce"]
		op, _ := envelope["operation"].(string)
		knownVerb := op == "subscribe" || op == "unsubscribe" || op == "upgrade" || op == "validate"
		if hasNonce && knownVerb {
			t.Errorf("AssertRejectsMalformed exercised an envelope that looks well-formed "+
				"(nonce present, known verb %q) — the invalid- split may be broken", op)
		}
	}
}
