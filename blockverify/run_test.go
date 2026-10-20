package main

// Run refuses anything but standalone mode, runs every suite and reports one verdict, exit code and digest pair.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// exitNotJudged marks a case that pins a suite's verdict without the run's exit code.
const exitNotJudged = -1

// standaloneInvocation is the command a block author runs from the block's own directory.
const standaloneInvocation = "blockverify --standalone ."

var jsonStandalone = []string{"--json", "--standalone", "$DIR"}

type suiteWant struct {
	status string
	detail []string
}

// runCase reads stdout as a JSON report when verdict is set; $DIR and $BASE in args and wants stand for the case's paths.
type runCase struct {
	name    string
	dir     dirFunc
	base    func(t *testing.T) string
	args    []string
	exit    int
	stdout  []string
	verdict string
	suites  map[string]suiteWant
	human   map[string]suiteWant
}

var humanStatus = map[string]string{"✓": "pass", "✗": "fail", "○": "skipped"}

// humanSuites reads each suite line of the human-readable report, a mark, the suite name and its detail.
func humanSuites(stdout string) map[string]reportSuite {
	out := map[string]reportSuite{}
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || humanStatus[fields[0]] == "" || !strings.HasPrefix(line, "  ") {
			continue
		}
		out[fields[1]] = reportSuite{Name: fields[1], Status: humanStatus[fields[0]], Detail: strings.Join(fields[2:], " ")}
	}
	return out
}

func both(w suiteWant) map[string]suiteWant {
	return map[string]suiteWant{"trust-plumbing": w, "contract": w}
}

