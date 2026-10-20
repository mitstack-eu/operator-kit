package main

// Each suite judges a fixture block by the lifecycle contract its manifest declares.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const kitHandlerImport = "github.com/mitstack-eu/operator-kit/v2/handler"

// scanFailureMarker is the phrase a scan that could not run carries and an absent import does not.
const scanFailureMarker = "could not scan"

const kitV1PathGoMod = "module fixture\n\ngo 1.25\n\nrequire github.com/mitstack-eu/operator-kit v1.32.0\n"

func appendTo(name, text string) option {
	return func(_ *testing.T, files map[string]string) { files[name] += text }
}

func shimOnKitV1Path(_ *testing.T, files map[string]string) {
	files["kitshim.go"] = strings.ReplaceAll(files["kitshim.go"], "operator-kit/v2/", "operator-kit/")
}

func githubBlock(file, src string) dirFunc {
	opts := []option{withSource("creds.go", "github-creds.go")}
	if file != "" {
		opts = append(opts, withSource(file, src))
	}
	return legacyBlock(opts...)
}

func TestTrustPlumbingSuite(t *testing.T) {
	runSuites(t, []string{"trust-plumbing"}, []suiteCase{
		{name: "a legacy block whose kitshim.go is the canonical passes", dir: noContractBlock(),
			status: "pass", detail: []string{"kitshim.go matches the kit's canonical"}},
		{name: "a legacy block with no kitshim.go fails, naming it", dir: noContractBlock(without(kitShimFile)),
			status: "fail", detail: []string{kitShimFile}},
		{name: "a legacy block whose kitshim.go drifts from the canonical fails, naming the file",
			dir: noContractBlock(appendTo(kitShimFile, "\nfunc localHelper() {}\n")), status: "fail", detail: []string{"kitshim.go differs from the kit's canonical"}},
		{name: "a legacy block on the kit's v1 module path is judged against the canonical with that path",
			dir: noContractBlock(with("go.mod", kitV1PathGoMod), shimOnKitV1Path), status: "pass", detail: []string{"kitshim.go matches the kit's canonical"}},
		{name: "a legacy block on the kit's v1 module path that carries the v2 shim fails",
			dir: noContractBlock(with("go.mod", kitV1PathGoMod)), status: "fail", detail: []string{"kitshim.go differs from the kit's canonical"}},
		{name: "a kit/v1 block passes with no kitshim.go, naming the kit/v1 rule", dir: kitV1Block(),
			status: "pass", detail: []string{"kit/v1"}},
		{name: "a kit/v1 block's kitshim.go is not judged, because the kit owns its binding",
			dir: kitV1Block(with(kitShimFile, "package main\n\nfunc localHelper() {}\n")), status: "pass", detail: []string{"kit/v1"}},
		{name: "a kit/v1 block that redeclares a kit-owned symbol still fails, naming it",
			dir: kitV1Block(withSource("rogue.go", "redeclares-emit-progress.go")), status: "fail", detail: []string{"EmitProgress"}},
		{name: "a module that reads no GitHub credential, instance or host is not judged", dir: githubBlock("", ""),
			status: "pass", detail: []string{"manifest.yaml present"}},
		{name: "a module that reads only the GitHub owner makes no API call and is not judged",
			dir: githubBlock("owner.go", "github-owner-only.go"), status: "pass", detail: []string{"manifest.yaml present"}},
		{name: "a GitHub read that lives only in a test file is not judged",
			dir: githubBlock("github_test.go", "github-token-in-a-test.go"), status: "pass", detail: []string{"manifest.yaml present"}},
		{name: "a GitHub-consuming module that resolves its base through ghapi.Base passes",
			dir: githubBlock("github.go", "github-through-ghapi-base.go"), status: "pass", detail: []string{"ghapi"}},
		{name: "a GitHub-consuming module that resolves its base through ghapi.BaseFromEnv passes",
			dir: githubBlock("github.go", "github-through-ghapi-base-from-env.go"), status: "pass", detail: []string{"ghapi"}},
		{name: "a urlguard.go module that returns the allowlist entry passes",
			dir: githubBlock("urlguard.go", "urlguard-allowlist.go"), status: "pass", detail: []string{"urlguard.go"}},
		{name: "a module that reads the GitHub token with neither urlguard.go nor ghapi fails",
			dir: githubBlock("github.go", "github-token-unguarded.go"), status: "fail", detail: []string{"ghapi.Base"}},
		{name: "a module that reads the GitHub instance with neither urlguard.go nor ghapi fails",
			dir: githubBlock("github.go", "github-instance-unguarded.go"), status: "fail", detail: []string{"ghapi.Base"}},
		{name: "a module that names the GitHub API host with neither urlguard.go nor ghapi fails",
			dir: githubBlock("github.go", "github-host-unguarded.go"), status: "fail", detail: []string{"ghapi.Base"}},
		{name: "a urlguard.go module that returns a base built from the request host fails",
			dir: githubBlock("urlguard.go", "urlguard-request-host.go"), status: "fail", detail: []string{"request host"}},
		{name: "a urlguard.go module with no allowedGitHubHosts fails",
			dir: githubBlock("urlguard.go", "urlguard-no-allowlist.go"), status: "fail", detail: []string{"allowedGitHubHosts"}},
		{name: "a module that concatenates the raw apiBase into a URL fails even when it adopts ghapi",
			dir: githubBlock("github.go", "github-raw-apibase-concatenated.go"), status: "fail", detail: []string{"apiBase"}},
		{name: "a whole-line comment that describes apiBase concatenation is not a violation",
			dir: githubBlock("github.go", "github-raw-apibase-in-a-comment.go"), status: "pass", detail: []string{"ghapi"}},
	})
}

