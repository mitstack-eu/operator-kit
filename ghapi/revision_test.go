package ghapi_test

// revision_test.go pins the revision-addressed GitHub calls a GitOps
// repository is built on, which sit beside CommitFiles and leave it as it is:
//
//   - HeadCommit returns the commit refs/heads/main points at, or "" when the
//     repository has no main yet;
//   - ReadFileAt returns a file's content at one commit through the contents
//     API, with a 404 meaning the path is absent;
//   - CommitChanges lands one commit on a named parent, writing and deleting
//     paths, creating main when the parent is "", returning the parent when
//     the tree is unchanged, and wrapping ErrNotFastForward without any
//     replay when the parent is no longer the tip;
//   - CommitParentAndPaths returns a commit's first parent and the sorted
//     paths it touched, a rename naming both of its paths.
//
// Every call validates the repository through SafeRepo before any request,
// and no error ever carries the token, even when GitHub's body echoes it.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/ghapi"
)

const (
	revRepo  = "acme/gitops"
	revToken = "ghs_revisionTestToken0123456789"
)

func strp(s string) *string { return &s }

// echoTokenBody is a GitHub error body that echoes the caller's token, as a
// proxy or a misconfigured enterprise host might.
func echoTokenBody(r *http.Request) string {
	return `{"message":"failure for ` + r.Header.Get("Authorization") + `"}`
}

// wantNoToken fails when err is nil or carries the token.
func wantNoToken(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if strings.Contains(err.Error(), revToken) {
		t.Errorf("the error carries the token: %v", err)
	}
}

// --- HeadCommit --------------------------------------------------------------

func isMainRefRead(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		(r.URL.Path == "/repos/acme/gitops/git/ref/heads/main" || r.URL.Path == "/repos/acme/gitops/git/refs/heads/main")
}

func TestHeadCommit_ReturnsTheCommitMainPointsAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMainRefRead(r) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+revToken {
			t.Errorf("Authorization = %q, want the client's bearer token", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/main", "object": map[string]string{"sha": "tip-sha", "type": "commit"}})
	}))
	defer srv.Close()

	head, err := newTestClient(srv, revToken).HeadCommit(context.Background(), revRepo)
	if err != nil {
		t.Fatalf("HeadCommit: %v", err)
	}
	if head != "tip-sha" {
		t.Errorf("HeadCommit = %q, want %q", head, "tip-sha")
	}
}

func TestHeadCommit_IsEmptyWhenTheRepositoryHasNoMainYet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMainRefRead(r) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	head, err := newTestClient(srv, revToken).HeadCommit(context.Background(), revRepo)
	if err != nil {
		t.Fatalf("HeadCommit on a repository without main: %v, want no error", err)
	}
	if head != "" {
		t.Errorf("HeadCommit = %q, want \"\" for a repository without main", head)
	}
}

func TestHeadCommit_AnyOtherStatusIsAnErrorThatNeverCarriesTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(echoTokenBody(r)))
	}))
	defer srv.Close()

	_, err := newTestClient(srv, revToken).HeadCommit(context.Background(), revRepo)
	wantNoToken(t, err)
}

// --- ReadFileAt --------------------------------------------------------------

// wrappedBase64 encodes content the way the contents API does, broken into
// 60-character lines.
func wrappedBase64(content string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(content))
	var b strings.Builder
	for len(enc) > 60 {
		b.WriteString(enc[:60])
		b.WriteString("\n")
		enc = enc[60:]
	}
	b.WriteString(enc)
	b.WriteString("\n")
	return b.String()
}

