package ghapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/ghapi"
)

// A malformed variable name must never reach the PATCH fallback's URL path, an ambiguous 422 must fail rather than PATCH, and baseCommit's decodes must be bounded.

func TestSetVariable_RejectsMalformedNamesBeforeAnyRequest(t *testing.T) {
	for _, name := range []string{
		"a/b",
		"../etc",
		"a%2fb",
		"1abc",
		"",
	} {
		t.Run(name, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			c := ghapi.NewClient(srv.URL, "test-token-abc123", srv.Client())

			err := c.SetVariable(context.Background(), "acme/widget", name, "value")
			if err == nil {
				t.Errorf("SetVariable(name=%q) = nil error, want a validation error", name)
			}
			if calls != 0 {
				t.Errorf("SetVariable(name=%q): %d HTTP request(s) made, want 0 — invalid names must be refused before any request", name, calls)
			}
		})
	}
}

func TestSetVariable_AcceptsValidNames(t *testing.T) {
	for _, name := range []string{
		"MITSTACK_VAR",
		"_leading_underscore",
		"a1",
		"lower_case_123",
	} {
		t.Run(name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.WriteHeader(http.StatusCreated)
			}))
			defer srv.Close()
			c := ghapi.NewClient(srv.URL, "test-token-abc123", srv.Client())

			if err := c.SetVariable(context.Background(), "acme/widget", name, "value"); err != nil {
				t.Fatalf("SetVariable(name=%q): unexpected error: %v", name, err)
			}
			if gotPath != "/repos/acme/widget/actions/variables" {
				t.Errorf("request path = %q, want the create endpoint", gotPath)
			}
		})
	}
}

// --- 1b. The two 422 meanings are told apart ---------------------------------

func TestSetVariable_AlreadyExists422StillFallsBackToPatch(t *testing.T) {
	var patches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widget/actions/variables":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"variable already exists"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/widget/actions/variables/MITSTACK_VAR":
			patches++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := ghapi.NewClient(srv.URL, "test-token-abc123", srv.Client())

	err := c.SetVariable(context.Background(), "acme/widget", "MITSTACK_VAR", "value")
	if err != nil {
		t.Fatalf("SetVariable: an 'already exists' 422 must still fall back to PATCH and succeed, got: %v", err)
	}
	if patches != 1 {
		t.Errorf("PATCH calls = %d, want exactly 1", patches)
	}
}

func TestSetVariable_InvalidName422FailsWithoutPatchFallback(t *testing.T) {
	var patches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widget/actions/variables":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"name is not valid. It can only contain alphanumeric characters and underscores"}`))
		case r.Method == http.MethodPatch:
			patches++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := ghapi.NewClient(srv.URL, "test-token-abc123", srv.Client())

	err := c.SetVariable(context.Background(), "acme/widget", "MITSTACK_VAR", "value")
	if err == nil {
		t.Fatal("SetVariable: an 'invalid name' 422 must be a real failure, not silently fall through to PATCH")
	}
	if patches != 0 {
		t.Errorf("PATCH calls = %d, want 0 — an invalid-name 422 must never reach the concatenating PATCH path", patches)
	}
}

func TestSetVariable_AmbiguousUnparseable422FailsSafeWithoutPatchFallback(t *testing.T) {
	var patches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widget/actions/variables":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`not json at all`))
		case r.Method == http.MethodPatch:
			patches++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := ghapi.NewClient(srv.URL, "test-token-abc123", srv.Client())

	err := c.SetVariable(context.Background(), "acme/widget", "MITSTACK_VAR", "value")
	if err == nil {
		t.Fatal("SetVariable: an unparseable 422 body must fail safe (error), never silently fall through to PATCH")
	}
	if patches != 0 {
		t.Errorf("PATCH calls = %d, want 0 — an ambiguous 422 must fail safe, not guess 'already exists'", patches)
	}
}

// --- 2. baseCommit's two decodes are bounded ---------------------------------

