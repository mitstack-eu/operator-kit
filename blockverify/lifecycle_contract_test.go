package main

// blockverify judges a block by the lifecycle contract its manifest declares at spec.lifecycle.contract, as the platform's operator gates do.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const kitHandlerImport = "github.com/mitstack-eu/operator-kit/v2/handler"

// kitV1ManifestYAML declares the kit/v1 lifecycle contract in the shape
// postgres-db's manifest uses: a lifecycle mapping nested directly in spec.
const kitV1ManifestYAML = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: blockverify-kitv1-fixture
  name: blockverify kit/v1 fixture block
  owner: blockverify-tests
spec:
  layer: backend
  version: 1.0.0
  description: >
    A minimal fixture block on the kit/v1 lifecycle contract, used only to
    prove blockverify judges it by the kit/v1 rule.
  provides:
    - blockverify-kitv1-capability
  requires: []
  lifecycle:
    # kit/v1: the operator serves its verbs through the kit's handler.
    contract: kit/v1
    endpoint: https://api.mitstack.dev/operators/blockverify-kitv1-fixture/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
`

// kitV1MainGo binds the block to the kit from production code.
const kitV1MainGo = `package main

import "github.com/mitstack-eu/operator-kit/v2/handler"

var _ = handler.New

func main() {}
`

// kitV1MainNoHandlerGo is production code that never imports the handler.
const kitV1MainNoHandlerGo = `package main

func main() {}
`

// kitV1HandlerOnlyInTestGo imports the handler from a test file only, which
// binds a test rather than what the block serves.
const kitV1HandlerOnlyInTestGo = `package main

import (
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/handler"
)

func TestHandlerIsReachable(t *testing.T) {
	_ = handler.New
}
`

// kitV1FixtureFiles is a kit/v1 block with no kitshim.go and none of the
// three TestContract_ functions, whose main.go imports the kit handler.
func kitV1FixtureFiles(t *testing.T) map[string]string {
	t.Helper()
	goMod := "module fixture\n\ngo 1.25\n\n" +
		"require github.com/mitstack-eu/operator-kit/v2 v2.0.0\n\n" +
		"replace github.com/mitstack-eu/operator-kit/v2 => " + kitModuleDir(t) + "\n"
	return map[string]string{
		"go.mod":        goMod,
		"main.go":       kitV1MainGo,
		"manifest.yaml": kitV1ManifestYAML,
		"Dockerfile":    dockerfileContents,
	}
}

func newFixtureFrom(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, files)
	return dir
}

func TestTrustPlumbing_KitV1TargetPassesWithoutKitShim(t *testing.T) {
	dir := newFixtureFrom(t, kitV1FixtureFiles(t))

	got := trustPlumbingSuite(dir)
	if got.Status != "pass" {
		t.Fatalf("trust-plumbing status = %q, want %q for a kit/v1 target with no %s; detail: %s",
			got.Status, "pass", kitShimFile, got.Detail)
	}
	if !strings.Contains(got.Detail, "kit/v1") {
		t.Errorf("trust-plumbing detail = %q, want it to name the kit/v1 rule it applied", got.Detail)
	}
}

func TestContractSuite_KitV1TargetPassesWithoutTestContractFuncs(t *testing.T) {
	dir := newFixtureFrom(t, kitV1FixtureFiles(t))

	got := contractSuite(dir)
	if got.Status != "pass" {
		t.Fatalf("contract status = %q, want %q for a kit/v1 target defining none of %v; detail: %s",
			got.Status, "pass", contractRequiredTests, got.Detail)
	}
	if !strings.Contains(got.Detail, "kit/v1") {
		t.Errorf("contract detail = %q, want it to name the kit/v1 rule it applied", got.Detail)
	}
}

// The kit/v1 waiver covers kitshim.go alone: the rest of trust-plumbing still
// judges the target, so a redeclared kit-owned symbol still fails it.
func TestTrustPlumbing_KitV1TargetStillFailsARedeclaredKitOwnedSymbol(t *testing.T) {
	files := kitV1FixtureFiles(t)
	files["rogue.go"] = `package main

import "context"