// handlerImportCases reads the shared set in ../kitbinding/testdata/handler-import, whose comment opens with the verdict.
func handlerImportCases(t *testing.T) []suiteCase {
	t.Helper()
	paths, err := filepath.Glob("../kitbinding/testdata/handler-import/*.txtar")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures under ../kitbinding/testdata/handler-import (%v): the shared handler-import set is missing", err)
	}
	var cases []suiteCase
	for _, p := range paths {
		comment, files := readTxtar(t, p)
		var verdict, names string
		for _, line := range strings.Split(comment, "\n") {
			if v, ok := strings.CutPrefix(line, "verdict: "); ok && verdict == "" {
				verdict = v
			}
			if v, ok := strings.CutPrefix(line, "names: "); ok && names == "" {
				names = v
			}
		}
		opts := []option{without("main.go")}
		for rel, content := range files {
			opts = append(opts, with(filepath.FromSlash(rel), content))
		}
		c := suiteCase{name: "shared fixture " + strings.TrimSuffix(filepath.Base(p), ".txtar"), dir: kitV1Block(opts...), status: "fail"}
		switch verdict {
		case "pass":
			c.status, c.detail = "pass", []string{names}
		case "absent":
			c.detail, c.not = []string{kitHandlerImport}, []string{scanFailureMarker}
		case "tooling":
			c.detail = []string{scanFailureMarker, names}
		default:
			t.Fatalf("fixture %s: unknown verdict %q", p, verdict)
		}
		cases = append(cases, c)
	}
	return cases
}

func lockDir(name string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		locked := filepath.Join(dir, name)
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
		if _, err := os.ReadDir(locked); err == nil {
			t.Fatalf("setup: %s is still readable after chmod 0 (running as root?), so the fault never reaches the scan", locked)
		}
	}
}

func TestKitV1HandlerImport(t *testing.T) {
	cases := append(handlerImportCases(t),
		suiteCase{name: "no non-test file importing the kit handler fails, naming the import",
			dir: kitV1Block(withSource("main.go", "main-without-handler.go")), status: "fail", detail: []string{kitHandlerImport}},
		suiteCase{name: "a handler import that sits only in a test file fails, naming the import",
			dir:    kitV1Block(withSource("main.go", "main-without-handler.go"), withSource("handler_wiring_test.go", "handler-imported-only-in-a-test.go")),
			status: "fail", detail: []string{kitHandlerImport}},
		suiteCase{name: "a directory the scan cannot enter fails as a tooling failure beside a readable importing file",
			dir:    then(kitV1Block(with(filepath.Join("internal", "wire", "wire.go"), "package wire\n")), lockDir("internal")),
			status: "fail", detail: []string{scanFailureMarker}},
	)

	runSuites(t, []string{"trust-plumbing", "contract"}, cases)
}

func TestContractSuite(t *testing.T) {
	runSuites(t, []string{"contract"}, []suiteCase{
		{name: "a kit/v1 block passes defining no TestContract_ function, naming the kit/v1 rule", dir: kitV1Block(),
			status: "pass", detail: []string{"kit/v1"}},
		{name: "a conformant legacy block passes", dir: noContractBlock(), status: "pass"},
		{name: "a legacy block missing a TestContract_ function fails, naming it", dir: noContractBlock(without("contract_response_test.go")),
			status: "fail", detail: []string{"TestContract_EmitsConformingResponse"}},
	})
}

