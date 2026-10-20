package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const advice = "A comment says what the code does or its constraint: no ticket id, no history. " +
	"Carry the ticket id in the commit subject and the pull-request title, name a function rather than a file:line, " +
	"and delete a comment that restates the code."

type row struct {
	name   string
	base   map[string]string
	head   map[string]string
	rm     []string
	mv     [][2]string
	commit bool
	args   []string
	code   int
	want   []string
	not    []string
}

func hermetic(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func lines(n int, format string) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, format, i)
	}
	return b.String()
}

func goFile(pkg string, comments, total int) string {
	return lines(comments, "// note %d\n") + "package " + pkg + "\n" + lines(total-comments-1, "var v%[1]d = %[1]d\n")
}

func run(args ...string) (stdout, stderr string, code int) {
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut)
	return out.String(), errOut.String(), code
}

func repoAt(t *testing.T, files map[string]string) (dir, base string) {
	t.Helper()
	hermetic(t)
	dir = t.TempDir()
	git(t, dir, "init", "-q")
	write(t, dir, files)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	return dir, git(t, dir, "rev-parse", "HEAD")
}

// ticketIDs writes {MIT} and {MS} as the real key prefixes, so no test source holds a ticket id.
var ticketIDs = strings.NewReplacer("{MIT}", "MIT", "{MS}", "MS")

func withIDs(files map[string]string) map[string]string {
	out := make(map[string]string, len(files))
	for p, c := range files {
		out[p] = ticketIDs.Replace(c)
	}
	return out
}

func (r row) check(t *testing.T) {
	t.Helper()
	dir, base := repoAt(t, withIDs(r.base))
	short := git(t, dir, "rev-parse", "--short", base)
	for _, m := range r.mv {
		git(t, dir, "mv", m[0], m[1])
	}
	for _, p := range r.rm {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Fatal(err)
		}
	}
	write(t, dir, withIDs(r.head))
	if r.commit {
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-q", "--allow-empty", "-m", "head")
	}
	out, errOut, code := run(append([]string{"-repo", dir, "-base", base}, r.args...)...)
	if code != r.code {
		t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", code, r.code, out, errOut)
	}
	for _, w := range r.want {
		if w = ticketIDs.Replace(strings.ReplaceAll(w, "{base}", short)); !strings.Contains(out, w) {
			t.Errorf("stdout lacks %q:\n%s", w, out)
		}
	}
	for _, n := range r.not {
		if n = ticketIDs.Replace(n); strings.Contains(out, n) {
			t.Errorf("stdout holds %q:\n%s", n, out)
		}
	}
	summary := `^commentcheck: \d+ files judged against ` + short + `, no finding\n?$`
	if slices.Contains(r.args, "-same-code") {
		summary = `^commentcheck: \d+ Go and TypeScript files carry the same code as ` + short + `\n?$`
	}
	switch {
	case r.code == 0 && !regexp.MustCompile(summary).MatchString(out):
		t.Errorf("stdout does not match %s:\n%s", summary, out)
	case r.code == 1 && !slices.Contains(r.args, "-same-code") && !strings.Contains(strings.Join(strings.Fields(out), " "), advice):
		t.Errorf("stdout lacks the closing paragraph:\n%s", out)
	}
}

func runRows(t *testing.T, rows []row) {
	for _, r := range rows {
		t.Run(r.name, r.check)
	}
}

func TestRun_UsageErrorsExitTwo(t *testing.T) {
	dir, _ := repoAt(t, map[string]string{"a/a.go": "package a\n"})
	for _, c := range []struct {
		name string
		args []string
	}{
		{"check without -base", []string{"-repo", dir}},
		{"same-code without -base", []string{"-repo", dir, "-same-code"}},
		{"same-code with report", []string{"-repo", dir, "-base", "HEAD", "-same-code", "-report", "."}},
		{"a base that is not a commit", []string{"-repo", dir, "-base", "no-such-rev"}},
		{"an unknown flag", []string{"-repo", dir, "-base", "HEAD", "-bogus"}},
		{"a report path that does not exist", []string{"-repo", dir, "-report", "missing"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if out, errOut, code := run(c.args...); code != 2 {
				t.Errorf("exit %d, want 2\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
			}
		})
	}
}

func TestReport_PrintsSharesPerPathWithoutGit(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"r/a.go":                "// Package r reports.\n// second line\npackage r\n\nfunc f(p *int) {\n\t*p = 1\n}\n" + lines(2, "var a%[1]d = %[1]d\n") + "var last = 0",
		"r/sub/b.go":            "package sub\n/*\nexplains b\n*/\n" + lines(5, "var b%[1]d = %[1]d\n") + "var t = 1 // trailing\n",
		"r/c.ts":                "export const c = 1;\nconst s = '// not a comment';\nconst re = /https?:\\/\\//;\nexport const d = `/* ${s} */`;\nexport default re;\n",
		"r/empty.go":            "",
		"r/gen.go":              "// Code generated by stub. DO NOT EDIT.\n\npackage r\n// x\n",
		"r/testdata/x.go":       "// a\n// b\n",
		"r/node_modules/m/i.ts": "// junk\n",
		"r/notes.md":            "# heading\n",
		"r/a_test.go":           "package r\n// checks f\n" + lines(6, "var t%[1]d = %[1]d\n"),
		"r/__tests__/d.ts":      "export {};\n" + lines(3, "const d%[1]d = %[1]d;\n"),
		"r/e.spec.tsx":          "export const E = () => <div>{1}</div>;\n",
		"r/sub/vendor/v/v.go":   "// v\npackage v\n",
	})
	out, errOut, code := run("-repo", dir, "-report", "r", "r/a.go", "r/sub")
	want := "r: production 5/25 (20.0%), test 1/13 (7.7%)\n" +
		"r/a.go: production 2/10 (20.0%), test 0/0 (0.0%)\n" +
		"r/sub: production 3/10 (30.0%), test 0/0 (0.0%)\n"
	if code != 0 || out != want {
		t.Errorf("exit %d, stdout:\n%s\nwant exit 0, stdout:\n%s\nstderr:\n%s", code, out, want, errOut)
	}
}