func EmitProgress(ctx context.Context, detail string) error { return nil }
`
	dir := newFixtureFrom(t, files)

	got := trustPlumbingSuite(dir)
	if got.Status != "fail" {
		t.Fatalf("trust-plumbing status = %q, want %q; detail: %s", got.Status, "fail", got.Detail)
	}
	if !strings.Contains(got.Detail, "EmitProgress") {
		t.Errorf("trust-plumbing detail = %q, want it to name the redeclared symbol EmitProgress", got.Detail)
	}
}

func TestKitV1_FailsWhenNoNonTestFileImportsTheKitHandler(t *testing.T) {
	files := kitV1FixtureFiles(t)
	files["main.go"] = kitV1MainNoHandlerGo
	dir := newFixtureFrom(t, files)

	for _, s := range []SuiteResult{trustPlumbingSuite(dir), contractSuite(dir)} {
		if s.Status != "fail" {
			t.Errorf("%s status = %q, want %q when no non-test .go file imports %s; detail: %s",
				s.Name, s.Status, "fail", kitHandlerImport, s.Detail)
			continue
		}
		if !strings.Contains(s.Detail, kitHandlerImport) {
			t.Errorf("%s detail = %q, want it to name the expected import %s", s.Name, s.Detail, kitHandlerImport)
		}
	}
}

func TestKitV1_FailsWhenTheOnlyHandlerImportIsInATestFile(t *testing.T) {
	files := kitV1FixtureFiles(t)
	files["main.go"] = kitV1MainNoHandlerGo
	files["handler_wiring_test.go"] = kitV1HandlerOnlyInTestGo
	dir := newFixtureFrom(t, files)

	for _, s := range []SuiteResult{trustPlumbingSuite(dir), contractSuite(dir)} {
		if s.Status != "fail" {
			t.Errorf("%s status = %q, want %q when the handler import sits only in a _test.go file; detail: %s",
				s.Name, s.Status, "fail", s.Detail)
			continue
		}
		if !strings.Contains(s.Detail, kitHandlerImport) {
			t.Errorf("%s detail = %q, want it to name the expected import %s", s.Name, s.Detail, kitHandlerImport)
		}
	}
}

func TestRun_KitV1Target_NeitherSuiteSkippedAndEachNamesTheRule(t *testing.T) {
	bound := kitV1FixtureFiles(t)
	unbound := kitV1FixtureFiles(t)
	unbound["main.go"] = kitV1MainNoHandlerGo

	cases := map[string]map[string]string{
		"handler imported":     bound,
		"handler not imported": unbound,
	}
	for label, files := range cases {
		t.Run(label, func(t *testing.T) {
			dir := newFixtureFrom(t, files)
			stdout, _ := runBlockverify(t, "--json", "--standalone", dir)
			r := decodeReport(t, stdout)

			for _, name := range []string{"trust-plumbing", "contract"} {
				s := suiteByName(t, r, name)
				if s.Status != "pass" && s.Status != "fail" {
					t.Errorf("%s status = %q on a kit/v1 target, want pass or fail", name, s.Status)
				}
				if !strings.Contains(s.Detail, "kit/v1") {
					t.Errorf("%s detail = %q, want it to name the kit/v1 rule it applied", name, s.Detail)
				}
			}
		})
	}
}

func TestRun_UnreadableManifest_IsAToolingFailureNamingTheTarget(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		// A directory where the file should be: present, but cannot be read.
		"cannot be opened": func(t *testing.T, dir string) {
			if err := os.Mkdir(filepath.Join(dir, "manifest.yaml"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		// A name with no file behind it is unopenable, not absent.
		"dangling symlink": func(t *testing.T, dir string) {
			if err := os.Symlink(filepath.Join(dir, "nowhere.yaml"), filepath.Join(dir, "manifest.yaml")); err != nil {
				t.Fatal(err)
			}
		},
		// A key no manifest field names makes the parser refuse it.
		"refused by the parser": func(t *testing.T, dir string) {
			content := manifestYAML + "  lifecycle:\n    contract: kit/v1\n    frobnicate: true\n"
			if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}

	for label, breakManifest := range cases {
		t.Run(label, func(t *testing.T) {
			// Everything but the manifest passes the legacy rule, so a
			// legacy verdict here would read as a pass.
			files := goldenFixtureFiles(t)
			delete(files, "manifest.yaml")
			dir := newFixtureFrom(t, files)
			breakManifest(t, dir)

			stdout, exit := runBlockverify(t, "--json", "--standalone", dir)
			if exit != 1 {
				t.Errorf("exit = %d, want 1 for a manifest that cannot be read\nstdout:\n%s", exit, stdout)
			}
			r := decodeReport(t, stdout)
			if r.Verdict != "fail" {
				t.Errorf("verdict = %q, want %q", r.Verdict, "fail")
			}

			for _, name := range []string{"trust-plumbing", "contract"} {
				s := suiteByName(t, r, name)
				if s.Status != "fail" {
					t.Errorf("%s status = %q, want %q: the contract that judges this target is unknown, so the legacy rule must not judge it; detail: %s",
						name, s.Status, "fail", s.Detail)
					continue
				}
				if !strings.Contains(s.Detail, dir) {
					t.Errorf("%s detail = %q, want it to name the target %s", name, s.Detail, dir)
				}
				if !strings.Contains(s.Detail, "manifest.yaml") {
					t.Errorf("%s detail = %q, want it to name manifest.yaml", name, s.Detail)
				}
			}
		})
	}
}

func TestRun_UnknownContractValue_FailsNamingTheValue(t *testing.T) {
	files := goldenFixtureFiles(t)
	files["manifest.yaml"] = manifestYAML + "  lifecycle:\n    contract: kit/v2\n"
	dir := newFixtureFrom(t, files)

	stdout, exit := runBlockverify(t, "--json", "--standalone", dir)
	if exit != 1 {
		t.Errorf("exit = %d, want 1 for a contract value the runner has no rule for\nstdout:\n%s", exit, stdout)
	}
	r := decodeReport(t, stdout)
	if r.Verdict != "fail" {
		t.Errorf("verdict = %q, want %q", r.Verdict, "fail")
	}

	for _, name := range []string{"trust-plumbing", "contract"} {
		s := suiteByName(t, r, name)
		if s.Status != "fail" {
			t.Errorf("%s status = %q, want %q: an unknown contract must not fall through to the legacy rule; detail: %s",
				name, s.Status, "fail", s.Detail)
			continue
		}
		if !strings.Contains(s.Detail, "kit/v2") {
			t.Errorf("%s detail = %q, want it to name the value kit/v2", name, s.Detail)
		}
	}
}

// noContractManifestYAML declares no lifecycle contract, while carrying the
// text "contract: kit/v1" where it is not one: inside a folded description
// and as a key of spec.lifecycle.verbBudgets, a mapping nested under the
// lifecycle. A lifecycle mapping without a contract key still declares
// none.
const noContractManifestYAML = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: blockverify-fixture
  name: blockverify fixture block
  owner: blockverify-tests
spec:
  layer: backend
  version: 1.0.0
  description: >
    A legacy fixture block. This prose mentions
    contract: kit/v1
    only as text inside a folded scalar.
  provides:
    - blockverify-fixture-capability
  requires: []
  lifecycle:
    endpoint: https://api.mitstack.dev/operators/blockverify-fixture/hook
    operations: [subscribe, unsubscribe]
    verbBudgets:
      contract: kit/v1
  params:
    - name: mode
`

