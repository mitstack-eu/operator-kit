package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type reportSuite struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// report is a local struct, not the production type, so the test pins the JSON field names and shapes.
type report struct {
	SuiteVersion   string        `json:"suiteVersion"`
	KitVersion     string        `json:"kitVersion"`
	Mode           string        `json:"mode"`
	ManifestDigest string        `json:"manifestDigest"`
	Verdict        string        `json:"verdict"`
	ResultsDigest  string        `json:"resultsDigest"`
	Suites         []reportSuite `json:"suites"`
}

func runBlockverify(t *testing.T, args ...string) (stdout string, exitCode int) {
	t.Helper()
	var buf bytes.Buffer
	exitCode = Run(args, &buf)
	return buf.String(), exitCode
}

func decodeReport(t *testing.T, stdout string) report {
	t.Helper()
	var r report
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("stdout is not a valid JSON report: %v\nstdout:\n%s", err, stdout)
	}
	return r
}

func expectedManifestDigest(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
	if err != nil {
		t.Fatalf("reading fixture manifest.yaml to compute the expected digest: %v", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// digestEntry keeps name before status, so the digest equals `jq -c '[.suites[] | {name, status}]' | sha256sum`.
type digestEntry struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func expectedResultsDigest(suites []reportSuite) string {
	entries := make([]digestEntry, 0, len(suites))
	for _, s := range suites {
		entries = append(entries, digestEntry{Name: s.Name, Status: s.Status})
	}
	raw, _ := json.Marshal(entries)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func suiteByName(t *testing.T, r report, name string) reportSuite {
	t.Helper()
	for _, s := range r.Suites {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("report has no %q suite; suites present: %+v", name, r.Suites)
	return reportSuite{}
}

func TestRun_StandalonePass_GoldenFixture(t *testing.T) {
	dir := newGoldenFixture(t)

	stdout, exit := runBlockverify(t, "--json", "--standalone", dir)
	if exit != 0 {
		t.Fatalf("want exit 0 on a fully-conformant fixture, got %d\nstdout:\n%s", exit, stdout)
	}

	r := decodeReport(t, stdout)

	if r.Mode != "standalone" {
		t.Errorf("mode = %q, want %q", r.Mode, "standalone")
	}
	if r.SuiteVersion != "1.3.0" {
		t.Errorf("suiteVersion = %q, want %q (the suite set includes resources)", r.SuiteVersion, "1.3.0")
	}
	if r.KitVersion != "v0.1.0" {
		t.Errorf("kitVersion = %q, want %q (the kit pin floor)", r.KitVersion, "v0.1.0")
	}
	if r.Verdict != "pass" {
		t.Errorf("verdict = %q, want %q", r.Verdict, "pass")
	}

	wantManifestDigest := expectedManifestDigest(t, dir)
	if r.ManifestDigest != wantManifestDigest {
		t.Errorf("manifestDigest = %q, want %q (sha256 of <dir>/manifest.yaml)", r.ManifestDigest, wantManifestDigest)
	}
	if !strings.HasPrefix(r.ManifestDigest, "sha256:") {
		t.Errorf("manifestDigest = %q, want sha256:-prefixed", r.ManifestDigest)
	}

	wantResultsDigest := expectedResultsDigest(r.Suites)
	if r.ResultsDigest != wantResultsDigest {
		t.Errorf("resultsDigest = %q, want %q (sha256 over the compact [{name,status}] array)", r.ResultsDigest, wantResultsDigest)
	}

	wantOrder := []string{"trust-plumbing", "contract", "secret-safety", "teardown", "resources", "deliverable", "manifest", "naming"}
	if len(r.Suites) != len(wantOrder) {
		t.Fatalf("suites = %d entries, want %d: %+v", len(r.Suites), len(wantOrder), r.Suites)
	}
	for i, name := range wantOrder {
		if r.Suites[i].Name != name {
			t.Errorf("suites[%d].name = %q, want %q (run order is part of the contract)", i, r.Suites[i].Name, name)
		}
	}

	for _, name := range []string{"trust-plumbing", "contract", "secret-safety", "teardown", "deliverable", "manifest"} {
		s := suiteByName(t, r, name)
		if s.Status != "pass" {
			t.Errorf("suite %q status = %q, want %q; detail: %s", name, s.Status, "pass", s.Detail)
		}
	}

	naming := suiteByName(t, r, "naming")
	if naming.Status != "skipped" {
		t.Errorf("naming status = %q, want %q — the golden fixture declares no spec.naming, so no limit applies", naming.Status, "skipped")
	}
	if naming.Detail == "" {
		t.Error("naming must carry a reason in detail, not a silent skip")
	}

	resources := suiteByName(t, r, "resources")
	if resources.Status != "skipped" {
		t.Errorf("resources status = %q, want %q — the golden fixture's manifest is mitstack/v1alpha, so it declares no resources", resources.Status, "skipped")
	}
	if resources.Detail == "" {
		t.Error("resources must carry a reason in detail, not a silent skip")
	}
}

func TestRun_MissingManifestYaml_FailsTrustPlumbing(t *testing.T) {
	dir := t.TempDir()
	files := goldenFixtureFiles(t)
	delete(files, "manifest.yaml")
	writeFixture(t, dir, files)

	stdout, exit := runBlockverify(t, "--json", "--standalone", dir)
	if exit != 1 {
		t.Fatalf("want exit 1 (a target-specific problem, not a usage error) when manifest.yaml is missing, got %d\nstdout:\n%s", exit, stdout)
	}

	r := decodeReport(t, stdout)
	if r.Verdict != "fail" {
		t.Errorf("verdict = %q, want %q", r.Verdict, "fail")
	}
	tp := suiteByName(t, r, "trust-plumbing")
	if tp.Status != "fail" {
		t.Errorf("trust-plumbing status = %q, want %q", tp.Status, "fail")
	}
	if !strings.Contains(tp.Detail, "manifest.yaml") {
		t.Errorf("trust-plumbing detail = %q, want it to name manifest.yaml", tp.Detail)
	}
}

func TestRun_MissingDockerfile_FailsDeliverable(t *testing.T) {
	dir := t.TempDir()
	files := goldenFixtureFiles(t)
	delete(files, "Dockerfile")
	writeFixture(t, dir, files)

	stdout, exit := runBlockverify(t, "--json", "--standalone", dir)
	if exit != 1 {
		t.Fatalf("want exit 1 when Dockerfile is missing, got %d\nstdout:\n%s", exit, stdout)
	}

	r := decodeReport(t, stdout)
	if r.Verdict != "fail" {
		t.Errorf("verdict = %q, want %q", r.Verdict, "fail")
	}
	d := suiteByName(t, r, "deliverable")
	if d.Status != "fail" {
		t.Errorf("deliverable status = %q, want %q", d.Status, "fail")
	}
	if !strings.Contains(d.Detail, "Dockerfile") {
		t.Errorf("deliverable detail = %q, want it to name Dockerfile", d.Detail)
	}

}

func TestRun_RedeclaredKitOwnedSymbol_FailsTrustPlumbing(t *testing.T) {
	dir := t.TempDir()
	files := goldenFixtureFiles(t)
	// The redeclaration stops the module compiling, which the static trust-plumbing check must catch anyway.
	files["rogue.go"] = `package main

import "context"

func EmitProgress(ctx context.Context, detail string) error { return nil }
`
	writeFixture(t, dir, files)

	stdout, exit := runBlockverify(t, "--json", "--standalone", dir)
	if exit != 1 {
		t.Fatalf("want exit 1 when a kit-owned symbol is redeclared, got %d\nstdout:\n%s", exit, stdout)
	}

	r := decodeReport(t, stdout)
	if r.Verdict != "fail" {
		t.Errorf("verdict = %q, want %q", r.Verdict, "fail")
	}
	tp := suiteByName(t, r, "trust-plumbing")
	if tp.Status != "fail" {
		t.Errorf("trust-plumbing status = %q, want %q", tp.Status, "fail")
	}
	if !strings.Contains(tp.Detail, "EmitProgress") {
		t.Errorf("trust-plumbing detail = %q, want it to name the redeclared symbol EmitProgress", tp.Detail)
	}
}

func TestRun_MissingContractTests_FailsContract(t *testing.T) {
	dir := t.TempDir()
	files := goldenFixtureFiles(t)
	delete(files, "contract_conformance_test.go")
	writeFixture(t, dir, files)

	stdout, exit := runBlockverify(t, "--json", "--standalone", dir)
	if exit != 1 {
		t.Fatalf("want exit 1 when TestContract_* is missing, got %d\nstdout:\n%s", exit, stdout)
	}

	r := decodeReport(t, stdout)
	if r.Verdict != "fail" {
		t.Errorf("verdict = %q, want %q", r.Verdict, "fail")
	}
	c := suiteByName(t, r, "contract")
	if c.Status != "fail" {
		t.Errorf("contract status = %q, want %q — `go test -run TestContract_` with NO matching tests exits 0 (vacuous pass) unless presence is checked explicitly, exactly the gap check-operator-contract-tests.sh's grep guards against", c.Status, "fail")
	}
	if !strings.Contains(c.Detail, "TestContract_") {
		t.Errorf("contract detail = %q, want it to name the missing TestContract_* function(s)", c.Detail)
	}
}

// A name in contractRequiredTests that the presence check never reaches would satisfy the list comparison yet let a module with no response coverage through.
func TestRun_MissingResponseContractTest_FailsContract(t *testing.T) {
	dir := t.TempDir()
	files := goldenFixtureFiles(t)
	delete(files, "contract_response_test.go")
	writeFixture(t, dir, files)

	stdout, exit := runBlockverify(t, "--json", "--standalone", dir)
	if exit != 1 {
		t.Fatalf("want exit 1 when TestContract_EmitsConformingResponse is missing, got %d\nstdout:\n%s", exit, stdout)
	}

	r := decodeReport(t, stdout)
	if r.Verdict != "fail" {
		t.Errorf("verdict = %q, want %q", r.Verdict, "fail")
	}
	c := suiteByName(t, r, "contract")
	if c.Status != "fail" {
		t.Errorf("contract status = %q, want %q — the response half of the webhook contract is required of every operator (the engine's decoder discards its unmarshal error, so a non-conforming response loses the whole payload silently)", c.Status, "fail")
	}
	if !strings.Contains(c.Detail, "TestContract_EmitsConformingResponse") {
		t.Errorf("contract detail = %q, want it to name the missing TestContract_EmitsConformingResponse", c.Detail)
	}
}

func TestRun_ResultsDigest_DeterministicAcrossRuns(t *testing.T) {
	dir := newGoldenFixture(t)

	stdout1, exit1 := runBlockverify(t, "--json", "--standalone", dir)
	if exit1 != 0 {
		t.Fatalf("first run: want exit 0, got %d\n%s", exit1, stdout1)
	}
	stdout2, exit2 := runBlockverify(t, "--json", "--standalone", dir)
	if exit2 != 0 {
		t.Fatalf("second run: want exit 0, got %d\n%s", exit2, stdout2)
	}

	r1 := decodeReport(t, stdout1)
	r2 := decodeReport(t, stdout2)

	if r1.ResultsDigest != r2.ResultsDigest {
		t.Errorf("resultsDigest is not deterministic across identical runs: %q vs %q", r1.ResultsDigest, r2.ResultsDigest)
	}
	if r1.ManifestDigest != r2.ManifestDigest {
		t.Errorf("manifestDigest is not deterministic across identical runs: %q vs %q", r1.ManifestDigest, r2.ManifestDigest)
	}
}

func TestRun_ResultsDigest_ChangesWhenASuiteOutcomeChanges(t *testing.T) {
	passDir := newGoldenFixture(t)
	passStdout, exit := runBlockverify(t, "--json", "--standalone", passDir)
	if exit != 0 {
		t.Fatalf("golden fixture: want exit 0, got %d\n%s", exit, passStdout)
	}
	passReport := decodeReport(t, passStdout)

	failDir := t.TempDir()
	files := goldenFixtureFiles(t)
	delete(files, "Dockerfile")
	writeFixture(t, failDir, files)
	failStdout, _ := runBlockverify(t, "--json", "--standalone", failDir)
	failReport := decodeReport(t, failStdout)

	if passReport.ResultsDigest == failReport.ResultsDigest {
		t.Errorf("resultsDigest must change when a suite's status changes (pass fixture vs missing-Dockerfile fixture): both were %q", passReport.ResultsDigest)
	}
}

// standaloneInvocation is the command a block author runs from the block's own directory.
const standaloneInvocation = "blockverify --standalone ."

func TestRun_RepoModeRefused_NoStandaloneFlag(t *testing.T) {
	dir := newGoldenFixture(t)

	stdout, exit := runBlockverify(t, dir)
	if exit != 2 {
		t.Fatalf("want exit 2 (usage/config error) refusing repo mode, got %d\nstdout:\n%s", exit, stdout)
	}
	if !strings.Contains(stdout, standaloneInvocation) {
		t.Errorf("refusal message must show the standalone invocation %q, got:\n%s", standaloneInvocation, stdout)
	}
}

func TestRun_RepoModeRefused_NoArgsAtAll(t *testing.T) {
	stdout, exit := runBlockverify(t)
	if exit != 2 {
		t.Fatalf("want exit 2 with no arguments at all, got %d\nstdout:\n%s", exit, stdout)
	}
	if !strings.Contains(stdout, standaloneInvocation) {
		t.Errorf("refusal message must show the standalone invocation %q, got:\n%s", standaloneInvocation, stdout)
	}
}

func TestRun_UsageError_StandaloneFlagWithNoTarget(t *testing.T) {
	stdout, exit := runBlockverify(t, "--json", "--standalone")
	if exit != 2 {
		t.Fatalf("want exit 2 when --standalone has no target, got %d\nstdout:\n%s", exit, stdout)
	}
}

// The fixture serves a copy of hcloud-k3s-cluster's manifest typed capability, beside the platform's types it names.
func TestRun_StandalonePassesACapabilityThatImplementsNoIssueHandler(t *testing.T) {
	const fixture = "testdata/capability-without-issue-handler"
	files := kitV1FixtureFiles(t)
	delete(files, "main.go")
	entries, err := os.ReadDir(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(fixture, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(raw)
	}
	dir := newFixtureFrom(t, files)

	stdout, exit := runBlockverify(t, "--json", "--standalone", dir)

	if r := decodeReport(t, stdout); exit != 0 || r.Verdict != "pass" {
		t.Errorf("exit %d, verdict %q; want 0 and pass\nstdout:\n%s", exit, r.Verdict, stdout)
	}
}
