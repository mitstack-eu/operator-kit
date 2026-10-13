package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

const manifestHead = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: reads-consumer
  name: Reads Consumer
  owner: blockverify-tests
spec:
  layer: backend
  version: 1.0.0
  description: A block for the manifest suite.
`

func manifestDir(t *testing.T, content string) string {
	t.Helper()
	return newFixtureFrom(t, map[string]string{"manifest.yaml": content})
}

func baseManifestFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "base-manifest.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func withVersion(v string) string {
	return strings.Replace(manifestHead, "version: 1.0.0", "version: "+v, 1)
}

func requireManifestPass(t *testing.T, got SuiteResult) {
	t.Helper()
	if got.Name != "manifest" {
		t.Errorf("suite name = %q, want %q", got.Name, "manifest")
	}
	if got.Status != "pass" {
		t.Errorf("manifest status = %q, want %q; detail: %s", got.Status, "pass", got.Detail)
	}
}

func requireManifestFail(t *testing.T, got SuiteResult, wants ...string) {
	t.Helper()
	if got.Name != "manifest" {
		t.Errorf("suite name = %q, want %q", got.Name, "manifest")
	}
	if got.Status != "fail" {
		t.Errorf("manifest status = %q, want %q; detail: %s", got.Status, "fail", got.Detail)
		return
	}
	for _, want := range wants {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("manifest detail = %q, want it to contain %q", got.Detail, want)
		}
	}
}

func TestManifestSuite_NoManifestIsSkipped(t *testing.T) {
	got := manifestSuite(t.TempDir(), "")
	if got.Name != "manifest" {
		t.Errorf("suite name = %q, want %q", got.Name, "manifest")
	}
	if got.Status != "skipped" {
		t.Errorf("manifest status = %q, want %q with no manifest.yaml (trust-plumbing fails that); detail: %s",
			got.Status, "skipped", got.Detail)
	}
	if got.Detail == "" {
		t.Error("a skipped manifest suite must say why in detail")
	}
}

func TestManifestSuite_ABlockThatDeclaresNoReadsPasses(t *testing.T) {
	cases := map[string]string{
		"the golden fixture's requires: []": manifestYAML,
		"no requires at all":                manifestHead,
		"requirements with no reads": manifestHead + `  requires:
    - capability: database
      hint: a postgres database
    - capability: no-registry-entry-lists-this
`,
	}
	for label, content := range cases {
		t.Run(label, func(t *testing.T) {
			requireManifestPass(t, manifestSuite(manifestDir(t, content), ""))
		})
	}
}

func TestManifestSuite_ReadsTheRegistryGivesTheirCapabilityPass(t *testing.T) {
	cases := map[string]string{
		"flow list": `  requires:
    - capability: database
      reads: [appName, dbName]
`,
		"block list naming an alias": `  requires:
    - capability: app-host
      reads:
        - region
`,
		"reads before capability": `  requires:
    - reads: [host, port]
      capability: database
`,
		"quoted names and comments": `  requires:
    # the database this block connects to
    - capability: database # postgres
      reads: ["dbName", 'username'] # both from the database
`,
		"a name two capabilities give": `  requires:
    - capability: aws-topology
      reads:
        - appHostname
`,
		"one requirement with reads beside one without": `  requires:
    - capability: github
    - capability: llm
      reads: [llmEndpoint, model]
    - capability: database
`,
	}
	for label, spec := range cases {
		t.Run(label, func(t *testing.T) {
			requireManifestPass(t, manifestSuite(manifestDir(t, manifestHead+spec), ""))
		})
	}
}

func TestManifestSuite_AReadTheRegistryDoesNotHoldFails(t *testing.T) {
	dir := manifestDir(t, manifestHead+`  requires:
    - capability: database
      reads: [appName, dbNme]
`)
	requireManifestFail(t, manifestSuite(dir, ""),
		`spec.requires[database].reads: "dbNme" is not in the resource registry`,
		`"dbName"`)
}

func TestManifestSuite_AReadItsCapabilityDoesNotGiveFails(t *testing.T) {
	cases := []struct {
		label, spec, want string
	}{
		{
			label: "database reads appHostname",
			spec: `  requires:
    - capability: database
      reads: [appHostname]