func TestReadFileAt_ReturnsTheDecodedContentAtTheRevision(t *testing.T) {
	content := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: app-alpha\n  annotations:\n    mitstack.dev/cluster: k3s-fsn1\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/gitops/contents/apps/app-alpha/namespace.yaml" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.URL.Query().Get("ref"); got != shaRev1 {
			t.Errorf("ref = %q, want the revision %q", got, shaRev1)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"type": "file", "encoding": "base64", "path": "apps/app-alpha/namespace.yaml", "content": wrappedBase64(content),
		})
	}))
	defer srv.Close()

	got, ok, err := newTestClient(srv, revToken).ReadFileAt(context.Background(), revRepo, shaRev1, "apps/app-alpha/namespace.yaml")
	if err != nil {
		t.Fatalf("ReadFileAt: %v", err)
	}
	if !ok {
		t.Fatal("ReadFileAt reported the file absent, want present")
	}
	if string(got) != content {
		t.Errorf("ReadFileAt = %q, want %q", got, content)
	}
}

func TestReadFileAt_A404IsAnAbsentPathAndNoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	got, ok, err := newTestClient(srv, revToken).ReadFileAt(context.Background(), revRepo, shaRev1, "apps/app-alpha/gone.yaml")
	if err != nil {
		t.Fatalf("ReadFileAt on a 404: %v, want no error", err)
	}
	if ok || got != nil {
		t.Errorf("ReadFileAt on a 404 = (%q, %v), want (nil, false)", got, ok)
	}
}

func TestReadFileAt_AnyOtherStatusIsAnErrorThatNeverCarriesTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(echoTokenBody(r)))
	}))
	defer srv.Close()

	got, ok, err := newTestClient(srv, revToken).ReadFileAt(context.Background(), revRepo, shaRev1, "a.yaml")
	wantNoToken(t, err)
	if ok || got != nil {
		t.Errorf("ReadFileAt on a 403 = (%q, %v), want (nil, false) with the error", got, ok)
	}
}

func TestReadFileAt_EscapesEachPathSegmentAndTheRevision(t *testing.T) {
	var gotPath string
	var gotRefs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRefs = r.URL.Query()["ref"]
		_ = json.NewEncoder(w).Encode(map[string]string{"type": "file", "encoding": "base64", "content": wrappedBase64("x")})
	}))
	defer srv.Close()

	path := "apps/my app/a#b?c=d.yaml"
	revision := shaRev1
	if _, _, err := newTestClient(srv, revToken).ReadFileAt(context.Background(), revRepo, revision, path); err != nil {
		t.Fatalf("ReadFileAt: %v", err)
	}
	if want := "/repos/acme/gitops/contents/" + path; gotPath != want {
		t.Errorf("the request reached path %q, want %q: each segment must be escaped so none is cut off or turned into a query", gotPath, want)
	}
	if !reflect.DeepEqual(gotRefs, []string{revision}) {
		t.Errorf("ref query = %q, want exactly [%q]", gotRefs, revision)
	}
}

// --- CommitChanges -----------------------------------------------------------

// gitData is a scripted Git Data API for one repository. It answers the main
// ref from tip, a commit's tree from trees, a tree POST with treeSHA and a
// commit POST with newCommit, and records every write it receives.
type gitData struct {
	t *testing.T

	mu        sync.Mutex
	tip       string
	trees     map[string]string
	treeSHA   string
	newCommit string

	patchStatus int
	patchBody   string
	refStatus   int
	refBody     string

	requests    int
	treePosts   []map[string]any
	commitPosts []map[string]any
	patches     []map[string]any
	refPosts    []map[string]any
}

func newGitData(t *testing.T, tip string) *gitData {
	return &gitData{
		t:         t,
		tip:       tip,
		trees:     map[string]string{},
		treeSHA:   "new-tree-sha",
		newCommit: "new-commit-sha",
	}
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("decoding %s %s: %v", r.Method, r.URL.Path, err)
	}
	return body
}