// hugeJSONWithSHA is comfortably larger than any sane single-object response
// bound (client.go's existing bounds are 4<<10 and 8<<10) while still being
// syntactically valid, complete JSON — an UNBOUNDED decoder reads it fine (in
// well under a second, in-process), which is exactly why this pins new
// behaviour: today's bare json.Decoder succeeds on it, so these tests are red
// against the current implementation.
func hugeJSONWithSHA(sha string) string {
	padding := strings.Repeat("a", 2<<20) // 2MiB — far past any 4KiB/8KiB bound
	return `{"object":{"sha":"` + sha + `"},"tree":{"sha":"` + sha + `"},"padding":"` + padding + `"}`
}

func TestCommitFiles_RefLookupDecodeIsBounded(t *testing.T) {
	var commitLookupCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget/git/ref/heads/main":
			_, _ = w.Write([]byte(hugeJSONWithSHA("base-sha")))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget/git/commits/base-sha":
			commitLookupCalls++
			_, _ = w.Write([]byte(`{"tree":{"sha":"tree-sha"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widget/git/trees":
			_, _ = w.Write([]byte(`{"sha":"tree-sha"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := ghapi.NewClient(srv.URL, "test-token-abc123", srv.Client())

	err := c.CommitFiles(context.Background(), "acme/widget", "reconcile", map[string]string{"a.txt": "x"})
	if err == nil {
		t.Fatal("CommitFiles: an oversized ref-lookup response must be rejected by a bounded decode, not read to completion")
	}
	if commitLookupCalls != 0 {
		t.Errorf("commit-lookup GET calls = %d, want 0 — the call chain must stop at the bounded ref decode, never proceed further", commitLookupCalls)
	}
}

func TestCommitFiles_CommitLookupDecodeIsBounded(t *testing.T) {
	var treeCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget/git/ref/heads/main":
			_, _ = w.Write([]byte(`{"object":{"sha":"base-sha"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget/git/commits/base-sha":
			_, _ = w.Write([]byte(hugeJSONWithSHA("tree-sha")))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widget/git/trees":
			treeCalls++
			_, _ = w.Write([]byte(`{"sha":"tree-sha"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := ghapi.NewClient(srv.URL, "test-token-abc123", srv.Client())

	err := c.CommitFiles(context.Background(), "acme/widget", "reconcile", map[string]string{"a.txt": "x"})
	if err == nil {
		t.Fatal("CommitFiles: an oversized commit-lookup response must be rejected by a bounded decode, not read to completion")
	}
	if treeCalls != 0 {
		t.Errorf("tree POST calls = %d, want 0 — the call chain must stop at the bounded commit decode, never proceed to build a tree", treeCalls)
	}
}

// --- 3. Guard: the token never leaks on the new failure paths ----------------

func TestNoTokenLeak_SetVariableRejectedNameError(t *testing.T) {
	const token = "ghs_super-secret-installation-token-999"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s — an invalid name must never reach the network", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := ghapi.NewClient(srv.URL, token, srv.Client())

	err := c.SetVariable(context.Background(), "acme/widget", "../evil", "value")
	if err == nil {
		t.Fatal("expected a validation error for a malformed name")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("SetVariable error leaked the installation token: %v", err)
	}
}

func TestNoTokenLeak_CommitFilesLimitBreachDoesNotLeakToken(t *testing.T) {
	const token = "ghs_super-secret-installation-token-999"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget/git/ref/heads/main":
			// A hostile/misbehaving upstream that happens to echo the
			// Authorization header back inside the oversized body.
			padding := strings.Repeat("a", 2<<20)
			_, _ = w.Write([]byte(`{"object":{"sha":"base-sha"},"auth":"` + token + `","padding":"` + padding + `"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget/git/commits/base-sha":
			// Reachable only if the ref decode is NOT bounded — kept
			// well-formed so an unbounded client sails straight through to
			// here (and beyond), which is what makes this test genuinely
			// red against today's implementation rather than incidentally
			// passing on an unrelated 404.
			_, _ = w.Write([]byte(`{"tree":{"sha":"tree-sha"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widget/git/trees":
			_, _ = w.Write([]byte(`{"sha":"tree-sha"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := ghapi.NewClient(srv.URL, token, srv.Client())

	err := c.CommitFiles(context.Background(), "acme/widget", "reconcile", map[string]string{"a.txt": "x"})
	if err == nil {
		t.Fatal("expected the oversized ref-lookup response to be rejected")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("CommitFiles error leaked the installation token: %v", err)
	}
}
