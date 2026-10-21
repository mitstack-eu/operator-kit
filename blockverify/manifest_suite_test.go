package main

// The manifest suite and the contract rule read a manifest through contract.ParseManifest and judge what it declares.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
)

// manifestHead ends inside spec, so a test appends spec keys to it.
const manifestHead = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: manifest-fixture
  name: Manifest Fixture
  owner: blockverify-tests
spec:
  lifecycle:
    endpoint: https://fixture.example.test/hook
    operations: [subscribe, unsubscribe]
    contract: kit/v1
  resources: [aws.s3-bucket]
  description: A block for the manifest suite.
  version: 1.0.0
`

const oldVersionManifest = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: manifest-fixture
  name: Manifest Fixture
spec:
  version: 1.0.0
`

func withVersion(t *testing.T, v string) string {
	return replaced(t, manifestHead, "version: 1.0.0", "version: "+v)
}

func withID(t *testing.T, idLine string) string {
	return replaced(t, manifestHead, "  id: manifest-fixture\n", "  id: "+idLine+"\n")
}

func plainV1Beta(t *testing.T) string {
	t.Helper()
	raw, err := contract.ManifestGoldens.ReadFile(contract.ManifestGoldensDir + "/plain-v1beta.yaml")
	if err != nil {
		t.Fatalf("reading plain-v1beta.yaml from contract.ManifestGoldens: %v", err)
	}
	return string(raw)
}

func missingBase(t *testing.T) string { return filepath.Join(t.TempDir(), "base-manifest.yaml") }

func dirBase(t *testing.T) string {
	base := missingBase(t)
	mkdir(filepath.Base(base))(t, filepath.Dir(base))
	return base
}

func passes(name, content string) suiteCase {
	return suiteCase{name: name, dir: manifestOnly(content), status: "pass"}
}

func fails(name, content string, detail ...string) suiteCase {
	return suiteCase{name: name, dir: manifestOnly(content), status: "fail", detail: detail}
}