func (g *gitData) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests++
	const prefix = "/repos/acme/gitops/git/"
	switch {
	case isMainRefRead(r):
		if g.tip == "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/main", "object": map[string]string{"sha": g.tip, "type": "commit"}})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, prefix+"commits/"):
		sha := strings.TrimPrefix(r.URL.Path, prefix+"commits/")
		tree, ok := g.trees[sha]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": sha, "tree": map[string]string{"sha": tree}, "parents": []any{}})
	case r.Method == http.MethodPost && r.URL.Path == prefix+"trees":
		g.treePosts = append(g.treePosts, decodeBody(g.t, r))
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": g.treeSHA})
	case r.Method == http.MethodPost && r.URL.Path == prefix+"commits":
		g.commitPosts = append(g.commitPosts, decodeBody(g.t, r))
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": g.newCommit})
	case r.Method == http.MethodPatch && r.URL.Path == prefix+"refs/heads/main":
		g.patches = append(g.patches, decodeBody(g.t, r))
		if g.patchStatus != 0 {
			w.WriteHeader(g.patchStatus)
			_, _ = w.Write([]byte(strings.ReplaceAll(g.patchBody, "{auth}", r.Header.Get("Authorization"))))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/main", "object": map[string]string{"sha": g.newCommit}})
	case r.Method == http.MethodPost && r.URL.Path == prefix+"refs":
		g.refPosts = append(g.refPosts, decodeBody(g.t, r))
		if g.refStatus != 0 {
			w.WriteHeader(g.refStatus)
			_, _ = w.Write([]byte(g.refBody))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/main", "object": map[string]string{"sha": g.newCommit}})
	default:
		g.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (g *gitData) serve(t *testing.T) *ghapi.Client {
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return newTestClient(srv, revToken)
}

// treeEntries returns the entries of one tree POST keyed by path.
func treeEntries(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	raw, ok := body["tree"].([]any)
	if !ok {
		t.Fatalf("tree POST carries no tree array: %v", body)
	}
	out := map[string]map[string]any{}
	for _, e := range raw {
		entry, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("tree entry %v is not an object", e)
		}
		path, _ := entry["path"].(string)
		out[path] = entry
	}
	return out
}

func TestCommitChanges_LandsOneCommitOnTheParentWritingAndDeletingPaths(t *testing.T) {
	g := newGitData(t, shaParent)
	g.trees[shaParent] = "parent-tree"
	c := g.serve(t)

	rev, err := c.CommitChanges(context.Background(), revRepo, shaParent, "app-alpha: update", map[string]*string{
		"apps/app-alpha/deploy.yaml": strp("image: v2\n"),
		"apps/app-alpha/old.yaml":    nil,
	})
	if err != nil {
		t.Fatalf("CommitChanges: %v", err)
	}
	if rev != "new-commit-sha" {
		t.Errorf("CommitChanges = %q, want the new commit %q", rev, "new-commit-sha")
	}

	if len(g.treePosts) != 1 {
		t.Fatalf("tree POSTs = %d, want 1", len(g.treePosts))
	}
	if got := g.treePosts[0]["base_tree"]; got != "parent-tree" {
		t.Errorf("base_tree = %v, want the parent's tree %q", got, "parent-tree")
	}
	entries := treeEntries(t, g.treePosts[0])
	if len(entries) != 2 {
		t.Errorf("tree entries = %v, want exactly the two changed paths", entries)
	}
	write := entries["apps/app-alpha/deploy.yaml"]
	if write["content"] != "image: v2\n" || write["mode"] != "100644" || write["type"] != "blob" {
		t.Errorf("written entry = %v, want a 100644 blob carrying the content", write)
	}
	del, ok := entries["apps/app-alpha/old.yaml"]
	if !ok {
		t.Fatal("the deleted path has no tree entry")
	}
	if sha, present := del["sha"]; !present || sha != nil {
		t.Errorf("deleted entry = %v, want \"sha\": null", del)
	}
	if _, present := del["content"]; present {
		t.Errorf("deleted entry = %v, want no content", del)
	}

	if len(g.commitPosts) != 1 {
		t.Fatalf("commit POSTs = %d, want 1", len(g.commitPosts))
	}
	commit := g.commitPosts[0]
	if commit["tree"] != "new-tree-sha" || commit["message"] != "app-alpha: update" || !reflect.DeepEqual(commit["parents"], []any{shaParent}) {
		t.Errorf("commit POST = %v, want tree %q, the message and parents [parent-sha]", commit, "new-tree-sha")
	}
	if len(g.patches) != 1 {
		t.Fatalf("ref PATCHes = %d, want 1", len(g.patches))
	}
	if g.patches[0]["sha"] != "new-commit-sha" {
		t.Errorf("ref PATCH = %v, want sha %q", g.patches[0], "new-commit-sha")
	}
	if force, _ := g.patches[0]["force"].(bool); force {
		t.Error("ref PATCH forces the update, want a fast forward only")
	}
	if len(g.refPosts) != 0 {
		t.Errorf("ref POSTs = %d, want 0 when main exists", len(g.refPosts))
	}
}

func TestCommitChanges_AnEmptyParentCreatesMain(t *testing.T) {
	g := newGitData(t, "")
	c := g.serve(t)

	rev, err := c.CommitChanges(context.Background(), revRepo, "", "first", map[string]*string{"a.yaml": strp("a\n")})
	if err != nil {
		t.Fatalf("CommitChanges: %v", err)
	}
	if rev != "new-commit-sha" {
		t.Errorf("CommitChanges = %q, want the new commit %q", rev, "new-commit-sha")
	}
	if len(g.treePosts) != 1 {
		t.Fatalf("tree POSTs = %d, want 1", len(g.treePosts))
	}
	if bt, present := g.treePosts[0]["base_tree"]; present && bt != nil && bt != "" {
		t.Errorf("base_tree = %v, want none for the first commit", bt)
	}
	if entries := treeEntries(t, g.treePosts[0]); entries["a.yaml"]["content"] != "a\n" {
		t.Errorf("tree entries = %v, want a.yaml carrying its content", entries)
	}
	if len(g.commitPosts) != 1 {
		t.Fatalf("commit POSTs = %d, want 1", len(g.commitPosts))
	}
	if parents, _ := g.commitPosts[0]["parents"].([]any); len(parents) != 0 {
		t.Errorf("commit parents = %v, want none for the first commit", parents)
	}
	if len(g.refPosts) != 1 || g.refPosts[0]["ref"] != "refs/heads/main" || g.refPosts[0]["sha"] != "new-commit-sha" {
		t.Errorf("ref POSTs = %v, want one creating refs/heads/main at the new commit", g.refPosts)
	}
	if len(g.patches) != 0 {
		t.Errorf("ref PATCHes = %d, want 0 when creating main", len(g.patches))
	}
}

func TestCommitChanges_AnUnchangedTreeMakesNoCommitAndReturnsTheParent(t *testing.T) {
	g := newGitData(t, shaParent)
	g.trees[shaParent] = "parent-tree"
	g.treeSHA = "parent-tree"
	c := g.serve(t)

	rev, err := c.CommitChanges(context.Background(), revRepo, shaParent, "noop", map[string]*string{"a.yaml": strp("same\n")})
	if err != nil {
		t.Fatalf("CommitChanges: %v", err)
	}
	if rev != shaParent {
		t.Errorf("CommitChanges = %q, want the parent %q", rev, shaParent)
	}
	if len(g.commitPosts) != 0 || len(g.patches) != 0 || len(g.refPosts) != 0 {
		t.Errorf("commit POSTs %d, ref PATCHes %d, ref POSTs %d, want none for an unchanged tree", len(g.commitPosts), len(g.patches), len(g.refPosts))
	}
}

// wantNoReplay checks a refused commit wrote at most one tree, one commit
// and one ref update, and created no ref.
func wantNoReplay(t *testing.T, g *gitData) {
	t.Helper()
	if len(g.treePosts) > 1 || len(g.commitPosts) > 1 || len(g.patches) > 1 {
		t.Errorf("tree POSTs %d, commit POSTs %d, ref PATCHes %d, want at most one each: CommitChanges must not replay",
			len(g.treePosts), len(g.commitPosts), len(g.patches))
	}
}

func TestCommitChanges_AParentThatIsNotTheTipWrapsErrNotFastForwardAndDoesNotReplay(t *testing.T) {
	for _, tc := range []struct {
		name string
		tip  string
	}{
		{"the tip already moved when read", "other-writer-sha"},
		{"the tip moved between the read and the ref update", shaParent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGitData(t, tc.tip)
			g.trees[shaParent] = "parent-tree"
			g.trees["other-writer-sha"] = "other-tree"
			g.patchStatus = http.StatusUnprocessableEntity
			g.patchBody = `{"message":"Update is not a fast forward","documentation_url":"https://docs.github.com/rest/git/refs#update-a-reference"}`
			c := g.serve(t)

			rev, err := c.CommitChanges(context.Background(), revRepo, shaParent, "app-alpha: update", map[string]*string{"a.yaml": strp("a2\n")})
			if !errors.Is(err, ghapi.ErrNotFastForward) {
				t.Fatalf("CommitChanges = (%q, %v), want an error wrapping ghapi.ErrNotFastForward", rev, err)
			}
			wantNoReplay(t, g)
			if len(g.refPosts) != 0 {
				t.Errorf("ref POSTs = %d, want 0", len(g.refPosts))
			}
		})
	}
}