`,
			want: `spec.requires[database].reads: "appHostname" is not an output of capability "database"`,
		},
		{
			label: "database reads an alias of a host entry",
			spec: `  requires:
    - capability: database
      reads:
        - instanceType
`,
			want: `spec.requires[database].reads: "instanceType" is not an output of capability "database"`,
		},
		{
			label: "the offending requirement follows a valid one",
			spec: `  requires:
    - capability: app-host
      reads: [appHostname]
    - capability: llm
      reads: [dbName]
`,
			want: `spec.requires[llm].reads: "dbName" is not an output of capability "llm"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			requireManifestFail(t, manifestSuite(manifestDir(t, manifestHead+tc.spec), ""), tc.want)
		})
	}
}

func TestManifestSuite_AnEmptyReadNameFails(t *testing.T) {
	dir := manifestDir(t, manifestHead+`  requires:
    - capability: database
      reads: [appName, ""]
`)
	requireManifestFail(t, manifestSuite(dir, ""), `spec.requires[database].reads: a name must not be empty`)
}

func TestManifestSuite_OutputsTheRegistryHoldsPass(t *testing.T) {
	cases := map[string]string{
		"block list with dotted names and an alias": `  outputs:
    - dbName
    - host.type
    - region
`,
		"flow list": `  outputs: [host, port, username]
`,
		"empty flow list": `  outputs: []
`,
	}
	for label, spec := range cases {
		t.Run(label, func(t *testing.T) {
			requireManifestPass(t, manifestSuite(manifestDir(t, manifestHead+spec), ""))
		})
	}
}

// The nearest name is the smallest edit distance over entry names and aliases, ties broken alphabetically.
func TestManifestSuite_AnOutputOutsideTheRegistryFailsNamingTheNearestName(t *testing.T) {
	cases := []struct {
		label, spec, output, nearest, notNearest string
	}{
		{"block list, tie broken alphabetically", "  outputs:\n    - url\n    - hostnme\n", "hostnme", "host", "hostId"},
		{"flow list, tie broken alphabetically", "  outputs: [runsOnP]\n", "runsOnP", "runsOn", "runsOnPr"},
		{"nearest is an alias", "  outputs:\n    - regon\n", "regon", "region", "repo"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got := manifestSuite(manifestDir(t, manifestHead+tc.spec), "")
			requireManifestFail(t, got, "spec.outputs", `"`+tc.output+`"`, `"`+tc.nearest+`"`)
			if strings.Contains(got.Detail, `"`+tc.notNearest+`"`) {
				t.Errorf("manifest detail = %q names %q, want only the nearest name %q", got.Detail, tc.notNearest, tc.nearest)
			}
		})
	}
}

func TestManifestSuite_UnchangedVersionPassesWithoutAReleaseClassOrNote(t *testing.T) {
	base := baseManifestFile(t, withVersion("1.0.0"))
	requireManifestPass(t, manifestSuite(manifestDir(t, withVersion("1.0.0")), base))
}

func TestManifestSuite_ChangedVersionWithAReleaseClassAndNotePasses(t *testing.T) {
	cases := map[string]string{
		"plain note": `  releaseClass: feature
  releaseNote: Adds the reads declaration.
`,
		"folded note": `  releaseClass: correctness-fix
  releaseNote: >
    Fixes the retry that dropped the second
    attempt's outputs.
  requires: []
`,
		"literal note": `  releaseNote: |
    Renames the output.
  releaseClass: breaking
`,
	}
	base := baseManifestFile(t, withVersion("1.0.0"))
	for label, spec := range cases {
		t.Run(label, func(t *testing.T) {
			requireManifestPass(t, manifestSuite(manifestDir(t, withVersion("1.1.0")+spec), base))
		})
	}
}

func TestManifestSuite_ChangedVersionWithNoReleaseClassFails(t *testing.T) {
	base := baseManifestFile(t, withVersion("1.0.0"))
	target := withVersion("1.1.0") + "  releaseNote: Adds the reads declaration.\n"
	requireManifestFail(t, manifestSuite(manifestDir(t, target), base), "spec.releaseClass")
}

