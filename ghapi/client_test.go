package ghapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/ghapi"
)

// newTestClient points a Client at an httptest fixture. Client.Base is a
// plain, already-resolved string — production callers build it via
// ghapi.Base(endpoint, extraHosts...).String() (base_test.go pins that
// resolution step); by the time it reaches Client the trust decision has
// already been made once, so a test fixture can supply srv.URL directly with
// no separate carve-out anywhere in the trust boundary itself.
func newTestClient(srv *httptest.Server, token string) *ghapi.Client {
	return ghapi.NewClient(srv.URL, token, srv.Client())
}

// --- EnsureRepo is idempotent (GET-then-create) ------------------------------

func TestEnsureRepo_MakesNoCreateCallWhenRepoAlreadyExists(t *testing.T) {
	var getCalls, createCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget":
			getCalls++
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/orgs/acme/repos":
			createCalls++
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(srv, "test-token-abc123")

	full, err := c.EnsureRepo(context.Background(), "acme", "widget")
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	if full != "acme/widget" {
		t.Errorf("full name = %q, want %q", full, "acme/widget")
	}
	if getCalls != 1 {
		t.Errorf("GET /repos calls = %d, want 1", getCalls)
	}
	if createCalls != 0 {
		t.Errorf("POST create calls = %d, want 0 — the repo already exists", createCalls)
	}
}

func TestEnsureRepo_CreatesWhenRepoIsMissing(t *testing.T) {
	var createCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/orgs/acme/repos":
			createCalls++
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"full_name": "acme/widget"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(srv, "test-token-abc123")

	full, err := c.EnsureRepo(context.Background(), "acme", "widget")
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	if full != "acme/widget" {
		t.Errorf("full name = %q, want %q", full, "acme/widget")
	}
	if createCalls != 1 {
		t.Errorf("create calls = %d, want exactly 1", createCalls)
	}
}

func TestDeleteRepo_AbsentRepoIsSuccessNotError(t *testing.T) {
	var deleteCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/repos/acme/widget" {
			deleteCalls++
			w.WriteHeader(http.StatusNotFound)
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := newTestClient(srv, "test-token-abc123")

	if err := c.DeleteRepo(context.Background(), "acme", "widget"); err != nil {
		t.Errorf("DeleteRepo on an already-absent repo must succeed (404 is success), got: %v", err)
	}
	if deleteCalls != 1 {
		t.Errorf("delete calls = %d, want 1", deleteCalls)
	}
}