func TestCommitChanges_AnEmptyParentWhenMainExistsWrapsErrNotFastForward(t *testing.T) {
	for _, tc := range []struct {
		name string
		tip  string
	}{
		{"main already exists when read", "other-writer-sha"},
		{"main was created between the read and the ref create", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGitData(t, tc.tip)
			g.trees["other-writer-sha"] = "other-tree"
			g.refStatus = http.StatusUnprocessableEntity
			g.refBody = `{"message":"Reference already exists"}`
			c := g.serve(t)

			rev, err := c.CommitChanges(context.Background(), revRepo, "", "first", map[string]*string{"a.yaml": strp("a\n")})
			if !errors.Is(err, ghapi.ErrNotFastForward) {
				t.Fatalf("CommitChanges = (%q, %v), want an error wrapping ghapi.ErrNotFastForward", rev, err)
			}
			wantNoReplay(t, g)
			if len(g.patches) != 0 {
				t.Errorf("ref PATCHes = %d, want 0: an empty parent never moves an existing main", len(g.patches))
			}
		})
	}
}

func TestCommitChanges_AnotherRefusalIsAnErrorButNotErrNotFastForward(t *testing.T) {
	g := newGitData(t, shaParent)
	g.trees[shaParent] = "parent-tree"
	g.patchStatus = http.StatusUnprocessableEntity
	g.patchBody = `{"message":"Object does not exist"}`
	c := g.serve(t)

	_, err := c.CommitChanges(context.Background(), revRepo, shaParent, "m", map[string]*string{"a.yaml": strp("a\n")})
	if err == nil {
		t.Fatal("CommitChanges = nil error, want the refusal")
	}
	if errors.Is(err, ghapi.ErrNotFastForward) {
		t.Errorf("CommitChanges = %v, want an error that does not wrap ErrNotFastForward: only a fast forward refusal is a stale parent", err)
	}
}