func TestRun(t *testing.T) {
	legacy := legacyManifest(t)
	unknownField := legacy + "  frobnicate: true\n"
	unknownFieldErr := refusal(t, unknownField, "spec.frobnicate")
	everyReader := suiteWant{"fail", []string{"spec.frobnicate", unknownFieldErr}}
	capability := func(t *testing.T, files map[string]string) {
		const dir = "testdata/capability-without-issue-handler"
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			files[e.Name()] = string(raw)
		}
	}
	cases := []runCase{
		{name: "a block with no manifest.yaml fails trust-plumbing, exiting 1", dir: legacyBlock(without(manifestFile)), args: jsonStandalone, exit: 1, verdict: "fail",
			suites: map[string]suiteWant{"trust-plumbing": {"fail", []string{"manifest.yaml"}}}},
		{name: "a block with no Dockerfile fails deliverable, exiting 1", dir: legacyBlock(without("Dockerfile")), args: jsonStandalone, exit: 1, verdict: "fail",
			suites: map[string]suiteWant{"deliverable": {"fail", []string{"Dockerfile"}}}},
		{name: "a block that redeclares a kit-owned symbol fails trust-plumbing even though it does not compile", args: jsonStandalone, exit: 1, verdict: "fail",
			dir: legacyBlock(withSource("rogue.go", "redeclares-emit-progress.go")), suites: map[string]suiteWant{"trust-plumbing": {"fail", []string{"EmitProgress"}}}},
		// go test -run with no matching test exits 0, so only the presence check catches a missing TestContract_ function.
		{name: "a block with no TestContract_ decode tests fails contract", dir: legacyBlock(without("contract_conformance_test.go")), args: jsonStandalone, exit: 1, verdict: "fail",
			suites: map[string]suiteWant{"contract": {"fail", []string{"TestContract_"}}}},
		{name: "a block with no response contract test fails contract, naming it", dir: legacyBlock(without("contract_response_test.go")), args: jsonStandalone, exit: 1, verdict: "fail",
			suites: map[string]suiteWant{"contract": {"fail", []string{"TestContract_EmitsConformingResponse"}}}},
		{name: "a target with no --standalone flag is refused as repo mode with exit 2", dir: legacyBlock(), args: []string{"$DIR"}, exit: 2, stdout: []string{standaloneInvocation}},
		{name: "no arguments at all are refused with exit 2", dir: emptyDir, args: []string{}, exit: 2, stdout: []string{standaloneInvocation}},
		{name: "--standalone with no target is a usage error", dir: emptyDir, args: []string{"--json", "--standalone"}, exit: 2},
		{name: "--base-manifest with no path is a usage error", dir: legacyBlock(), args: []string{"--json", "--standalone", "$DIR", "--base-manifest"}, exit: 2},
		{name: "a capability that implements no issue handler passes", dir: kitV1Block(without("main.go"), capability), args: jsonStandalone, exit: 0, verdict: "pass"},
		{name: "a kit/v1 block that imports the handler names the kit/v1 rule in both suites", dir: kitV1Block(), args: jsonStandalone, exit: exitNotJudged, verdict: "fail",
			suites: both(suiteWant{"pass", []string{"kit/v1"}})},
		{name: "a kit/v1 block that does not import the handler names the kit/v1 rule in both suites", dir: kitV1Block(withSource("main.go", "main-without-handler.go")),
			args: jsonStandalone, exit: exitNotJudged, verdict: "fail", suites: both(suiteWant{"fail", []string{"kit/v1"}})},
		// Everything but the manifest passes the legacy rule, so a legacy verdict here would read as a pass.
		{name: "a manifest.yaml that cannot be opened is a tooling failure naming the target", dir: then(legacyBlock(without(manifestFile)), mkdir(manifestFile)),
			args: jsonStandalone, exit: 1, verdict: "fail", suites: both(suiteWant{"fail", []string{"$DIR", "manifest.yaml"}})},
		{name: "a dangling manifest.yaml symlink is a tooling failure naming the target", args: jsonStandalone, exit: 1, verdict: "fail",
			dir: then(legacyBlock(without(manifestFile)), func(t *testing.T, dir string) {
				if err := os.Symlink(filepath.Join(dir, "nowhere.yaml"), filepath.Join(dir, manifestFile)); err != nil {
					t.Fatal(err)
				}
			}), suites: both(suiteWant{"fail", []string{"$DIR", "manifest.yaml"}})},
		{name: "a manifest the parser refuses is a tooling failure naming the target", dir: legacyBlock(with(manifestFile, legacy+"  lifecycle:\n    contract: kit/v1\n    frobnicate: true\n")),
			args: jsonStandalone, exit: 1, verdict: "fail", suites: both(suiteWant{"fail", []string{"$DIR", "manifest.yaml"}})},
		{name: "a contract value with no rule fails both suites, naming it", dir: legacyBlock(with(manifestFile, legacy+"  lifecycle:\n    contract: kit/v2\n")),
			args: jsonStandalone, exit: 1, verdict: "fail", suites: both(suiteWant{"fail", []string{"kit/v2"}})},
		{name: "the type, format and store spelling passes", dir: legacyBlock(withSource(manifestFile, "type-block-spelling-manifest.yaml")), args: jsonStandalone, exit: 0, verdict: "pass",
			suites: map[string]suiteWant{"manifest": {status: "pass"}}},
		{name: "the kind-in-all-three spelling passes", dir: legacyBlock(withSource(manifestFile, "kind-spelling-manifest.yaml")), args: jsonStandalone, exit: 0, verdict: "pass",
			suites: map[string]suiteWant{"manifest": {status: "pass"}}},
		{name: "an unknown spec field fails every suite that reads the manifest, naming it", dir: legacyBlock(with(manifestFile, unknownField)), args: jsonStandalone, exit: 1, verdict: "fail",
			suites: map[string]suiteWant{"trust-plumbing": everyReader, "contract": everyReader, "manifest": everyReader, "naming": everyReader}},
		{name: "the human-readable report fails the manifest suite, naming an unknown spec field", dir: legacyBlock(with(manifestFile, unknownField)),
			args: []string{"--standalone", "$DIR"}, exit: 1, human: map[string]suiteWant{"manifest": {"fail", []string{"spec.frobnicate"}}}},
		{name: "a private metadata.id passes the manifest suite", dir: legacyBlock(with(manifestFile, replaced(t, legacy, "  id: blockverify-fixture\n", "  id: \"@acme/billing-db\"\n"))),
			args: jsonStandalone, exit: 0, verdict: "pass", suites: map[string]suiteWant{"manifest": {status: "pass"}}},
		{name: "an uppercase metadata.id namespace fails the manifest suite", dir: legacyBlock(with(manifestFile, replaced(t, legacy, "  id: blockverify-fixture\n", "  id: \"@Acme/x\"\n"))),
			args: jsonStandalone, exit: 1, verdict: "fail", suites: map[string]suiteWant{"manifest": {"fail", []string{"metadata.id"}}}},
		{name: "--base-manifest after the target names the base and not the target", dir: legacyBlock(), base: baseManifest(replaced(t, legacy, "version: 1.0.0", "version: 0.9.0")),
			args: []string{"--json", "--standalone", "$DIR", "--base-manifest", "$BASE"}, exit: 1, verdict: "fail",
			suites: map[string]suiteWant{"trust-plumbing": {status: "pass"}, "manifest": {"fail", []string{"spec.releaseClass"}}}},
		{name: "--base-manifest before the target names the base and not the target", dir: legacyBlock(), base: baseManifest(replaced(t, legacy, "version: 1.0.0", "version: 0.9.0")),
			args: []string{"--base-manifest", "$BASE", "--json", "--standalone", "$DIR"}, exit: 1, verdict: "fail",
			suites: map[string]suiteWant{"trust-plumbing": {status: "pass"}, "manifest": {"fail", []string{"spec.releaseClass"}}}},
		{name: "a manifest with no spec.naming skips naming and passes", dir: legacyBlock(), args: jsonStandalone, exit: 0, verdict: "pass",
			suites: map[string]suiteWant{"naming": {status: "skipped"}}},
		{name: "aws-app-infra's naming declaration passes naming", dir: legacyBlock(with(manifestFile, legacy+awsAppInfraNamingSpec)), args: jsonStandalone, exit: 0, verdict: "pass",
			suites: map[string]suiteWant{"naming": {status: "pass"}}},
		{name: "a maxLength the derived names do not allow fails naming and the verdict", args: jsonStandalone, exit: 1, verdict: "fail",
			dir: legacyBlock(with(manifestFile, legacy+namingSpec("40", derivedName(ecrPushTemplate, "64")))), suites: map[string]suiteWant{"naming": {status: "fail"}}},
		{name: "a kubernetes.namespace block on a hetzner.k3s-cluster passes the manifest suite", dir: testdataDir("manifest-kubernetes-namespace-on-cluster"),
			args: jsonStandalone, exit: exitNotJudged, verdict: "fail", suites: map[string]suiteWant{"manifest": {status: "pass"}}},
		{name: "a block that needs credential refresh passes the manifest suite", dir: testdataDir("manifest-needs-credential-refresh"),
			args: jsonStandalone, exit: exitNotJudged, verdict: "fail", suites: map[string]suiteWant{"manifest": {status: "pass"}}},
	}
	for dir, wants := range map[string][]string{
		"manifest-requirement-carries-kind":     {"spec.requires[0].kind is not a field of a mitstack/v1beta manifest"},
		"manifest-resource-carries-inputs":      {"spec.resources[2].inputs is not a field of a mitstack/v1beta manifest"},
		"manifest-alternatives-without-a-name":  {"spec.resources[8]", "name"},
		"manifest-shared-requirement-reference": {"spec.requires[1]", `"github.repository"`},
		"manifest-namespace-carries-on":         {"spec.resources[0].on is not a field of a mitstack/v1beta manifest"},
	} {
		raw, err := os.ReadFile(filepath.Join("testdata", dir, manifestFile))
		if err != nil {
			t.Fatalf("reading the fixture: %v", err)
		}
		_, perr := contract.ParseManifest(raw)
		if perr == nil {
			t.Fatalf("contract.ParseManifest accepts testdata/%s/%s, so the suite's failure is not the parser's reason", dir, manifestFile)
		}
		cases = append(cases, runCase{name: "testdata/" + dir + " fails the manifest suite with the parser's reason", dir: testdataDir(dir), args: jsonStandalone,
			exit: exitNotJudged, verdict: "fail", suites: map[string]suiteWant{"manifest": {"fail", append([]string{manifestFile, perr.Error()}, wants...)}}})
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, base := c.dir(t), ""
			if c.base != nil {
				base = c.base(t)
			}
			expand := strings.NewReplacer("$DIR", dir, "$BASE", base).Replace
			args := make([]string, len(c.args))
			for i, a := range c.args {
				args[i] = expand(a)
			}

			stdout, exit := runBlockverify(t, args...)

			if c.exit != exitNotJudged && exit != c.exit {
				t.Errorf("Run(%q) exit = %d; want %d\nstdout:\n%s", args, exit, c.exit, stdout)
			}
			for _, want := range c.stdout {
				if !strings.Contains(stdout, want) {
					t.Errorf("Run(%q) stdout does not contain %q:\n%s", args, want, stdout)
				}
			}
			got, wants := humanSuites(stdout), c.human
			if c.verdict != "" {
				r := decodeReport(t, stdout)
				if r.Verdict != c.verdict {
					t.Errorf("Run(%q) verdict = %q; want %q", args, r.Verdict, c.verdict)
				}
				for _, s := range r.Suites {
					got[s.Name] = s
				}
				wants = c.suites
			}
			for name, want := range wants {
				s, ok := got[name]
				if !ok || s.Status != want.status {
					t.Errorf("%s status = %q; want %q; detail: %s", name, s.Status, want.status, s.Detail)
					continue
				}
				for _, d := range want.detail {
					if !strings.Contains(s.Detail, expand(d)) {
						t.Errorf("%s detail = %q; want it to contain %q", name, s.Detail, expand(d))
					}
				}
			}
		})
	}
}