func TestManifestSuite_ChangedVersionWithNoReleaseNoteFails(t *testing.T) {
	cases := map[string]string{
		"no note":           "  releaseClass: feature\n",
		"blank note":        "  releaseClass: feature\n  releaseNote: \"   \"\n",
		"empty folded note": "  releaseClass: feature\n  releaseNote: >\n  requires: []\n",
	}
	base := baseManifestFile(t, withVersion("1.0.0"))
	for label, spec := range cases {
		t.Run(label, func(t *testing.T) {
			requireManifestFail(t, manifestSuite(manifestDir(t, withVersion("1.1.0")+spec), base), "spec.releaseNote")
		})
	}
}

func TestManifestSuite_ChangedVersionWithNeitherNamesBoth(t *testing.T) {
	base := baseManifestFile(t, withVersion("1.0.0"))
	requireManifestFail(t, manifestSuite(manifestDir(t, withVersion("2.0.0")), base),
		"spec.releaseClass", "spec.releaseNote")
}

func TestManifestSuite_BaseManifestAbsentPasses(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "base-manifest.yaml")
	requireManifestPass(t, manifestSuite(manifestDir(t, withVersion("1.1.0")), missing))
}

func TestManifestSuite_NoBaseManifestFlagComparesNoVersion(t *testing.T) {
	requireManifestPass(t, manifestSuite(manifestDir(t, withVersion("1.1.0")), ""))
}

func TestManifestSuite_UnreadableBaseManifestFailsNamingIt(t *testing.T) {
	base := filepath.Join(t.TempDir(), "base-manifest.yaml")
	if err := os.Mkdir(base, 0o755); err != nil {
		t.Fatal(err)
	}
	requireManifestFail(t, manifestSuite(manifestDir(t, withVersion("1.1.0")), base), base)
}

func withID(idLine string) string {
	return strings.Replace(manifestHead, "  id: reads-consumer\n", "  id: "+idLine+"\n", 1)
}

func TestManifestSuite_ACommunityOrPrivateMetadataIDPasses(t *testing.T) {
	cases := map[string]string{
		"bare slug":                 "billing-db",
		"double-quoted private id":  `"@acme/billing-db"`,
		"single-quoted private id":  `'@acme/billing-db'`,
		"private id with a comment": `"@org-a/api" # the org's own api block`,
		"forty-character bare slug": "a" + strings.Repeat("b", 38) + "c",
	}
	for label, idLine := range cases {
		t.Run(label, func(t *testing.T) {
			requireManifestPass(t, manifestSuite(manifestDir(t, withID(idLine)), ""))
		})
	}
}

func TestManifestSuite_AMetadataIDOutsideBothFormsFailsNamingIt(t *testing.T) {
	cases := map[string]string{
		"uppercase namespace":     `"@Acme/x"`,
		"namespace with no name":  `"@acme/"`,
		"namespace without its @": "acme/x",
		"two slashes":             `"@acme/x/y"`,
		"uppercase bare id":       "Billing-DB",
		"trailing dash":           "billing-",
		"41-character bare id":    "a" + strings.Repeat("b", 39) + "c",
	}
	for label, idLine := range cases {
		t.Run(label, func(t *testing.T) {
			requireManifestFail(t, manifestSuite(manifestDir(t, withID(idLine)), ""),
				"manifest.yaml", "metadata.id", "@<org>/")
		})
	}
}

// YAML reserves @ at the start of a plain value, so the platform's parser refuses an unquoted private id.
func TestManifestSuite_AnUnquotedPrivateIDFailsAskingForQuotes(t *testing.T) {
	for label, idLine := range map[string]string{
		"unquoted private id":          "@acme/billing-db",
		"unquoted private id, comment": "@acme/billing-db # the billing database",
		"backtick-led id":              "`billing-db`",
	} {
		t.Run(label, func(t *testing.T) {
			requireManifestFail(t, manifestSuite(manifestDir(t, withID(idLine)), ""),
				"manifest.yaml", "metadata.id", "quote")
		})
	}
}

func TestManifestSuite_ABadMetadataIDIsReportedBesideASpecProblem(t *testing.T) {
	content := withID(`"@Acme/x"`) + "  outputs: [hostnme]\n"
	requireManifestFail(t, manifestSuite(manifestDir(t, content), ""),
		"metadata.id", "@<org>/", `spec.outputs: "hostnme"`)
}