func TestCommitChanges_AnUnknownParentIsAnErrorAndMovesNoRef(t *testing.T) {
	g := newGitData(t, "tip-sha")
	g.trees["tip-sha"] = "tip-tree"
	c := g.serve(t)

	if _, err := c.CommitChanges(context.Background(), revRepo, shaUnknown, "m", map[string]*string{"a.yaml": strp("a\n")}); err == nil {
		t.Fatal("CommitChanges on an unknown parent = nil error")
	}
	if len(g.patches) != 0 || len(g.refPosts) != 0 {
		t.Errorf("ref PATCHes %d, ref POSTs %d, want none", len(g.patches), len(g.refPosts))
	}
}

func TestCommitChanges_ErrorsNeverCarryTheToken(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"a fast forward refusal echoing the token", http.StatusUnprocessableEntity, `{"message":"Update is not a fast forward for {auth}"}`},
		{"a server error echoing the token", http.StatusInternalServerError, `{"message":"boom {auth}"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGitData(t, shaParent)
			g.trees[shaParent] = "parent-tree"
			g.patchStatus = tc.status
			g.patchBody = tc.body
			c := g.serve(t)

			_, err := c.CommitChanges(context.Background(), revRepo, shaParent, "m", map[string]*string{"a.yaml": strp("a\n")})
			wantNoToken(t, err)
		})
	}
}

func TestCommitChanges_ATreeRefusalEchoingTheTokenNeverCarriesIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case isMainRefRead(r):
			_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": shaParent}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/gitops/git/commits/"+shaParent:
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": shaParent, "tree": map[string]string{"sha": "parent-tree"}})
		default:
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(echoTokenBody(r)))
		}
	}))
	defer srv.Close()

	_, err := newTestClient(srv, revToken).CommitChanges(context.Background(), revRepo, shaParent, "m", map[string]*string{"a.yaml": strp("a\n")})
	wantNoToken(t, err)
}

// --- CommitParentAndPaths ----------------------------------------------------

func commitsServer(t *testing.T, revision string, status int, body any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/gitops/commits/"+revision {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestCommitParentAndPaths_ReturnsTheFirstParentAndTheSortedPaths(t *testing.T) {
	srv := commitsServer(t, shaRev2, http.StatusOK, map[string]any{
		"sha":     shaRev2,
		"parents": []map[string]string{{"sha": "rev-1"}, {"sha": "merged-sha"}},
		"files": []map[string]string{
			{"filename": "b.yaml", "status": "modified"},
			{"filename": "apps/app-alpha/z.yaml", "status": "added"},
			{"filename": "a.yaml", "status": "removed"},
		},
	})
	defer srv.Close()

	parent, paths, err := newTestClient(srv, revToken).CommitParentAndPaths(context.Background(), revRepo, shaRev2)
	if err != nil {
		t.Fatalf("CommitParentAndPaths: %v", err)
	}
	if parent != "rev-1" {
		t.Errorf("parent = %q, want the first parent %q", parent, "rev-1")
	}
	if want := []string{"a.yaml", "apps/app-alpha/z.yaml", "b.yaml"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %q, want %q", paths, want)
	}
}

func TestCommitParentAndPaths_TheParentOfARootCommitIsEmpty(t *testing.T) {
	srv := commitsServer(t, shaRev1, http.StatusOK, map[string]any{
		"sha":     shaRev1,
		"parents": []map[string]string{},
		"files":   []map[string]string{{"filename": "a.yaml", "status": "added"}},
	})
	defer srv.Close()

	parent, paths, err := newTestClient(srv, revToken).CommitParentAndPaths(context.Background(), revRepo, shaRev1)
	if err != nil {
		t.Fatalf("CommitParentAndPaths: %v", err)
	}
	if parent != "" {
		t.Errorf("parent = %q, want \"\" for a root commit", parent)
	}
	if !reflect.DeepEqual(paths, []string{"a.yaml"}) {
		t.Errorf("paths = %q, want [a.yaml]", paths)
	}
}

func TestCommitParentAndPaths_ARenameListsBothItsPaths(t *testing.T) {
	srv := commitsServer(t, shaRev2, http.StatusOK, map[string]any{
		"sha":     shaRev2,
		"parents": []map[string]string{{"sha": "rev-1"}},
		"files": []map[string]string{
			{"filename": "apps/app-alpha/new.yaml", "status": "renamed", "previous_filename": "apps/app-alpha/old.yaml"},
		},
	})
	defer srv.Close()

	_, paths, err := newTestClient(srv, revToken).CommitParentAndPaths(context.Background(), revRepo, shaRev2)
	if err != nil {
		t.Fatalf("CommitParentAndPaths: %v", err)
	}
	if want := []string{"apps/app-alpha/new.yaml", "apps/app-alpha/old.yaml"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %q, want both sides of the rename %q", paths, want)
	}
}

func TestCommitParentAndPaths_ARevisionTheRepositoryNeverHeldIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"422 no commit found", http.StatusUnprocessableEntity},
		{"404", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := commitsServer(t, shaUnknown, tc.status, map[string]string{"message": "No commit found for SHA: no-such-sha"})
			defer srv.Close()

			if _, _, err := newTestClient(srv, revToken).CommitParentAndPaths(context.Background(), revRepo, shaUnknown); err == nil {
				t.Error("CommitParentAndPaths of an unknown revision = nil error")
			}
		})
	}
}

func TestCommitParentAndPaths_ErrorsNeverCarryTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(echoTokenBody(r)))
	}))
	defer srv.Close()

	_, _, err := newTestClient(srv, revToken).CommitParentAndPaths(context.Background(), revRepo, shaRev2)
	wantNoToken(t, err)
}

func TestCommitParentAndPaths_EscapesTheRevisionAsOneSegment(t *testing.T) {
	var escaped []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped = append(escaped, r.URL.EscapedPath())
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "x", "parents": []any{}, "files": []any{}})
	}))
	defer srv.Close()

	_, _, err := newTestClient(srv, revToken).CommitParentAndPaths(context.Background(), revRepo, "x/../../orgs?y")
	for _, p := range escaped {
		if p != "/repos/acme/gitops/commits/x%2F..%2F..%2Forgs%3Fy" {
			t.Errorf("the request reached %q, want the revision escaped as one segment of /repos/acme/gitops/commits/", p)
		}
	}
	if len(escaped) == 0 && err == nil {
		t.Error("CommitParentAndPaths made no request and returned no error")
	}
}

// --- Every call validates the repository first --------------------------------

func TestRevisionCalls_RefuseAnInvalidRepositoryBeforeAnyRequest(t *testing.T) {
	calls := map[string]func(c *ghapi.Client, repo string) error{
		"HeadCommit": func(c *ghapi.Client, repo string) error {
			_, err := c.HeadCommit(context.Background(), repo)
			return err
		},
		"ReadFileAt": func(c *ghapi.Client, repo string) error {
			_, _, err := c.ReadFileAt(context.Background(), repo, shaRev1, "a.yaml")
			return err
		},
		"CommitChanges": func(c *ghapi.Client, repo string) error {
			_, err := c.CommitChanges(context.Background(), repo, "", "m", map[string]*string{"a.yaml": strp("a\n")})
			return err
		},
		"CommitParentAndPaths": func(c *ghapi.Client, repo string) error {
			_, _, err := c.CommitParentAndPaths(context.Background(), repo, shaRev1)
			return err
		},
	}
	for name, call := range calls {
		for _, repo := range []string{"acme", "acme/gitops/extra", "../gitops", "acme/..", "evil.com/x?y"} {
			t.Run(name+"/"+repo, func(t *testing.T) {
				var requests int
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					w.WriteHeader(http.StatusOK)
				}))
				defer srv.Close()

				if err := call(newTestClient(srv, revToken), repo); err == nil {
					t.Errorf("%s(%q) = nil error, want the repository refused", name, repo)
				}
				if requests != 0 {
					t.Errorf("%s(%q) made %d request(s), want 0", name, repo, requests)
				}
			})
		}
	}
}