func TestDeleteRepo_PropagatesARealFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"insufficient permissions"}`))
	}))
	defer srv.Close()
	c := newTestClient(srv, "test-token-abc123")

	if err := c.DeleteRepo(context.Background(), "acme", "widget"); err == nil {
		t.Error("a real (non-404) failure must be surfaced, not swallowed as if it were a successful teardown")
	}
}

func TestCommitFiles_NoOpWhenTheTreeIsUnchanged(t *testing.T) {
	var commitCalls, refPatches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget/git/ref/heads/main":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": "base-sha"}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget/git/commits/base-sha":
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": map[string]string{"sha": "tree-sha"}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widget/git/trees":
			// GitHub returns the SAME tree sha when the posted content is
			// byte-identical to base_tree.
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "tree-sha"})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widget/git/commits":
			commitCalls++
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/widget/git/refs/heads/main":
			refPatches++
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(srv, "test-token-abc123")

	err := c.CommitFiles(context.Background(), "acme/widget", "reconcile", map[string]string{"a.txt": "same content"})
	if err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}
	if commitCalls != 0 {
		t.Errorf("commit POSTs = %d, want 0 — an unchanged tree must be a true no-op (MIT-1230)", commitCalls)
	}
	if refPatches != 0 {
		t.Errorf("ref PATCHes = %d, want 0 — the ref must not move when nothing changed", refPatches)
	}
}

func TestCommitFiles_CommitsAndMovesTheRefWhenTheTreeChanges(t *testing.T) {
	var commitCalls, refPatches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget/git/ref/heads/main":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": "base-sha"}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget/git/commits/base-sha":
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": map[string]string{"sha": "tree-sha"}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widget/git/trees":
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "new-tree-sha"})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widget/git/commits":
			commitCalls++
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "new-commit-sha"})
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/widget/git/refs/heads/main":
			refPatches++
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(srv, "test-token-abc123")

	err := c.CommitFiles(context.Background(), "acme/widget", "reconcile", map[string]string{"a.txt": "new content"})
	if err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}
	if commitCalls != 1 {
		t.Errorf("commit POSTs = %d, want exactly 1", commitCalls)
	}
	if refPatches != 1 {
		t.Errorf("ref PATCHes = %d, want exactly 1", refPatches)
	}
}

// --- SetVariable is safe to call twice ---------------------------------------

func TestSetVariable_SafeToCallTwice(t *testing.T) {
	var creates, updates int
	var exists bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widget/actions/variables":
			if exists {
				w.WriteHeader(http.StatusConflict)
				return
			}
			creates++
			exists = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/widget/actions/variables/MITSTACK_DISPATCH_URL":
			updates++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(srv, "test-token-abc123")

	if err := c.SetVariable(context.Background(), "acme/widget", "MITSTACK_DISPATCH_URL", "https://cicd.mitstack.dev"); err != nil {
		t.Fatalf("first SetVariable: %v", err)
	}
	if err := c.SetVariable(context.Background(), "acme/widget", "MITSTACK_DISPATCH_URL", "https://cicd.mitstack.dev/v2"); err != nil {
		t.Fatalf("second SetVariable: %v", err)
	}
	if creates != 1 {
		t.Errorf("create calls = %d, want 1", creates)
	}
	if updates != 1 {
		t.Errorf("update (PATCH) calls = %d, want exactly 1 — the repeat call must update, not fail", updates)
	}
}

// --- The installation token never leaks (AC#5) --------------------------------
//
// These drive a GENUINE failure path — the upstream response body contains
// the token — and assert it is absent from the returned error. A guard that
// merely returns before any HTTP call would not exercise this at all.

func TestNoTokenLeak_EnsureRepoUpstreamFailureBody(t *testing.T) {
	const token = "ghs_super-secret-installation-token-999"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/orgs/acme/repos":
			// The realistic hostile case: GitHub's error payload happens to
			// echo back the Authorization header it received.
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"bad credentials","auth":"` + token + `"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(srv, token)

	_, err := c.EnsureRepo(context.Background(), "acme", "widget")
	if err == nil {
		t.Fatal("expected the upstream 422 to surface as an error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("EnsureRepo error leaked the installation token: %v", err)
	}
}

func TestNoTokenLeak_CommitFilesUpstreamFailureBody(t *testing.T) {
	const token = "ghs_super-secret-installation-token-999"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget/git/ref/heads/main":
			w.WriteHeader(http.StatusNotFound) // treat as a fresh, empty repo
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widget/git/trees":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"rejected for token ` + token + `"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(srv, token)

	err := c.CommitFiles(context.Background(), "acme/widget", "seed", map[string]string{"a.txt": "x"})
	if err == nil {
		t.Fatal("expected the upstream 422 to surface as an error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("CommitFiles error leaked the installation token: %v", err)
	}
}

func TestNoTokenLeak_SetVariableUpstreamFailureBody(t *testing.T) {
	const token = "ghs_super-secret-installation-token-999"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"upstream failure","request_token":"` + token + `"}`))
	}))
	defer srv.Close()
	c := newTestClient(srv, token)

	err := c.SetVariable(context.Background(), "acme/widget", "X", "y")
	if err == nil {
		t.Fatal("expected the upstream 500 to surface as an error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("SetVariable error leaked the installation token: %v", err)
	}
}

// --- CommitFiles: a lost fast-forward race replays onto the new head ---------

const fastForwardRefusal = `{"message":"Update is not a fast forward"}`

type fakeGitRequest struct {
	Repo   string
	Method string
	Path   string
	Body   map[string]any
}

type fakeGitRepo struct {
	gen      int
	empty    bool
	refusals int

	loseRaces   int
	raceBody    string
	patchStatus int
	patchBody   string
	treeSHA     func(baseTree string) string
	onRefusal   func(f *fakeGitHub, repo string)
}

type fakeGitHub struct {
	t     *testing.T
	mu    sync.Mutex
	repos map[string]*fakeGitRepo
	reqs  []fakeGitRequest
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	return &fakeGitHub{t: t, repos: map[string]*fakeGitRepo{}}
}

func (f *fakeGitHub) add(fullName string, r *fakeGitRepo) { f.repos[fullName] = r }