func TestManifestSuite(t *testing.T) {
	head, v1beta := manifestHead, plainV1Beta(t)
	from100 := baseManifest(withVersion(t, "1.0.0"))
	cases := []suiteCase{
		{name: "a directory with no manifest is skipped", dir: emptyDir, status: "skipped"},
		passes("a manifest that parses passes, naming its version", head),
		passes("a worked v1beta manifest passes", v1beta),
		fails("a manifest in the old version fails, naming the version the kit knows", oldVersionManifest,
			"manifest.yaml", `apiVersion "mitstack/v1alpha" is not a manifest version the kit knows; it must be mitstack/v1beta`),
		{name: "an unchanged version passes without a release class or note", dir: manifestOnly(withVersion(t, "1.0.0")), base: from100, status: "pass"},
		{name: "a changed version with a class and a plain note passes", base: from100, status: "pass",
			dir: manifestOnly(withVersion(t, "1.1.0") + "  releaseClass: feature\n  releaseNote: Adds the bucket.\n")},
		{name: "a changed version with a class and a folded note passes", base: from100, status: "pass",
			dir: manifestOnly(withVersion(t, "1.1.0") + "  releaseClass: correctness-fix\n  releaseNote: >\n    Fixes the retry that dropped the second\n    attempt's outputs.\n")},
		{name: "a changed version with a literal note before its class passes", base: from100, status: "pass",
			dir: manifestOnly(withVersion(t, "1.1.0") + "  releaseNote: |\n    Renames the output.\n  releaseClass: breaking\n")},
		{name: "a changed version with no release class fails, naming it", base: from100, status: "fail", detail: []string{"spec.releaseClass"},
			dir: manifestOnly(withVersion(t, "1.1.0") + "  releaseNote: Adds the bucket.\n")},
		{name: "a changed version with no release note fails, naming it", base: from100, status: "fail", detail: []string{"spec.releaseNote"},
			dir: manifestOnly(withVersion(t, "1.1.0") + "  releaseClass: feature\n")},
		{name: "a changed version with a blank release note fails, naming it", base: from100, status: "fail", detail: []string{"spec.releaseNote"},
			dir: manifestOnly(withVersion(t, "1.1.0") + "  releaseClass: feature\n  releaseNote: \"   \"\n")},
		{name: "a changed version with neither class nor note names both", dir: manifestOnly(withVersion(t, "2.0.0")), base: from100,
			status: "fail", detail: []string{"spec.releaseClass", "spec.releaseNote"}},
		{name: "a base manifest that does not exist marks the block new and passes", dir: manifestOnly(withVersion(t, "1.1.0")), base: missingBase, status: "pass"},
		passes("no --base-manifest compares no version", withVersion(t, "1.1.0")),
		{name: "an unreadable base manifest fails, naming it", dir: manifestOnly(withVersion(t, "1.1.0")), base: dirBase, status: "fail", detail: []string{"$BASE"}},
		{name: "a worked v1beta manifest passes against an older base", dir: manifestOnly(v1beta), base: baseManifest(replaced(t, v1beta, "version: 3.0.0", "version: 2.4.0")), status: "pass"},
		passes("a bare slug metadata.id passes", withID(t, "billing-db")),
		passes("a double-quoted private metadata.id passes", withID(t, `"@acme/billing-db"`)),
		passes("a single-quoted private metadata.id passes", withID(t, `'@acme/billing-db'`)),
		passes("a private metadata.id with a comment passes", withID(t, `"@org-a/api" # the org's own api block`)),
		passes("a forty-character bare slug passes", withID(t, "a"+strings.Repeat("b", 38)+"c")),
		fails("metadata as a flow mapping is a failure", replaced(t, head, "metadata:\n  id: manifest-fixture\n  name: Manifest Fixture\n  owner: blockverify-tests\n",
			"metadata: {id: \"@Acme/x\", name: Manifest Fixture}\n"), "manifest.yaml", "metadata"),
		fails("metadata.id as an alias is a failure", replaced(t, head, "  id: manifest-fixture\n  name: Manifest Fixture\n", "  name: &bad \"@Acme/x\"\n  id: *bad\n"),
			"manifest.yaml", "metadata"),
		fails("metadata.id as a mapping is a failure", withID(t, "\n    org: Acme"), "manifest.yaml", "metadata"),
		{name: "a manifest.yaml that is a directory is a failure", dir: then(emptyDir, mkdir(manifestFile)), status: "fail", detail: []string{"manifest.yaml"}},
		fails("a v1beta manifest carrying spec.outputs fails as the parser refuses it", v1beta+"  outputs: [host]\n", "manifest.yaml", "spec.outputs"),
		fails("an unknown apiVersion fails, naming the known version", replaced(t, head, "apiVersion: mitstack/v1beta", "apiVersion: mitstack/v2"),
			"manifest.yaml", "mitstack/v2", contract.ManifestAPIVersion),
		fails("no apiVersion fails, naming the known version", replaced(t, head, "apiVersion: mitstack/v1beta\n", ""),
			"manifest.yaml", contract.ManifestAPIVersion),
	}
	for _, idLine := range []string{`"@Acme/x"`, `"@acme/"`, "acme/x", `"@acme/x/y"`, "Billing-DB", "billing-", "a" + strings.Repeat("b", 39) + "c"} {
		cases = append(cases, fails("metadata.id "+idLine+" outside both forms fails, naming it", withID(t, idLine), "manifest.yaml", "metadata.id", "@<org>/"))
	}
	for _, idLine := range []string{"@acme/billing-db", "@acme/billing-db # the billing database", "`billing-db`"} {
		cases = append(cases, fails("unquoted metadata.id "+idLine+" fails, asking for quotes", withID(t, idLine), "manifest.yaml", "metadata.id", "quote"))
	}
	for path, spec := range map[string]string{
		"spec.frobnicate":           "  frobnicate: true\n",
		"spec.futureSection":        "  futureSection:\n    outputs: not-the-outputs\n",
		"spec.requires[0].optional": "  requires:\n    - type: aws.vpc\n      optional: true\n",
		"spec.dependsOn":            "  dependsOn:\n    - weather-app\n",
	} {
		cases = append(cases, fails("an unknown field "+path+" fails with the parser's message", head+spec, "manifest.yaml", path, refusal(t, head+spec, path)))
	}
	for label, content := range map[string]string{
		"a base under mitstack/v2":           replaced(t, withVersion(t, "1.0.0"), "apiVersion: mitstack/v1beta", "apiVersion: mitstack/v2"),
		"a base in the old version":          oldVersionManifest,
		"a base with an unquoted private id": withID(t, "@acme/billing-db"),
	} {
		_, parseErr := contract.ParseManifest([]byte(content))
		if parseErr == nil {
			t.Fatalf("the %s fixture parses, so it proves nothing:\n%s", label, content)
		}
		cases = append(cases,
			suiteCase{name: label + " leaves its version unknown, so a released change passes", base: baseManifest(content), status: "pass",
				dir: manifestOnly(withVersion(t, "1.0.0") + "  releaseClass: feature\n  releaseNote: Fixes the base manifest.\n")},
			suiteCase{name: label + " leaves its version unknown, so an unreleased change names the base, its error and both fields", base: baseManifest(content),
				dir: manifestOnly(withVersion(t, "1.0.0")), status: "fail", detail: []string{"$BASE", parseErr.Error(), "spec.releaseClass", "spec.releaseNote"}})
	}

	runSuites(t, []string{"manifest"}, cases)
}