var update = flag.Bool("update", false, "update golden files")

const goldenReportPath = "testdata/standalone-report.golden.json"

// The golden pins every field of the legacy block's report: versions, mode, both digests, the run order and each suite's status and detail.
func TestGolden_StandaloneReport(t *testing.T) {
	dir := legacyBlock()(t)

	stdout, exit := runBlockverify(t, "--json", "--standalone", dir)

	if exit != 0 {
		t.Fatalf("the legacy block must pass: exit %d\n%s", exit, stdout)
	}
	got := decodeReport(t, stdout)
	gotPretty, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("re-marshal report: %v", err)
	}
	if *update {
		if err := os.WriteFile(goldenReportPath, append(gotPretty, '\n'), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", goldenReportPath, err)
		}
		return
	}
	wantRaw, err := os.ReadFile(goldenReportPath)
	if err != nil {
		t.Fatalf("golden file %s not found, run with -update: %v", goldenReportPath, err)
	}
	var want report
	if err := json.Unmarshal(wantRaw, &want); err != nil {
		t.Fatalf("golden file %s is not valid JSON: %v", goldenReportPath, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("report does not match golden %s; rerun with -update if the shape change is intended.\ngot:\n%s\nwant:\n%s", goldenReportPath, gotPretty, wantRaw)
	}
}