func TestSecretSafetySuite(t *testing.T) {
	runSuites(t, []string{"secret-safety"}, []suiteCase{
		{name: "a conformant legacy block passes", dir: noContractBlock(), status: "pass"},
		{name: "a legacy block with no TestNoSecretLeak fails, naming it", dir: noContractBlock(without("secrets_leak_test.go")),
			status: "fail", detail: []string{"TestNoSecretLeak"}},
		{name: "a secret-leak test that builds kittest.FakeResource fails, naming its file",
			dir: legacyBlock(withSource("secrets_fake_test.go", "leak-test-over-the-kit-fake.go")), status: "fail", detail: []string{"secrets_fake_test.go builds the kit's FakeResource"}},
		{name: "a secret-leak test that builds the fake through an aliased kittest import fails",
			dir: legacyBlock(withSource("secrets_fake_test.go", "leak-test-over-an-aliased-kit-fake.go")), status: "fail", detail: []string{"secrets_fake_test.go builds the kit's FakeResource"}},
		{name: "a secret-leak test that names FakeResource only in a comment passes",
			dir: legacyBlock(appendTo("secrets_leak_test.go", "\n// The leak test never builds kittest.FakeResource, so it judges this block.\n")), status: "pass", detail: []string{"TestNoSecretLeak"}},
		{name: "a test file that declares no mandatory test may build the fake",
			dir: legacyBlock(withSource("fake_test.go", "kit-fake-outside-a-mandatory-test.go")), status: "pass", detail: []string{"TestNoSecretLeak"}},
	})
}

func TestTeardownSuite(t *testing.T) {
	runSuites(t, []string{"teardown"}, []suiteCase{
		{name: "a teardown that asks the provider again through the kit, proven by a passing test, passes", dir: noContractBlock(),
			status: "pass", detail: []string{"TestUnsubscribeVerifies"}},
		{name: "a legacy block with no teardown test fails, naming TestUnsubscribeDeletes", dir: noContractBlock(without("teardown_test.go")),
			status: "fail", detail: []string{"TestUnsubscribeDeletes"}},
		{name: "a teardown that reports success without calling Survivor, Verify or Absent fails",
			dir: noContractBlock(withSource("verify.go", "survived-unverified.go")), status: "fail", detail: []string{"Survivor, Verify or Absent"}},
		{name: "a verification call that lives only in a test file does not count",
			dir:    noContractBlock(withSource("verify.go", "survived-unverified.go"), withSource("verify_reachable_test.go", "verify-only-in-a-test.go")),
			status: "fail", detail: []string{"Survivor, Verify or Absent"}},
		{name: "a verification call inside a comma-ok type assertion block fails, naming its file and line",
			dir: noContractBlock(withSource("verify.go", "verify-gated-by-comma-ok.go")), status: "fail", detail: []string{"verify.go:14"}},
		{name: "a comma-ok block that closes before the verification call does not gate it",
			dir: noContractBlock(withSource("verify.go", "verify-after-a-closed-comma-ok.go")), status: "pass", detail: []string{"TestUnsubscribeVerifies"}},
		{name: "a module with no TestUnsubscribeVerifies test fails, naming it",
			dir: noContractBlock(without("teardown_verify_test.go")), status: "fail", detail: []string{"TestUnsubscribeVerifies"}},
		{name: "a failing TestUnsubscribeVerifies test fails the suite",
			dir: noContractBlock(withSource("teardown_verify_test.go", "teardown-verify-test-fails.go")), status: "fail", detail: []string{"FAIL"}},
		{name: "a kit/v1 block is not held to the legacy rule, because the kit owns its teardown verification",
			dir: kitV1Block(withSource("teardown_test.go", "teardown-deletes-test-empty.go")), status: "pass", detail: []string{"kit/v1"}},
		{name: "a teardown test that builds kittest.FakeResource fails, naming its file",
			dir: legacyBlock(withSource("teardown_fake_test.go", "teardown-test-over-the-kit-fake.go")), status: "fail", detail: []string{"teardown_fake_test.go builds the kit's FakeResource"}},
		{name: "a kit/v1 block's teardown test is held to the kit-fake rule too",
			dir: kitV1Block(withSource("teardown_test.go", "teardown-test-over-the-kit-fake.go")), status: "fail", detail: []string{"teardown_test.go builds the kit's FakeResource"}},
	})
}

