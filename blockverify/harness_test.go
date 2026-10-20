package main

// The harness writes each fixture block into t.TempDir() as a compiling module that replaces the kit onto this checkout.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

const fixturesDir = "testdata/fixtures"

func kitModuleDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed, so the kit module directory is unknown")
	}
	dir := filepath.Dir(filepath.Dir(thisFile))
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("resolved kit module dir %s has no go.mod: %v", dir, err)
	}
	return dir
}

func kitGoMod(t *testing.T) string {
	t.Helper()
	return "module fixture\n\ngo 1.25\n\nrequire github.com/mitstack-eu/operator-kit/v2 v2.0.0\n\n" +
		"replace github.com/mitstack-eu/operator-kit/v2 => " + kitModuleDir(t) + "\n"
}

func kitShimCanonical(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(kitShimCanonicalFile)
	if err != nil {
		t.Fatalf("reading the canonical shim %s: %v", kitShimCanonicalFile, err)
	}
	return string(raw)
}

// readTxtar returns an archive's leading comment and its files by name.
func readTxtar(t *testing.T, path string) (comment string, files map[string]string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	files = map[string]string{}
	var current string
	var body strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := sc.Text()
		if name, ok := strings.CutPrefix(line, "-- "); ok && strings.HasSuffix(name, " --") && len(name) > 3 {
			if current != "" {
				files[current] = body.String()
			}
			current, body = strings.TrimSuffix(name, " --"), strings.Builder{}
			continue
		}
		if current == "" {
			comment += line + "\n"
			continue
		}
		body.WriteString(line + "\n")
	}
	if current != "" {
		files[current] = body.String()
	}
	return comment, files
}

// source returns one file of testdata/fixtures/sources.txtar.
func source(t *testing.T, name string) string {
	t.Helper()
	_, files := readTxtar(t, filepath.Join(fixturesDir, "sources.txtar"))
	content, ok := files[name]
	if !ok {
		t.Fatalf("sources.txtar has no file %q", name)
	}
	return content
}

func fixtureFile(t *testing.T, archive, name string) string {
	t.Helper()
	_, files := readTxtar(t, filepath.Join(fixturesDir, archive))
	return files[name]
}

func legacyManifest(t *testing.T) string {
	return fixtureFile(t, "legacy-block.txtar", "manifest.yaml")
}

// replaced fails the test when old is not in s, so a fixture edit can never silently change nothing.
func replaced(t *testing.T, s, old, new string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("fixture edit: %q is not in\n%s", old, s)
	}
	return strings.Replace(s, old, new, 1)
}

// option edits a fixture's files before they are written.
type option func(t *testing.T, files map[string]string)

func with(name, content string) option {
	return func(_ *testing.T, files map[string]string) { files[name] = content }
}

func withSource(name, src string) option {
	return func(t *testing.T, files map[string]string) { files[name] = source(t, src) }
}

func without(names ...string) option {
	return func(_ *testing.T, files map[string]string) {
		for _, n := range names {
			delete(files, n)
		}
	}
}

// dirFunc builds a fixture directory for one case.
type dirFunc func(t *testing.T) string

func fixture(base func(t *testing.T) map[string]string, opts ...option) dirFunc {
	return func(t *testing.T) string {
		t.Helper()
		files := base(t)
		for _, o := range opts {
			o(t, files)
		}
		return writeFixture(t, files)
	}
}

// legacyBlock passes every suite as it stands.
func legacyBlock(opts ...option) dirFunc {
	return fixture(func(t *testing.T) map[string]string {
		_, files := readTxtar(t, filepath.Join(fixturesDir, "legacy-block.txtar"))
		files["go.mod"], files["kitshim.go"] = kitGoMod(t), kitShimCanonical(t)
		return files
	}, opts...)
}

// noContractBlock is legacyBlock with a manifest that mentions kit/v1 only where it declares no contract.
func noContractBlock(opts ...option) dirFunc {
	return legacyBlock(append([]option{withSource("manifest.yaml", "no-contract-manifest.yaml")}, opts...)...)
}

func kitV1Block(opts ...option) dirFunc {
	return fixture(func(t *testing.T) map[string]string {
		_, files := readTxtar(t, filepath.Join(fixturesDir, "kit-v1-block.txtar"))
		files["go.mod"] = kitGoMod(t)
		return files
	}, opts...)
}