func sha256Of(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestReportDigests(t *testing.T) {
	dir := legacyBlock()(t)
	first, _ := runBlockverify(t, "--json", "--standalone", dir)
	second, _ := runBlockverify(t, "--json", "--standalone", dir)
	failing, _ := runBlockverify(t, "--json", "--standalone", legacyBlock(without("Dockerfile"))(t))
	manifest, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		t.Fatal(err)
	}

	r1, r2, rFail := decodeReport(t, first), decodeReport(t, second), decodeReport(t, failing)
	entries := make([]map[string]string, 0, len(r1.Suites))
	for _, s := range r1.Suites {
		entries = append(entries, map[string]string{"name": s.Name, "status": s.Status})
	}
	compact, _ := json.Marshal(entries)

	if want := sha256Of(manifest); r1.ManifestDigest != want {
		t.Errorf("manifestDigest = %q; want %q, the sha256 of <dir>/manifest.yaml", r1.ManifestDigest, want)
	}
	if want := sha256Of(compact); r1.ResultsDigest != want {
		t.Errorf("resultsDigest = %q; want %q, the sha256 of the compact [{name,status}] array %s", r1.ResultsDigest, want, compact)
	}
	if r1.ResultsDigest != r2.ResultsDigest || r1.ManifestDigest != r2.ManifestDigest {
		t.Errorf("digests differ across identical runs: results %q vs %q, manifest %q vs %q", r1.ResultsDigest, r2.ResultsDigest, r1.ManifestDigest, r2.ManifestDigest)
	}
	if r1.ResultsDigest == rFail.ResultsDigest {
		t.Errorf("resultsDigest = %q for both a passing block and one missing its Dockerfile; want it to change with a suite's status", r1.ResultsDigest)
	}
}

