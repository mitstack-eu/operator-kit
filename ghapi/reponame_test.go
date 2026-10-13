package ghapi_test

import (
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/ghapi"
)

// SafeRepo validates the operator-supplied "owner/name" repo param so it
// can't be used to smuggle a host, path traversal, or extra path segments
// into an outbound GitHub API URL (the reference: examples/web-app-aws/
// app-scaffold/urlguard.go's safeRepoSegment).

func TestSafeRepo_AcceptsOwnerSlashName(t *testing.T) {
	for _, repo := range []string{
		"my-org/my-repo",
		"my_org.example/my.repo-name_2",
		"a/b",
	} {
		got, err := ghapi.SafeRepo(repo)
		if err != nil {
			t.Errorf("SafeRepo(%q): unexpected error: %v", repo, err)
		}
		if got != repo {
			t.Errorf("SafeRepo(%q) = %q, want it to round-trip", repo, got)
		}
	}
}

func TestSafeRepo_RejectsTraversalAndMalformedInput(t *testing.T) {
	for _, repo := range []string{
		"../x",
		"a/b/c",
		"x@evil.com",
		"owner/../../etc/passwd",
		"owner/name/../../../secrets",
		"https://evil.com/owner/name",
		"owner",
		"owner/",
		"/name",
		"owner/name?query=1",
		"owner/name#frag",
		"",
		"owner/.",
		"./name",
		"owner/..",
		"owner//name",
	} {
		if got, err := ghapi.SafeRepo(repo); err == nil {
			t.Errorf("SafeRepo(%q) = %q, nil; want an error", repo, got)
		}
	}
}