func noContractFixtureFiles(t *testing.T) map[string]string {
	t.Helper()
	files := goldenFixtureFiles(t)
	files["manifest.yaml"] = noContractManifestYAML
	return files
}

func TestNoContract_TrustPlumbingKeepsTheLegacyVerdict(t *testing.T) {
	if got := trustPlumbingSuite(newFixtureFrom(t, noContractFixtureFiles(t))); got.Status != "pass" {
		t.Errorf("trust-plumbing status = %q, want %q on a conformant legacy target; detail: %s", got.Status, "pass", got.Detail)
	}

	files := noContractFixtureFiles(t)
	delete(files, "kitshim.go")
	got := trustPlumbingSuite(newFixtureFrom(t, files))
	if got.Status != "fail" {
		t.Fatalf("trust-plumbing status = %q, want %q for a legacy target with no %s; detail: %s", got.Status, "fail", kitShimFile, got.Detail)
	}
	if !strings.Contains(got.Detail, kitShimFile) {
		t.Errorf("trust-plumbing detail = %q, want it to name %s", got.Detail, kitShimFile)
	}
}

func TestNoContract_ContractSuiteKeepsTheLegacyVerdict(t *testing.T) {
	if got := contractSuite(newFixtureFrom(t, noContractFixtureFiles(t))); got.Status != "pass" {
		t.Errorf("contract status = %q, want %q on a conformant legacy target; detail: %s", got.Status, "pass", got.Detail)
	}

	files := noContractFixtureFiles(t)
	delete(files, "contract_response_test.go")
	got := contractSuite(newFixtureFrom(t, files))
	if got.Status != "fail" {
		t.Fatalf("contract status = %q, want %q for a legacy target missing a TestContract_ function; detail: %s", got.Status, "fail", got.Detail)
	}
	if !strings.Contains(got.Detail, "TestContract_EmitsConformingResponse") {
		t.Errorf("contract detail = %q, want it to name TestContract_EmitsConformingResponse", got.Detail)
	}
}