// remove is called from onRefusal while ServeHTTP already holds f.mu.
func (f *fakeGitHub) remove(fullName string) { delete(f.repos, fullName) }

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/repos/"), "/", 3)
	if !strings.HasPrefix(r.URL.Path, "/repos/") || len(parts) != 3 {
		f.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	full, rest := parts[0]+"/"+parts[1], parts[2]
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	f.reqs = append(f.reqs, fakeGitRequest{Repo: full, Method: r.Method, Path: rest, Body: body})

	repo, ok := f.repos[full]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		return
	}
	headSHA := fmt.Sprintf("main-sha-gen-%d", repo.gen)
	headTree := fmt.Sprintf("main-tree-gen-%d", repo.gen)

	switch {
	case r.Method == http.MethodGet && rest == "git/ref/heads/main":
		if repo.empty {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": headSHA}})
	case r.Method == http.MethodGet && rest == "git/commits/"+headSHA:
		_ = json.NewEncoder(w).Encode(map[string]any{"tree": map[string]string{"sha": headTree}})
	case r.Method == http.MethodPost && rest == "git/trees":
		base, _ := body["base_tree"].(string)
		sha := "tree-over-" + base
		if repo.treeSHA != nil {
			sha = repo.treeSHA(base)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": sha})
	case r.Method == http.MethodPost && rest == "git/commits":
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "new-commit-sha"})
	case r.Method == http.MethodPost && rest == "git/refs":
		repo.empty = false
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/main", "object": map[string]any{"sha": body["sha"]}})
	case r.Method == http.MethodPatch && rest == "git/refs/heads/main":
		if repo.patchStatus != 0 {
			w.WriteHeader(repo.patchStatus)
			_, _ = w.Write([]byte(repo.patchBody))
			return
		}
		if repo.refusals < repo.loseRaces {
			repo.refusals++
			repo.gen++
			raceBody := repo.raceBody
			if raceBody == "" {
				raceBody = fastForwardRefusal
			}
			if repo.onRefusal != nil {
				repo.onRefusal(f, full)
			}
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(raceBody))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/main", "object": map[string]any{"sha": body["sha"]}})
	default:
		f.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeGitHub) requests(repo, method, path string) []fakeGitRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeGitRequest
	for _, r := range f.reqs {
		if r.Repo == repo && (method == "" || r.Method == method) && (path == "" || r.Path == path) {
			out = append(out, r)
		}
	}
	return out
}

func refGets(n int) *int { return &n }