func TestDeliverableSuite(t *testing.T) {
	runSuites(t, []string{"deliverable"}, []suiteCase{
		{name: "a conformant legacy block passes", dir: noContractBlock(), status: "pass"},
		{name: "a legacy block with no Dockerfile fails, naming it", dir: noContractBlock(without("Dockerfile")),
			status: "fail", detail: []string{"Dockerfile"}},
	})
}

// resourcesBlock drives the fixture's assertBlock with misbehave and container as Go literals.
func resourcesBlock(manifestSource, misbehave, container string) dirFunc {
	return moduleBlock(
		withSource(manifestFile, manifestSource),
		withSource("block_test.go", "resources-block_test.go"),
		func(t *testing.T, files map[string]string) {
			raw, err := os.ReadFile(filepath.Join(kitModuleDir(t), "contract", "testdata", "type-snapshot.json"))
			if err != nil {
				t.Fatalf("reading the platform's type snapshot fixture: %v", err)
			}
			files["type-snapshot.json"] = string(raw)
		},
		with("resources_test.go", "package main\n\nimport \"testing\"\n\nfunc TestDeclaredResources(t *testing.T) {\n\tassertBlock(t, misbehave{"+misbehave+"}, "+container+")\n}\n"),
	)
}

func TestResourcesSuite(t *testing.T) {
	twoResources := withSource(manifestFile, "resources-two-resources-manifest.yaml")
	runSuites(t, []string{"resources"}, []suiteCase{
		{name: "a manifest that is not mitstack/v1beta is skipped", dir: manifestOnly(manifestHead),
			status: "skipped", detail: []string{"mitstack/v1beta"}},
		{name: "a block that defines no TestDeclaredResources fails, naming it",
			dir:    moduleBlock(twoResources, with("resources_test.go", "package main\n\nimport \"testing\"\n\nfunc TestSomethingElse(t *testing.T) {}\n")),
			status: "fail", detail: []string{"TestDeclaredResources"}},
		{name: "a TestDeclaredResources that never calls kittest.AssertBlock fails, naming it",
			dir:    moduleBlock(twoResources, with("resources_test.go", "package main\n\nimport \"testing\"\n\nfunc TestDeclaredResources(t *testing.T) {}\n")),
			status: "fail", detail: []string{"kittest.AssertBlock"}},
		{name: "a block-level block that holds every guard passes, naming the replacement guard it ran",
			dir: resourcesBlock("resources-block-manifest.yaml", "", "false"), status: "pass", detail: []string{"replace"}},
		{name: "an observe that still reports a resource the provider lost fails, naming it",
			dir: resourcesBlock("resources-block-manifest.yaml", `staleObserve: "aws.ec2-instance"`, "false"), status: "fail", detail: []string{`resource "aws.ec2-instance"`}},
		{name: "a tagged resource the manifest does not declare fails, naming it and its type",
			dir: resourcesBlock("resources-block-manifest.yaml", `extraTagged: true`, "false"), status: "fail", detail: []string{`resource "state-bucket"`, "aws.s3-bucket"}},
		{name: "a declared resource left present after unsubscribe fails, naming it",
			dir: resourcesBlock("resources-block-manifest.yaml", `leaveOnDelete: "aws.ebs-volume"`, "false"), status: "fail", detail: []string{`resource "aws.ebs-volume"`}},
		{name: "a validate hiding the replacement of a stateful resource fails, naming it",
			dir: resourcesBlock("resources-block-manifest.yaml", `hideReplacement: true`, "false"), status: "fail", detail: []string{`resource "aws.ebs-volume"`, "stateful"}},
		{name: "a provided resource missing an output its type requires fails, naming the output",
			dir: resourcesBlock("resources-block-manifest.yaml", `dropOutput: "hostId"`, "false"), status: "fail", detail: []string{`resource "aws.ec2-instance"`, "hostId"}},
		{name: "a block run once per listed type passes without claiming the replacement guard, which did not run",
			dir: resourcesBlock("resources-list-manifest.yaml", "", "true"), status: "pass", not: []string{"replace"}},
		{name: "a listed type whose run fails is named beside the resource",
			dir: resourcesBlock("resources-list-manifest.yaml", `failOnHostType: "hetzner.server"`, "true"), status: "fail", detail: []string{`resource "app-container"`},
			check: func(t *testing.T, got SuiteResult) {
				if !strings.Contains(strings.ReplaceAll(got.Detail, "hetzner.server-container", ""), "hetzner.server") {
					t.Errorf("resources detail = %q, want it to name hetzner.server, the requirement type whose run failed", got.Detail)
				}
			}},
	})
}