func TestNoContract_SecretSafetyKeepsTheLegacyVerdict(t *testing.T) {
	if got := secretSafetySuite(newFixtureFrom(t, noContractFixtureFiles(t))); got.Status != "pass" {
		t.Errorf("secret-safety status = %q, want %q on a conformant legacy target; detail: %s", got.Status, "pass", got.Detail)
	}

	files := noContractFixtureFiles(t)
	delete(files, "secrets_leak_test.go")
	got := secretSafetySuite(newFixtureFrom(t, files))
	if got.Status != "fail" {
		t.Fatalf("secret-safety status = %q, want %q for a legacy target with no TestNoSecretLeak; detail: %s", got.Status, "fail", got.Detail)
	}
	if !strings.Contains(got.Detail, "TestNoSecretLeak") {
		t.Errorf("secret-safety detail = %q, want it to name TestNoSecretLeak", got.Detail)
	}
}

func TestNoContract_TeardownKeepsTheLegacyVerdict(t *testing.T) {
	if got := teardownSuite(newFixtureFrom(t, noContractFixtureFiles(t))); got.Status != "pass" {
		t.Errorf("teardown status = %q, want %q on a conformant legacy target; detail: %s", got.Status, "pass", got.Detail)
	}

	files := noContractFixtureFiles(t)
	delete(files, "teardown_test.go")
	got := teardownSuite(newFixtureFrom(t, files))
	if got.Status != "fail" {
		t.Fatalf("teardown status = %q, want %q for a legacy target with no teardown test; detail: %s", got.Status, "fail", got.Detail)
	}
	if !strings.Contains(got.Detail, "TestUnsubscribeDeletes") {
		t.Errorf("teardown detail = %q, want it to name TestUnsubscribeDeletes", got.Detail)
	}
}

func TestNoContract_DeliverableKeepsTheLegacyVerdict(t *testing.T) {
	if got := deliverableSuite(newFixtureFrom(t, noContractFixtureFiles(t))); got.Status != "pass" {
		t.Errorf("deliverable status = %q, want %q on a conformant legacy target; detail: %s", got.Status, "pass", got.Detail)
	}

	files := noContractFixtureFiles(t)
	delete(files, "Dockerfile")
	got := deliverableSuite(newFixtureFrom(t, files))
	if got.Status != "fail" {
		t.Fatalf("deliverable status = %q, want %q for a legacy target with no Dockerfile; detail: %s", got.Status, "fail", got.Detail)
	}
	if !strings.Contains(got.Detail, "Dockerfile") {
		t.Errorf("deliverable detail = %q, want it to name Dockerfile", got.Detail)
	}
}

const unverifiedSurvivedGo = `package main

import "context"

func (h *Handler) survived(_ context.Context, id string) bool {
	return h.Store.Exists(id)
}
`

const verifyOnlyInATestGo = `package main

import (
	"context"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/teardown"
)

func TestVerifyIsReachable(t *testing.T) {
	_ = teardown.Verify(context.Background(), teardown.Options{})
}
`

const verifyGatedByCommaOkGo = `package main

import (
	"context"

	"github.com/mitstack-eu/operator-kit/v2/teardown"
)

type existenceChecker interface{ Exists(string) bool }

func (h *Handler) survived(ctx context.Context, id string) bool {
	var store any = h.Store
	if checker, ok := store.(existenceChecker); ok {
		return teardown.Verify(ctx, teardown.Options{}, teardown.Check{
			Kind:    "fixture resource",
			Present: func(context.Context) (bool, error) { return checker.Exists(id), nil },
		})
	}
	return false
}
`

