package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRange builds a repository with a seed commit and then one empty commit per subject, returning the directory and the seed and head shas.
func newRange(t *testing.T, subjects ...string) (dir, base, head string) {
	t.Helper()
	dir = t.TempDir()
	runGit(t, dir, "init", "-q")
	for _, s := range append([]string{"seed"}, subjects...) {
		runGit(t, dir, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "--allow-empty", "-q", "-m", s)
		head = runGit(t, dir, "rev-parse", "HEAD")
		if base == "" {
			base = head
		}
	}
	return dir, base, head
}

func TestRun(t *testing.T) {
	keyed := key(12) + ": fix the thing"
	merged := []string{"delete gets instance config", "fix review comments", "Merge pull request #6 from mitstack-eu/fix/" + key(3149) + "-delete-gets-instance-config"}
	cases := []struct {
		name, title string
		subjects    []string
		omit        string
		wantCode    int
		wantFirst   string
	}{
		{"a keyed commit and a keyed title exit zero", keyed, []string{keyed}, "", 0, ""},
		{"a merge range keyed only in its branch name exits non-zero naming the commits", key(3149) + ": delete gets instance config", merged, "", 1, "pr-keys: no board key in the pull request's commits"},
		{"a keyless title exits non-zero naming the title", "fix the thing", []string{keyed}, "", 1, "pr-keys: no board key in the pull request's title"},
		{"an empty range exits non-zero naming the commits", keyed, nil, "", 1, "pr-keys: no board key in the pull request's commits"},
		{"a missing --head exits two", keyed, nil, "--head", 2, "pr-keys: --head is required"},
		{"a missing --base exits two", keyed, nil, "--base", 2, "pr-keys: --base is required"},
		{"the title falls back to PR_TITLE when --title is omitted", keyed, []string{keyed}, "--title", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, base, head := newRange(t, tc.subjects...)
			t.Setenv("PR_TITLE", tc.title)
			var args []string
			for _, f := range [][2]string{{"--repo", dir}, {"--base", base}, {"--head", head}, {"--title", tc.title}} {
				if f[0] != tc.omit {
					args = append(args, f[:]...)
				}
			}
			var out bytes.Buffer

			code := Run(args, &out)

			first, _, _ := strings.Cut(out.String(), "\n")
			if code != tc.wantCode || (tc.wantFirst != "" && first != tc.wantFirst) {
				t.Errorf("Run(%q) = %d, first line %q; want %d, %q\noutput: %s", args, code, first, tc.wantCode, tc.wantFirst, out.String())
			}
		})
	}
}