// The legacy block fails suites the field never reaches, so comparing with the same manifest unset isolates the field.
func TestNeedsCredentialRefreshChangesNoSuiteStatus(t *testing.T) {
	set := readFixtureManifest(t, "manifest-needs-credential-refresh")
	statuses := func(manifest string) (map[string]string, report) {
		stdout, _ := runBlockverify(t, "--json", "--standalone", legacyBlock(with(manifestFile, manifest))(t))
		r := decodeReport(t, stdout)
		out := map[string]string{}
		for _, s := range r.Suites {
			out[s.Name] = s.Status
		}
		return out, r
	}

	withField, r := statuses(set)
	withoutField, _ := statuses(replaced(t, set, "    needsCredentialRefresh: true\n", ""))

	if !reflect.DeepEqual(withField, withoutField) {
		t.Errorf("suite statuses with the field = %v, without it = %v; want the field to change none", withField, withoutField)
	}
	if m := suiteByName(t, r, "manifest"); m.Status != "pass" {
		t.Errorf("manifest status = %q, want %q; detail: %s", m.Status, "pass", m.Detail)
	}
	for _, s := range r.Suites {
		if strings.Contains(s.Detail, "needsCredentialRefresh") {
			t.Errorf("%s detail names the field: %s", s.Name, s.Detail)
		}
	}
}

func readFixtureManifest(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", dir, manifestFile))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	return string(raw)
}

func awsAppInfraGolden(t *testing.T) string {
	t.Helper()
	raw, err := contract.ManifestGoldens.ReadFile(contract.ManifestGoldensDir + "/aws-app-infra-v1beta.yaml")
	if err != nil {
		t.Fatalf("reading aws-app-infra-v1beta.yaml from contract.ManifestGoldens: %v", err)
	}
	return string(raw)
}

// Each refused fixture is its source with one edit, so it carries no second defect.
func TestFixtureManifests(t *testing.T) {
	onCluster := readFixtureManifest(t, "manifest-kubernetes-namespace-on-cluster")
	cases := []struct {
		dir, want string
	}{
		{"manifest-requirement-carries-kind", replaced(t, awsAppInfraGolden(t), "  requires: [github.repository]\n", "  requires:\n    - { type: github.repository, kind: git-repository }\n")},
		{"manifest-resource-carries-inputs", replaced(t, awsAppInfraGolden(t), "    - aws.security-group\n", "    - { type: aws.security-group, inputs: { network: resources.aws.vpc } }\n")},
		{"manifest-alternatives-without-a-name", replaced(t, awsAppInfraGolden(t), "    - aws.egress-proxy\n", "    - { type: [aws.egress-proxy, hetzner.firewall] }\n")},
		{"manifest-shared-requirement-reference", replaced(t, awsAppInfraGolden(t), "  requires: [github.repository]\n", "  requires: [github.repository, github.repository]\n")},
		{"manifest-namespace-carries-on", replaced(t, onCluster, "  resources: [kubernetes.namespace]\n", "  resources:\n    - { type: kubernetes.namespace, on: requires.hetzner.k3s-cluster }\n")},
	}
	for _, c := range cases {
		if got := readFixtureManifest(t, c.dir); got != c.want {
			t.Errorf("testdata/%s/%s is not its source with its one edit; want:\n%s", c.dir, manifestFile, c.want)
		}
	}
	for dir, wantRefresh := range map[string]bool{"manifest-kubernetes-namespace-on-cluster": false, "manifest-needs-credential-refresh": true} {
		m, err := contract.ParseManifest([]byte(readFixtureManifest(t, dir)))
		if err != nil || m.APIVersion != contract.ManifestAPIVersion || m.Block.Spec.Lifecycle.NeedsCredentialRefresh != wantRefresh {
			t.Errorf("testdata/%s parses as apiVersion %q, needsCredentialRefresh %v, error %v; want %s, %v, no error",
				dir, m.APIVersion, m.Block != nil && m.Block.Spec.Lifecycle.NeedsCredentialRefresh, err, contract.ManifestAPIVersion, wantRefresh)
		}
	}
}