const verifyAfterAClosedCommaOkGo = `package main

import (
	"context"
	"log"

	"github.com/mitstack-eu/operator-kit/v2/teardown"
)

type existenceChecker interface{ Exists(string) bool }

func (h *Handler) survived(ctx context.Context, id string) bool {
	var store any = h.Store
	if _, ok := store.(existenceChecker); ok {
		log.Printf("store answers existence checks")
	}
	return teardown.Verify(ctx, teardown.Options{}, teardown.Check{
		Kind:    "fixture resource",
		Present: func(context.Context) (bool, error) { return h.Store.Exists(id), nil },
	})
}
`

const failingTeardownVerifyTestGo = `package main

import "testing"

func TestUnsubscribeVerifiesNothing(t *testing.T) {
	t.Fatal("the verification test fails")
}
`

const kitV1TeardownTestGo = `package main

import "testing"

func TestUnsubscribeDeletesWhatSubscribeCreated(t *testing.T) {}
`

func TestTeardownSuite_LegacyRuleRequiresAnUnconditionalVerifiedTeardown(t *testing.T) {
	tests := []struct {
		name       string
		files      func(t *testing.T) map[string]string
		wantStatus string
		wantDetail string
	}{
		{
			name:       "a teardown that asks the provider again through the kit, proven by a passing test, passes",
			files:      noContractFixtureFiles,
			wantStatus: "pass",
			wantDetail: "TestUnsubscribeVerifies",
		},
		{
			name: "a teardown that reports success without calling Survivor, Verify or Absent fails",
			files: func(t *testing.T) map[string]string {
				files := noContractFixtureFiles(t)
				files["verify.go"] = unverifiedSurvivedGo
				return files
			},
			wantStatus: "fail",
			wantDetail: "Survivor, Verify or Absent",
		},
		{
			name: "a verification call that lives only in a test file does not count",
			files: func(t *testing.T) map[string]string {
				files := noContractFixtureFiles(t)
				files["verify.go"] = unverifiedSurvivedGo
				files["verify_reachable_test.go"] = verifyOnlyInATestGo
				return files
			},
			wantStatus: "fail",
			wantDetail: "Survivor, Verify or Absent",
		},
		{
			name: "a verification call inside a comma-ok type assertion block fails, naming its file and line",
			files: func(t *testing.T) map[string]string {
				files := noContractFixtureFiles(t)
				files["verify.go"] = verifyGatedByCommaOkGo
				return files
			},
			wantStatus: "fail",
			wantDetail: "verify.go:14",
		},
		{
			name: "a comma-ok block that closes before the verification call does not gate it",
			files: func(t *testing.T) map[string]string {
				files := noContractFixtureFiles(t)
				files["verify.go"] = verifyAfterAClosedCommaOkGo
				return files
			},
			wantStatus: "pass",
			wantDetail: "TestUnsubscribeVerifies",
		},
		{
			name: "a module with no TestUnsubscribeVerifies test fails, naming it",
			files: func(t *testing.T) map[string]string {
				files := noContractFixtureFiles(t)
				delete(files, "teardown_verify_test.go")
				return files
			},
			wantStatus: "fail",
			wantDetail: "TestUnsubscribeVerifies",
		},
		{
			name: "a failing TestUnsubscribeVerifies test fails the suite",
			files: func(t *testing.T) map[string]string {
				files := noContractFixtureFiles(t)
				files["teardown_verify_test.go"] = failingTeardownVerifyTestGo
				return files
			},
			wantStatus: "fail",
			wantDetail: "FAIL",
		},
		{
			name: "a kit/v1 block is not held to the legacy rule, because the kit owns its teardown verification",
			files: func(t *testing.T) map[string]string {
				files := kitV1FixtureFiles(t)
				files["teardown_test.go"] = kitV1TeardownTestGo
				return files
			},
			wantStatus: "pass",
			wantDetail: "kit/v1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := newFixtureFrom(t, tc.files(t))

			got := teardownSuite(dir)

			if got.Status != tc.wantStatus {
				t.Fatalf("teardownSuite(%s).Status = %q; want %q; detail: %s", dir, got.Status, tc.wantStatus, got.Detail)
			}
			if !strings.Contains(got.Detail, tc.wantDetail) {
				t.Errorf("teardownSuite(%s).Detail = %q; want it to contain %q", dir, got.Detail, tc.wantDetail)
			}
		})
	}
}
