package ghapi_test

import (
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/ghapi"
)

// SafeRepo keeps an operator-supplied owner/name from smuggling a host, path traversal or extra segments into a GitHub API URL.
func TestSafeRepo(t *testing.T) {
	for repo, ok := range map[string]bool{
		"my-org/my-repo": true, "my_org.example/my.repo-name_2": true, "a/b": true,
		"../x": false, "a/b/c": false, "x@evil.com": false, "owner/../../etc/passwd": false, "owner/name/../../../secrets": false,
		"https://evil.com/owner/name": false, "owner": false, "owner/": false, "/name": false, "owner/name?query=1": false,
		"owner/name#frag": false, "": false, "owner/.": false, "./name": false, "owner/..": false, "owner//name": false,
	} {
		got, err := ghapi.SafeRepo(repo)

		if ok && (err != nil || got != repo) {
			t.Errorf("SafeRepo(%q) = %q, %v; want it to round-trip", repo, got, err)
		}
		if !ok && err == nil {
			t.Errorf("SafeRepo(%q) = %q, nil; want an error", repo, got)
		}
	}
}