func TestRequireKitV1Contract(t *testing.T) {
	cases := []struct {
		name        string
		dir         dirFunc
		wantRefusal []string
	}{
		{name: "the worked v1beta block declares kit/v1", dir: manifestOnly(plainV1Beta(t))},
		{name: "a flow-mapped lifecycle declaring kit/v1 passes", dir: manifestOnly(replaced(t, manifestHead,
			"  lifecycle:\n    endpoint: https://fixture.example.test/hook\n    operations: [subscribe, unsubscribe]\n    contract: kit/v1\n",
			"  lifecycle: {endpoint: https://fixture.example.test/hook, operations: [subscribe, unsubscribe], contract: kit/v1}\n"))},
		{name: "another contract fails, naming it", dir: manifestOnly(replaced(t, manifestHead, "contract: kit/v1", "contract: kit/v2")),
			wantRefusal: []string{"manifest.yaml", `"kit/v2"`}},
		{name: "no manifest fails, naming manifest.yaml", dir: emptyDir, wantRefusal: []string{"manifest.yaml"}},
		{name: "a manifest in the old version fails with the parser's message", dir: manifestOnly(oldVersionManifest),
			wantRefusal: []string{"manifest.yaml", refusal(t, oldVersionManifest, "apiVersion")}},
	}
	for path, content := range map[string]string{
		"spec.frobnicate":           manifestHead + "  frobnicate: true\n",
		"spec.lifecycle.frobnicate": replaced(t, manifestHead, "    contract: kit/v1\n", "    contract: kit/v1\n    frobnicate: true\n"),
		"metadata.frobnicate":       replaced(t, manifestHead, "  owner: blockverify-tests\n", "  owner: blockverify-tests\n  frobnicate: true\n"),
		"spec.lifecycle.hooks":      replaced(t, manifestHead, "    contract: kit/v1\n", "    contract: kit/v1\n    hooks: [beforeEnsure]\n"),
	} {
		cases = append(cases, struct {
			name        string
			dir         dirFunc
			wantRefusal []string
		}{name: "a manifest the parser refuses at " + path + " fails with its message", dir: manifestOnly(content),
			wantRefusal: []string{"manifest.yaml", refusal(t, content, path)}})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := c.dir(t)

			detail, ok := requireKitV1Contract(dir)

			if c.wantRefusal == nil && !ok {
				t.Errorf("requireKitV1Contract(%s) = %q, false; want it to pass", dir, detail)
			}
			if c.wantRefusal != nil && ok {
				t.Errorf("requireKitV1Contract(%s) passed; want a failure carrying %q", dir, c.wantRefusal)
			}
			for _, want := range c.wantRefusal {
				if !strings.Contains(detail, want) {
					t.Errorf("requireKitV1Contract(%s) detail %q; want it to carry %q", dir, detail, want)
				}
			}
		})
	}
}