// moduleBlock is a bare compiling module that the options give a manifest and tests.
func moduleBlock(opts ...option) dirFunc {
	return fixture(func(t *testing.T) map[string]string {
		return map[string]string{"go.mod": kitGoMod(t), "main.go": source(t, "main-without-handler.go")}
	}, opts...)
}

func manifestOnly(content string) dirFunc {
	return fixture(func(*testing.T) map[string]string { return map[string]string{manifestFile: content} })
}

func emptyDir(t *testing.T) string { return t.TempDir() }

func testdataDir(name string) dirFunc {
	return func(*testing.T) string { return filepath.Join("testdata", name) }
}

// then runs after on the directory dir builds, for a fault the files map cannot express.
func then(dir dirFunc, after func(t *testing.T, dir string)) dirFunc {
	return func(t *testing.T) string {
		t.Helper()
		d := dir(t)
		after(t, d)
		return d
	}
}

func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return dir
}

func mkdir(name string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func baseManifest(content string) func(t *testing.T) string {
	return func(t *testing.T) string {
		path := filepath.Join(t.TempDir(), "base-manifest.yaml")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
}

// refusal fails the test when the parser accepts content, and returns the parser's message.
func refusal(t *testing.T, content, path string) string {
	t.Helper()
	_, err := contract.ParseManifest([]byte(content))
	if err == nil {
		t.Fatalf("contract.ParseManifest accepts the manifest, which must be refused naming %s:\n%s", path, content)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("contract.ParseManifest error = %q, want it to name %s", err, path)
	}
	return err.Error()
}

// suiteCase is one input to one or more suites; $DIR and $BASE in detail and not stand for the case's paths.
type suiteCase struct {
	name   string
	dir    dirFunc
	base   func(t *testing.T) string
	status string
	detail []string
	not    []string
	check  func(t *testing.T, got SuiteResult)
}

var suitesByName = map[string]func(target, base string) SuiteResult{
	"trust-plumbing": func(d, _ string) SuiteResult { return trustPlumbingSuite(d) },
	"contract":       func(d, _ string) SuiteResult { return contractSuite(d) },
	"secret-safety":  func(d, _ string) SuiteResult { return secretSafetySuite(d) },
	"teardown":       func(d, _ string) SuiteResult { return teardownSuite(d) },
	"resources":      func(d, _ string) SuiteResult { return resourcesSuite(d) },
	"deliverable":    func(d, _ string) SuiteResult { return deliverableSuite(d) },
	"manifest":       manifestSuite,
	"naming":         func(d, _ string) SuiteResult { return namingSuite(d) },
}

func runSuites(t *testing.T, suites []string, cases []suiteCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, base := c.dir(t), ""
			if c.base != nil {
				base = c.base(t)
			}
			expand := strings.NewReplacer("$DIR", dir, "$BASE", base).Replace

			for _, name := range suites {
				got := suitesByName[name](dir, base)

				if got.Name != name || got.Status != c.status || got.Detail == "" {
					t.Errorf("%s(%s) = %q %q, detail %q; want %q %q with a detail", name, dir, got.Name, got.Status, got.Detail, name, c.status)
					continue
				}
				for _, want := range c.detail {
					if !strings.Contains(got.Detail, expand(want)) {
						t.Errorf("%s detail = %q; want it to contain %q", name, got.Detail, expand(want))
					}
				}
				for _, unwanted := range c.not {
					if strings.Contains(got.Detail, expand(unwanted)) {
						t.Errorf("%s detail = %q; want it not to contain %q", name, got.Detail, expand(unwanted))
					}
				}
				if c.check != nil {
					c.check(t, got)
				}
			}
		})
	}
}

// report is a local struct rather than the production type, so the test pins the JSON field names.
type report struct {
	SuiteVersion   string        `json:"suiteVersion"`
	KitVersion     string        `json:"kitVersion"`
	Mode           string        `json:"mode"`
	ManifestDigest string        `json:"manifestDigest"`
	Verdict        string        `json:"verdict"`
	ResultsDigest  string        `json:"resultsDigest"`
	Suites         []reportSuite `json:"suites"`
}

type reportSuite struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
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