func TestManifestSuite_NoMetadataIDKeepsTheSpecVerdict(t *testing.T) {
	noID := strings.Replace(manifestHead, "  id: reads-consumer\n", "", 1)
	noMetadata := strings.Replace(manifestHead,
		"metadata:\n  id: reads-consumer\n  name: Reads Consumer\n  owner: blockverify-tests\n", "", 1)
	if noID == manifestHead || noMetadata == manifestHead {
		t.Fatal("manifestHead changed shape; the metadata.id fixtures no longer remove the id")
	}

	for label, content := range map[string]string{
		"metadata without an id": noID,
		"no metadata at all":     noMetadata,
	} {
		t.Run(label+" passes with a valid spec", func(t *testing.T) {
			requireManifestPass(t, manifestSuite(manifestDir(t, content), ""))
		})
		t.Run(label+" fails with a bad spec", func(t *testing.T) {
			got := manifestSuite(manifestDir(t, content+"  outputs: [hostnme]\n"), "")
			requireManifestFail(t, got, `spec.outputs: "hostnme"`)
			if strings.Contains(got.Detail, "metadata.id") {
				t.Errorf("manifest detail = %q names metadata.id, want no id problem when the id is absent", got.Detail)
			}
		})
	}
}

func TestManifestSuite_AMetadataTheReaderCannotFollowIsAFailure(t *testing.T) {
	head := "apiVersion: mitstack/v1alpha\nkind: Block\n"
	spec := "spec:\n  layer: backend\n  version: 1.0.0\n"
	cases := map[string]string{
		"metadata is a flow mapping": head + "metadata: {id: \"@Acme/x\", owner: blockverify-tests}\n" + spec,
		"metadata.id is an alias":    head + "metadata:\n  name: &bad \"@Acme/x\"\n  id: *bad\n" + spec,
		"metadata.id is a mapping":   head + "metadata:\n  id:\n    org: Acme\n" + spec,
	}
	for label, content := range cases {
		t.Run(label, func(t *testing.T) {
			requireManifestFail(t, manifestSuite(manifestDir(t, content), ""), "manifest.yaml", "metadata")
		})
	}
}

const typeBlockSpellingManifest = `apiVersion: mitstack/v1alpha
type: block
metadata:
  id: blockverify-fixture
  name: blockverify fixture block
  owner: blockverify-tests
spec:
  version: 1.0.0
  description: >
    The blockverify fixture block, written with type, format and store.
  provides:
    - blockverify-fixture-capability
  providedContracts:
    - capability: blockverify-fixture-capability
      format: config
  continuity:
    - name: fixture-state
      store: volume
      stateful: false
  requires:
    - capability: database
      reads: [host, port, dbName]
  outputs:
    - url
    - region
`

const kindSpellingManifest = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: blockverify-fixture
  name: blockverify fixture block
  owner: blockverify-tests
spec:
  layer: backend
  version: 1.0.0
  description: >
    The blockverify fixture block, written with kind in all three places.
  provides:
    - blockverify-fixture-capability
  providedContracts:
    - capability: blockverify-fixture-capability
      kind: config
  continuity:
    - name: fixture-state
      kind: volume
      stateful: false
  requires:
    - capability: database
      reads: [host, port, dbName]
  outputs:
    - url
    - region
`

func TestManifestSuite_BothManifestSpellingsPass(t *testing.T) {
	for label, content := range map[string]string{
		"type, format and store": typeBlockSpellingManifest,
		"kind in all three":      kindSpellingManifest,
	} {
		t.Run(label, func(t *testing.T) {
			requireManifestPass(t, manifestSuite(manifestDir(t, content), ""))
		})
	}
}

var unknownSpecFields = map[string]string{
	"spec.frobnicate":    "  frobnicate: true\n",
	"spec.futureSection": "  futureSection:\n    outputs: not-the-outputs\n    reads: [not, the, reads]\n",
	"spec.requires[0].frobnicate": "  requires:\n    - capability: database\n      reads: [host]\n" +
		"      frobnicate: true\n",
	"spec.lifecycle.frobnicate": "  lifecycle:\n    contract: kit/v1\n    frobnicate: true\n",
}

func TestManifestSuite_AnUnknownSpecFieldFailsNamingIt(t *testing.T) {
	for path, spec := range unknownSpecFields {
		t.Run(path, func(t *testing.T) {
			content := manifestHead + spec
			_, perr := contract.ParseManifest([]byte(content))
			if perr == nil {
				t.Fatalf("contract.ParseManifest accepts the manifest, which must be refused naming %s:\n%s", path, content)
			}
			requireManifestFail(t, manifestSuite(manifestDir(t, content), ""), "manifest.yaml", path, perr.Error())
		})
	}
}

func TestManifestSuite_PassesAnOldFormManifestWithDependsOnAndProvidedSlugs(t *testing.T) {
	spec := `  dependsOn:
    - weather-app
  provides:
    - runner
  requires:
    - capability: repo
      reads: [repo]
    - capability: app-host
  outputs:
    - repo