func TestCommitFiles_ReplaysALostFastForwardRaceOntoTheNewHead(t *testing.T) {
	const (
		seeded = "acme/widget"
		other  = "acme/other"
		token  = "ghs_replay-secret-token-777"
	)
	files := map[string]string{"a.txt": "alpha", "b/c.txt": "charlie"}

	cases := []struct {
		name  string
		setup func(f *fakeGitHub)

		wantErr         bool
		wantErrContains string

		wantPatches     int
		wantRefPosts    int
		wantCommitPosts int
		wantRefGets     *int
		check           func(t *testing.T, f *fakeGitHub)
	}{
		{
			name:        "one lost race replays to success on the new head",
			setup:       func(f *fakeGitHub) { f.add(seeded, &fakeGitRepo{loseRaces: 1}) },
			wantPatches: 2, wantCommitPosts: 2, wantRefGets: refGets(2),
			check: func(t *testing.T, f *fakeGitHub) {
				trees := f.requests(seeded, http.MethodPost, "git/trees")
				if len(trees) != 2 {
					t.Fatalf("tree POSTs = %d, want 2", len(trees))
				}
				if got := trees[0].Body["base_tree"]; got != "main-tree-gen-0" {
					t.Errorf("first tree base_tree = %v, want main-tree-gen-0", got)
				}
				if got := trees[1].Body["base_tree"]; got != "main-tree-gen-1" {
					t.Errorf("replayed tree base_tree = %v, want the new head's tree main-tree-gen-1", got)
				}
				if !reflect.DeepEqual(trees[0].Body["tree"], trees[1].Body["tree"]) {
					t.Errorf("replayed tree entries = %v, want the first attempt's %v", trees[1].Body["tree"], trees[0].Body["tree"])
				}
				commits := f.requests(seeded, http.MethodPost, "git/commits")
				if len(commits) != 2 {
					t.Fatalf("commit POSTs = %d, want 2", len(commits))
				}
				if got := commits[0].Body["parents"]; !reflect.DeepEqual(got, []any{"main-sha-gen-0"}) {
					t.Errorf("first commit parents = %v, want [main-sha-gen-0]", got)
				}
				if got := commits[1].Body["parents"]; !reflect.DeepEqual(got, []any{"main-sha-gen-1"}) {
					t.Errorf("replayed commit parents = %v, want exactly the new head [main-sha-gen-1]", got)
				}
				if got := commits[1].Body["tree"]; got != "tree-over-main-tree-gen-1" {
					t.Errorf("replayed commit tree = %v, want the rebuilt tree tree-over-main-tree-gen-1", got)
				}
				for i, p := range f.requests(seeded, http.MethodPatch, "git/refs/heads/main") {
					if got := p.Body["sha"]; got != "new-commit-sha" {
						t.Errorf("PATCH #%d sha = %v, want new-commit-sha", i+1, got)
					}
				}
			},
		},
		{
			name: "the fast forward match is case-insensitive",
			setup: func(f *fakeGitHub) {
				f.add(seeded, &fakeGitRepo{loseRaces: 1, raceBody: `{"message":"UPDATE IS NOT A FAST FORWARD"}`})
			},
			wantPatches: 2, wantCommitPosts: 2, wantRefGets: refGets(2),
		},
		{
			name:    "a main that never stops moving fails after three PATCHes",
			setup:   func(f *fakeGitHub) { f.add(seeded, &fakeGitRepo{loseRaces: 1 << 20}) },
			wantErr: true, wantErrContains: "fast forward",
			wantPatches: 3, wantCommitPosts: 3, wantRefGets: refGets(3),
		},
		{
			name: "the exhausted-replay error never carries the token",
			setup: func(f *fakeGitHub) {
				f.add(seeded, &fakeGitRepo{loseRaces: 1 << 20, raceBody: `{"message":"Update is not a fast forward","auth":"` + token + `"}`})
			},
			wantErr: true, wantErrContains: "fast forward",
			wantPatches: 3, wantCommitPosts: 3, wantRefGets: refGets(3),
		},
		{
			name: "a protected-branch 422 fails at once without carrying the token",
			setup: func(f *fakeGitHub) {
				f.add(seeded, &fakeGitRepo{patchStatus: http.StatusUnprocessableEntity, patchBody: `{"message":"Required status check \"build\" is expected.","auth":"` + token + `"}`})
			},
			wantErr:     true,
			wantPatches: 1, wantCommitPosts: 1, wantRefGets: refGets(1),
		},
		{
			name: "a validation-failed 422 fails at once without carrying the token",
			setup: func(f *fakeGitHub) {
				f.add(seeded, &fakeGitRepo{patchStatus: http.StatusUnprocessableEntity, patchBody: `{"message":"Validation Failed","errors":[{"resource":"Ref","code":"custom","field":"sha"}],"auth":"` + token + `"}`})
			},
			wantErr:     true,
			wantPatches: 1, wantCommitPosts: 1, wantRefGets: refGets(1),
		},
		{
			name: "a 500 fails at once without carrying the token",
			setup: func(f *fakeGitHub) {
				f.add(seeded, &fakeGitRepo{patchStatus: http.StatusInternalServerError, patchBody: `{"message":"Server Error","auth":"` + token + `"}`})
			},
			wantErr:     true,
			wantPatches: 1, wantCommitPosts: 1, wantRefGets: refGets(1),
		},
		{
			name:        "an empty repo creates the ref once and never PATCHes",
			setup:       func(f *fakeGitHub) { f.add(seeded, &fakeGitRepo{empty: true, loseRaces: 1 << 20}) },
			wantPatches: 0, wantRefPosts: 1, wantCommitPosts: 1, wantRefGets: refGets(1),
			check: func(t *testing.T, f *fakeGitHub) {
				trees := f.requests(seeded, http.MethodPost, "git/trees")
				if len(trees) != 1 {
					t.Fatalf("tree POSTs = %d, want 1", len(trees))
				}
				if v, ok := trees[0].Body["base_tree"]; ok {
					t.Errorf("empty-repo tree carried base_tree %v, want none", v)
				}
				commits := f.requests(seeded, http.MethodPost, "git/commits")
				if v, ok := commits[0].Body["parents"]; ok {
					t.Errorf("empty-repo commit carried parents %v, want none", v)
				}
				refs := f.requests(seeded, http.MethodPost, "git/refs")
				if got := refs[0].Body["ref"]; got != "refs/heads/main" {
					t.Errorf("created ref = %v, want refs/heads/main", got)
				}
				if got := refs[0].Body["sha"]; got != "new-commit-sha" {
					t.Errorf("created ref sha = %v, want new-commit-sha", got)
				}
			},
		},
		{
			name: "main vanishing mid-replay errors without creating a ref",
			setup: func(f *fakeGitHub) {
				f.add(seeded, &fakeGitRepo{loseRaces: 1, onRefusal: func(f *fakeGitHub, repo string) { f.remove(repo) }})
			},
			wantErr:     true,
			wantPatches: 1, wantRefPosts: 0, wantCommitPosts: 1, wantRefGets: refGets(2),
		},
		{
			name: "a replay whose rebuilt tree equals the new head's tree is a no-op",
			setup: func(f *fakeGitHub) {
				f.add(seeded, &fakeGitRepo{loseRaces: 1, treeSHA: func(base string) string {
					if base == "main-tree-gen-1" {
						return base
					}
					return "tree-over-" + base
				}})
			},
			wantPatches: 1, wantCommitPosts: 1, wantRefGets: refGets(2),
			check: func(t *testing.T, f *fakeGitHub) {
				trees := f.requests(seeded, http.MethodPost, "git/trees")
				if len(trees) != 2 {
					t.Fatalf("tree POSTs = %d, want 2: the replay rebuilds before deciding it is a no-op", len(trees))
				}
				if got := trees[1].Body["base_tree"]; got != "main-tree-gen-1" {
					t.Errorf("replayed tree base_tree = %v, want main-tree-gen-1", got)
				}
			},
		},
		{
			name: "a replay never touches another repository",
			setup: func(f *fakeGitHub) {
				f.add(seeded, &fakeGitRepo{loseRaces: 1})
				f.add(other, &fakeGitRepo{gen: 7, loseRaces: 1})
			},
			wantPatches: 2, wantCommitPosts: 2, wantRefGets: refGets(2),
			check: func(t *testing.T, f *fakeGitHub) {
				if reqs := f.requests(other, "", ""); len(reqs) != 0 {
					t.Errorf("requests to %s = %v, want none", other, reqs)
				}
				if r := f.repos[other]; r.gen != 7 || r.refusals != 0 {
					t.Errorf("%s state changed: gen=%d refusals=%d, want gen=7 refusals=0", other, r.gen, r.refusals)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeGitHub(t)
			c.setup(f)
			srv := httptest.NewServer(f)
			defer srv.Close()
			cl := newTestClient(srv, token)

			err := cl.CommitFiles(context.Background(), seeded, "seed", files)

			if c.wantErr {
				if err == nil {
					t.Fatal("CommitFiles succeeded, want an error")
				}
				if c.wantErrContains != "" && !strings.Contains(err.Error(), c.wantErrContains) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), c.wantErrContains)
				}
				if strings.Contains(err.Error(), token) {
					t.Errorf("error leaked the installation token: %v", err)
				}
			} else if err != nil {
				t.Fatalf("CommitFiles: %v", err)
			}

			if got := len(f.requests(seeded, http.MethodPatch, "git/refs/heads/main")); got != c.wantPatches {
				t.Errorf("PATCH refs/heads/main = %d, want %d", got, c.wantPatches)
			}
			if got := len(f.requests(seeded, http.MethodPost, "git/refs")); got != c.wantRefPosts {
				t.Errorf("POST git/refs = %d, want %d", got, c.wantRefPosts)
			}
			if got := len(f.requests(seeded, http.MethodPost, "git/commits")); got != c.wantCommitPosts {
				t.Errorf("POST git/commits = %d, want %d", got, c.wantCommitPosts)
			}
			if c.wantRefGets != nil {
				if got := len(f.requests(seeded, http.MethodGet, "git/ref/heads/main")); got != *c.wantRefGets {
					t.Errorf("GET git/ref/heads/main = %d, want %d", got, *c.wantRefGets)
				}
			}
			for _, r := range f.reqs {
				if r.Repo != seeded {
					t.Errorf("request outside the seeded repository: %s %s/%s", r.Method, r.Repo, r.Path)
				}
			}
			if c.check != nil {
				c.check(t, f)
			}
		})
	}
}
