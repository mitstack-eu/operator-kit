package kitbinding_test

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/kitbinding"
)

const fixtureDir = "testdata/handler-import"

// The txtar cases under testdata/handler-import are shared with the platform's operator gates.
type fixtureCase struct {
	name    string
	verdict string
	names   string
	files   map[string]string
}

func loadFixtureSet(t *testing.T) []fixtureCase {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no fixtures under %s: the shared handler-import set is missing", fixtureDir)
	}
	sort.Strings(paths)
	var cases []fixtureCase
	for _, p := range paths {
		cases = append(cases, parseFixture(t, p))
	}
	return cases
}

func parseFixture(t *testing.T, path string) fixtureCase {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c := fixtureCase{
		name:  strings.TrimSuffix(filepath.Base(path), ".txtar"),
		files: map[string]string{},
	}
	var current string
	var body strings.Builder
	flush := func() {
		if current != "" {
			c.files[current] = body.String()
		}
		body.Reset()
	}
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "-- ") && strings.HasSuffix(line, " --") && len(line) > 6 {
			flush()
			current = strings.TrimSuffix(strings.TrimPrefix(line, "-- "), " --")
			continue
		}
		if current == "" {
			if v, ok := strings.CutPrefix(line, "verdict: "); ok && c.verdict == "" {
				c.verdict = v
			}
			if v, ok := strings.CutPrefix(line, "names: "); ok && c.names == "" {
				c.names = v
			}
			continue
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	flush()
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	switch c.verdict {
	case "pass", "absent", "tooling":
	default:
		t.Fatalf("%s: verdict %q is none of pass, absent, tooling", path, c.verdict)
	}
	if len(c.files) == 0 {
		t.Fatalf("%s holds no files", path)
	}
	return c
}

func materialise(t *testing.T, c fixtureCase) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range c.files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestHandlerPackage_IsTheKitsHandlerImportPath(t *testing.T) {
	if kitbinding.HandlerPackage != "github.com/mitstack-eu/operator-kit/v3/handler" {
		t.Errorf("HandlerPackage = %q, want %q", kitbinding.HandlerPackage, "github.com/mitstack-eu/operator-kit/v3/handler")
	}
}

// Deleting a hard fixture must fail here, or the agreement between the kit and the platform turns vacuous.
func TestFixtureSet_HoldsEveryRequiredCase(t *testing.T) {
	want := map[string]string{
		"comment-only":                "absent",
		"string-literal-only":         "absent",
		"build-ignore-only":           "absent",
		"testdata-only":               "absent",
		"vendor-only":                 "absent",
		"underscore-dir-only":         "absent",
		"dot-dir-only":                "absent",
		"test-file-only":              "absent",
		"no-mention":                  "absent",
		"import-root":                 "pass",
		"import-satisfied-constraint": "pass",
		"import-aliased":              "pass",
		"import-subpackage":           "pass",
		"import-major-v2":             "pass",
		"import-major-v3":             "pass",
		"unparsable-only":             "tooling",
		"unparsable-beside-import":    "tooling",
		"malformed-constraint":        "tooling",
	}
	got := map[string]string{}
	for _, c := range loadFixtureSet(t) {
		got[c.name] = c.verdict
	}
	for name, verdict := range want {
		if got[name] != verdict {
			t.Errorf("fixture %s: verdict %q, want %q (missing fixtures read as \"\")", name, got[name], verdict)
		}
	}
}

func TestHandlerImportFiles_AnswersEveryFixtureWithItsVerdict(t *testing.T) {
	for _, c := range loadFixtureSet(t) {
		t.Run(c.name, func(t *testing.T) {
			dir := materialise(t, c)
			files, err := kitbinding.HandlerImportFiles(dir)

			switch c.verdict {
			case "pass":
				if err != nil {
					t.Fatalf("HandlerImportFiles error = %v, want the importing file %s", err, c.names)
				}
				if want := []string{c.names}; !reflect.DeepEqual(files, want) {
					t.Errorf("HandlerImportFiles = %q, want %q (slash-separated, relative to the module dir)", files, want)
				}
			case "absent":
				if err != nil {
					t.Fatalf("HandlerImportFiles error = %v, want no error: this tree scans cleanly and imports nothing", err)
				}
				if len(files) != 0 {
					t.Errorf("HandlerImportFiles = %q, want none: no file a default build compiles imports %s",
						files, kitbinding.HandlerPackage)
				}
			case "tooling":
				if err == nil {
					t.Fatalf("HandlerImportFiles = %q with no error, want an error: a scan that could not read %s is never an answer",
						files, c.names)
				}
				if !strings.Contains(err.Error(), c.names) {
					t.Errorf("error = %q, want it to name %s, the file the scan could not read", err, c.names)
				}
			}
		})
	}
}

func TestHandlerImportFiles_ReportsEveryImportingFileSorted(t *testing.T) {
	const imp = "package main\n\nimport \"github.com/mitstack-eu/operator-kit/v3/handler\"\n\nvar _ = handler.New\n"
	dir := materialise(t, fixtureCase{files: map[string]string{
		"z.go":         imp,
		"a.go":         imp,
		"cmd/x/x.go":   strings.Replace(imp, "package main", "package x", 1),
		"a_test.go":    imp,
		"plain.go":     "package main\n",
		"docs/note.md": "\"github.com/mitstack-eu/operator-kit/v3/handler\"\n",
	}})
	files, err := kitbinding.HandlerImportFiles(dir)
	if err != nil {
		t.Fatalf("HandlerImportFiles error = %v", err)
	}
	want := []string{"a.go", "cmd/x/x.go", "z.go"}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("HandlerImportFiles = %q, want %q", files, want)
	}
}

func TestHandlerImportFiles_MissingDirectoryIsAnError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-block")
	files, err := kitbinding.HandlerImportFiles(missing)
	if err == nil {
		t.Fatalf("HandlerImportFiles = %q with no error, want an error for a directory that does not exist", files)
	}
	if !strings.Contains(err.Error(), "no-such-block") {
		t.Errorf("error = %q, want it to name the directory it could not read", err)
	}
}

// A directory the scan cannot enter is an error even when another file imports the handler, since what it could not read is unknown.
func TestHandlerImportFiles_UnreadableDirectoryIsAnError(t *testing.T) {
	dir := materialise(t, fixtureCase{files: map[string]string{
		"main.go":               "package main\n\nimport \"github.com/mitstack-eu/operator-kit/v3/handler\"\n\nfunc main() { _ = handler.New }\n",
		"internal/wire/wire.go": "package wire\n",
	}})
	locked := filepath.Join(dir, "internal")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Fatalf("setup: %s is still readable after chmod 0 (running as root?), so the fault never reaches the scan", locked)
	}

	files, err := kitbinding.HandlerImportFiles(dir)
	if err == nil {
		t.Fatalf("HandlerImportFiles = %q with no error, want an error for the unreadable directory %s", files, locked)
	}
	if !strings.Contains(err.Error(), "internal") {
		t.Errorf("error = %q, want it to name the directory it could not read", err)
	}
}