`
	requireManifestPass(t, manifestSuite(manifestDir(t, manifestHead+spec), ""))
}

func TestManifestSuite_PassesAManifestWithDependsOnAndPrintsItsWarning(t *testing.T) {
	spec := `  dependsOn:
    - weather-app
  requires:
    - capability: repo
      reads: [repo]
`
	got := manifestSuite(manifestDir(t, manifestHead+spec), "")
	requireManifestPass(t, got)
	if !strings.Contains(got.Detail, "spec.dependsOn") {
		t.Errorf("manifest detail = %q, want it to print the warning naming %q", got.Detail, "spec.dependsOn")
	}
}

func TestManifestSuite_AManifestTheReaderCannotFollowIsAFailure(t *testing.T) {
	cases := map[string]string{
		"spec is a flow mapping":          "apiVersion: mitstack/v1alpha\nkind: Block\nspec: {version: 1.0.0, outputs: [nope]}\n",
		"spec.outputs is a flow mapping":  manifestHead + "  outputs: {first: nope}\n",
		"spec.outputs is an alias":        manifestHead + "  tags: &outs [nope]\n  outputs: *outs\n",
		"spec.requires is a flow mapping": manifestHead + "  requires: {capability: database, reads: [nope]}\n",
		"spec.requires is an alias":       manifestHead + "  resources: &reqs\n    - name: database\n  requires: *reqs\n",
	}
	for label, content := range cases {
		t.Run(label, func(t *testing.T) {
			requireManifestFail(t, manifestSuite(manifestDir(t, content), ""), "manifest.yaml")
		})
	}

	t.Run("manifest.yaml is a directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "manifest.yaml"), 0o755); err != nil {
			t.Fatal(err)
		}
		requireManifestFail(t, manifestSuite(dir, ""), "manifest.yaml")
	})
}

func TestRun_BothManifestSpellingsPassBlockverify(t *testing.T) {
	for label, content := range map[string]string{
		"type, format and store": typeBlockSpellingManifest,
		"kind in all three":      kindSpellingManifest,
	} {
		t.Run(label, func(t *testing.T) {
			files := goldenFixtureFiles(t)
			files["manifest.yaml"] = content
			stdout, exit := runBlockverify(t, "--json", "--standalone", newFixtureFrom(t, files))
			r := decodeReport(t, stdout)
			if exit != 0 || r.Verdict != "pass" {
				t.Errorf("exit %d, verdict %q; want 0 and pass\nstdout:\n%s", exit, r.Verdict, stdout)
			}
			if s := suiteByName(t, r, "manifest"); s.Status != "pass" {
				t.Errorf("manifest status = %q, want %q; detail: %s", s.Status, "pass", s.Detail)
			}
		})
	}
}

func TestRun_AnUnknownSpecFieldFailsBlockverifyNamingIt(t *testing.T) {
	const field = "spec.frobnicate"
	content := manifestYAML + unknownSpecFields[field]
	_, perr := contract.ParseManifest([]byte(content))
	if perr == nil {
		t.Fatalf("contract.ParseManifest accepts the manifest, which must be refused naming %s:\n%s", field, content)
	}
	files := goldenFixtureFiles(t)
	files["manifest.yaml"] = content
	dir := newFixtureFrom(t, files)

	stdout, exit := runBlockverify(t, "--json", "--standalone", dir)
	r := decodeReport(t, stdout)
	if exit != 1 || r.Verdict != "fail" {
		t.Errorf("exit %d, verdict %q; want 1 and fail for a manifest carrying %s\nstdout:\n%s", exit, r.Verdict, field, stdout)
	}
	for _, name := range []string{"trust-plumbing", "contract", "manifest", "naming"} {
		s := suiteByName(t, r, name)
		if s.Status != "fail" {
			t.Errorf("%s status = %q, want %q: a manifest the parser refuses fails every suite that reads it; detail: %s",
				name, s.Status, "fail", s.Detail)
			continue
		}
		for _, want := range []string{field, perr.Error()} {
			if !strings.Contains(s.Detail, want) {
				t.Errorf("%s detail = %q, want it to contain %q", name, s.Detail, want)
			}
		}
	}

	human, exit := runBlockverify(t, "--standalone", dir)
	if exit != 1 {
		t.Errorf("exit = %d without --json, want 1\nstdout:\n%s", exit, human)
	}
	if !strings.Contains(human, field) {
		t.Errorf("the human-readable report does not name %s:\n%s", field, human)
	}
}

func TestRun_MetadataIDIsJudgedByTheManifestSuite(t *testing.T) {
	cases := []struct {
		label, idLine, status string
		exit                  int
	}{
		{"private id passes", `"@acme/billing-db"`, "pass", 0},
		{"uppercase namespace fails", `"@Acme/x"`, "fail", 1},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			files := goldenFixtureFiles(t)
			content := strings.Replace(manifestYAML, "  id: blockverify-fixture\n", "  id: "+tc.idLine+"\n", 1)
			if content == manifestYAML {
				t.Fatal("manifestYAML changed shape; the fixture no longer carries id: blockverify-fixture")
			}
			files["manifest.yaml"] = content
			stdout, exit := runBlockverify(t, "--json", "--standalone", newFixtureFrom(t, files))
			if exit != tc.exit {
				t.Errorf("exit = %d, want %d\nstdout:\n%s", exit, tc.exit, stdout)
			}
			r := decodeReport(t, stdout)
			if s := suiteByName(t, r, "manifest"); s.Status != tc.status {
				t.Errorf("manifest status = %q, want %q; detail: %s", s.Status, tc.status, s.Detail)
			} else if tc.status == "fail" && !strings.Contains(s.Detail, "metadata.id") {
				t.Errorf("manifest detail = %q, want it to name metadata.id", s.Detail)
			}
		})
	}
}

func TestRun_BaseManifestFlagNamesTheBaseAndNotTheTarget(t *testing.T) {
	base := baseManifestFile(t, strings.Replace(manifestYAML, "version: 1.0.0", "version: 0.9.0", 1))
	dir := newGoldenFixture(t)

	for label, args := range map[string][]string{
		"flag after the target":  {"--json", "--standalone", dir, "--base-manifest", base},
		"flag before the target": {"--base-manifest", base, "--json", "--standalone", dir},
	} {
		t.Run(label, func(t *testing.T) {
			stdout, exit := runBlockverify(t, args...)
			if exit != 1 {
				t.Errorf("exit = %d, want 1: the version moved from 0.9.0 with no release class\nstdout:\n%s", exit, stdout)
			}
			r := decodeReport(t, stdout)
			if tp := suiteByName(t, r, "trust-plumbing"); tp.Status != "pass" {
				t.Errorf("trust-plumbing status = %q, want %q: the base path must not be taken for the target; detail: %s",
					tp.Status, "pass", tp.Detail)
			}
			m := suiteByName(t, r, "manifest")
			if m.Status != "fail" || !strings.Contains(m.Detail, "spec.releaseClass") {
				t.Errorf("manifest = %q %q, want fail naming spec.releaseClass", m.Status, m.Detail)
			}
		})
	}
}

func TestRun_BaseManifestFlagWithNoValueIsAUsageError(t *testing.T) {
	dir := newGoldenFixture(t)
	stdout, exit := runBlockverify(t, "--json", "--standalone", dir, "--base-manifest")
	if exit != 2 {
		t.Errorf("exit = %d, want 2 for --base-manifest with no path\nstdout:\n%s", exit, stdout)
	}
}
