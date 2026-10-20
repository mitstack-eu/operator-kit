package gitops_test

// fakeGitHub serves one repository through just enough of GitHub's REST API for GitHubRepository, keeping real trees and refusing a ref update that is no fast forward.

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
)

const fakeRepo = "acme/gitops"

type fakeCommit struct {
	parent, tree, message string
}

type fakeGitHub struct {
	t     *testing.T
	token string

	mu      sync.Mutex
	tip     string
	commits map[string]fakeCommit
	trees   map[string]map[string]string
	seq     int

	fastForwardRefusals int
	// beforeRefUpdate runs once, lock held, just before the first ref update is judged, so a test can land another writer's commit in between.
	beforeRefUpdate func(f *fakeGitHub)
	// failStatus answers every request with that status and a body echoing the Authorization header.
	failStatus int
}

func newFakeGitHub(t *testing.T, token string) *fakeGitHub {
	return &fakeGitHub{
		t:       t,
		token:   token,
		commits: map[string]fakeCommit{},
		trees:   map[string]map[string]string{"": {}},
	}
}

func hashOf(parts ...string) string {
	h := sha1.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// storeTreeLocked stores files and returns their content-addressed sha.
func (f *fakeGitHub) storeTreeLocked(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	parts := []string{"tree"}
	for _, p := range paths {
		parts = append(parts, p, files[p])
	}
	sha := hashOf(parts...)
	f.trees[sha] = files
	return sha
}

func (f *fakeGitHub) storeCommitLocked(parent, tree, message string) string {
	f.seq++
	sha := hashOf("commit", parent, tree, message, fmt.Sprint(f.seq))
	f.commits[sha] = fakeCommit{parent: parent, tree: tree, message: message}
	return sha
}

// landLocked commits changes on the tip as another writer would, moving main.
func (f *fakeGitHub) landLocked(changes map[string]string, message string) string {
	files := map[string]string{}
	if f.tip != "" {
		for p, c := range f.trees[f.commits[f.tip].tree] {
			files[p] = c
		}
	}
	for p, c := range changes {
		files[p] = c
	}
	f.tip = f.storeCommitLocked(f.tip, f.storeTreeLocked(files), message)
	return f.tip
}

// filesAt returns the files commit holds.
func (f *fakeGitHub) filesAt(commit string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for p, c := range f.trees[f.commits[commit].tree] {
		out[p] = c
	}
	return out
}

func (f *fakeGitHub) parentOf(commit string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commits[commit].parent
}

func (f *fakeGitHub) head() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tip
}

func (f *fakeGitHub) commitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.commits)
}

func (f *fakeGitHub) refusals() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fastForwardRefusals
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func message(text string) map[string]string { return map[string]string{"message": text} }

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failStatus != 0 {
		reply(w, f.failStatus, message("upstream failure for "+r.Header.Get("Authorization")))
		return
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+f.token {
		f.t.Errorf("%s %s carried Authorization %q, want the client's bearer token", r.Method, r.URL.Path, got)
		reply(w, http.StatusUnauthorized, message("Bad credentials"))
		return
	}
	prefix := "/repos/" + fakeRepo + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		f.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		reply(w, http.StatusNotFound, message("Not Found"))
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)

	switch {
	case r.Method == http.MethodGet && (rest == "git/ref/heads/main" || rest == "git/refs/heads/main"):
		if f.tip == "" {
			reply(w, http.StatusNotFound, message("Not Found"))
			return
		}
		reply(w, http.StatusOK, map[string]any{"ref": "refs/heads/main", "object": map[string]string{"sha": f.tip, "type": "commit"}})

	case r.Method == http.MethodGet && strings.HasPrefix(rest, "git/commits/"):
		f.getGitCommit(w, strings.TrimPrefix(rest, "git/commits/"))

	case r.Method == http.MethodPost && rest == "git/trees":
		f.postTree(w, r)

	case r.Method == http.MethodPost && rest == "git/commits":
		f.postCommit(w, r)

	case r.Method == http.MethodPost && rest == "git/refs":
		f.postRef(w, r)

	case r.Method == http.MethodPatch && rest == "git/refs/heads/main":
		f.patchMain(w, r)

	case r.Method == http.MethodGet && strings.HasPrefix(rest, "contents/"):
		f.getContents(w, strings.TrimPrefix(rest, "contents/"), r.URL.Query().Get("ref"))

	case r.Method == http.MethodGet && strings.HasPrefix(rest, "commits/"):
		f.getCommit(w, strings.TrimPrefix(rest, "commits/"))

	default:
		f.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		reply(w, http.StatusNotFound, message("Not Found"))
	}
}

func (f *fakeGitHub) getGitCommit(w http.ResponseWriter, sha string) {
	c, ok := f.commits[sha]
	if !ok {
		reply(w, http.StatusNotFound, message("Not Found"))
		return
	}
	parents := []map[string]string{}
	if c.parent != "" {
		parents = append(parents, map[string]string{"sha": c.parent})
	}
	reply(w, http.StatusOK, map[string]any{"sha": sha, "message": c.message, "tree": map[string]string{"sha": c.tree}, "parents": parents})
}

func (f *fakeGitHub) postTree(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseTree *string                      `json:"base_tree"`
		Tree     []map[string]json.RawMessage `json:"tree"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Errorf("tree POST: %v", err)
		reply(w, http.StatusBadRequest, message("Problems parsing JSON"))
		return
	}
	files := map[string]string{}
	if body.BaseTree != nil && *body.BaseTree != "" {
		base, ok := f.trees[*body.BaseTree]
		if !ok {
			reply(w, http.StatusUnprocessableEntity, message("base_tree is not a valid tree oid"))
			return
		}
		for p, c := range base {
			files[p] = c
		}
	}
	for _, entry := range body.Tree {
		var path, mode, typ string
		_ = json.Unmarshal(entry["path"], &path)
		_ = json.Unmarshal(entry["mode"], &mode)
		_ = json.Unmarshal(entry["type"], &typ)
		if path == "" || mode != "100644" || typ != "blob" {
			f.t.Errorf("tree entry %s: want a path, mode 100644 and type blob", entry)
			reply(w, http.StatusUnprocessableEntity, message("Invalid tree info"))
			return
		}
		content, hasContent := entry["content"]
		sha, hasSHA := entry["sha"]
		switch {
		case hasContent && !hasSHA:
			var s string
			if err := json.Unmarshal(content, &s); err != nil {
				f.t.Errorf("tree entry %s: content is not a string", entry)
				reply(w, http.StatusUnprocessableEntity, message("Invalid tree info"))
				return
			}
			files[path] = s
		case hasSHA && !hasContent && string(sha) == "null":
			delete(files, path)
		default:
			f.t.Errorf("tree entry %s: want either content or \"sha\": null", entry)
			reply(w, http.StatusUnprocessableEntity, message("Invalid tree info"))
			return
		}
	}
	reply(w, http.StatusCreated, map[string]string{"sha": f.storeTreeLocked(files)})
}

func (f *fakeGitHub) postCommit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message string   `json:"message"`
		Tree    string   `json:"tree"`
		Parents []string `json:"parents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Errorf("commit POST: %v", err)
		reply(w, http.StatusBadRequest, message("Problems parsing JSON"))
		return
	}
	if _, ok := f.trees[body.Tree]; !ok || body.Tree == "" {
		reply(w, http.StatusUnprocessableEntity, message("Tree SHA does not exist"))
		return
	}
	if len(body.Parents) > 1 {
		f.t.Errorf("commit POST with parents %v, want at most one", body.Parents)
	}
	parent := ""
	if len(body.Parents) == 1 {
		parent = body.Parents[0]
		if _, ok := f.commits[parent]; !ok {
			reply(w, http.StatusUnprocessableEntity, message("Parent SHA does not exist or is not a commit object"))
			return
		}
	}
	reply(w, http.StatusCreated, map[string]string{"sha": f.storeCommitLocked(parent, body.Tree, body.Message)})
}

func (f *fakeGitHub) postRef(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Ref != "refs/heads/main" {
		f.t.Errorf("ref POST for %q, want refs/heads/main", body.Ref)
	}
	if f.tip != "" {
		reply(w, http.StatusUnprocessableEntity, message("Reference already exists"))
		return
	}
	if _, ok := f.commits[body.SHA]; !ok {
		reply(w, http.StatusUnprocessableEntity, message("Object does not exist"))
		return
	}
	f.tip = body.SHA
	reply(w, http.StatusCreated, map[string]any{"ref": "refs/heads/main", "object": map[string]string{"sha": f.tip}})
}

func (f *fakeGitHub) patchMain(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SHA   string `json:"sha"`
		Force bool   `json:"force"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Force {
		f.t.Error("ref PATCH with force, want a fast forward only")
	}
	if hook := f.beforeRefUpdate; hook != nil {
		f.beforeRefUpdate = nil
		hook(f)
	}
	if f.tip == "" {
		reply(w, http.StatusUnprocessableEntity, message("Reference does not exist"))
		return
	}
	if _, ok := f.commits[body.SHA]; !ok {
		reply(w, http.StatusUnprocessableEntity, message("Object does not exist"))
		return
	}
	for c := body.SHA; c != ""; c = f.commits[c].parent {
		if c == f.tip {
			f.tip = body.SHA
			reply(w, http.StatusOK, map[string]any{"ref": "refs/heads/main", "object": map[string]string{"sha": f.tip}})
			return
		}
	}
	f.fastForwardRefusals++
	reply(w, http.StatusUnprocessableEntity, map[string]string{
		"message":           "Update is not a fast forward",
		"documentation_url": "https://docs.github.com/rest/git/refs#update-a-reference",
	})
}

// wrapBase64 encodes content as the contents API does, in 60-character lines.
func wrapBase64(content string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(content))
	var b strings.Builder
	for len(enc) > 60 {
		b.WriteString(enc[:60] + "\n")
		enc = enc[60:]
	}
	b.WriteString(enc + "\n")
	return b.String()
}

func (f *fakeGitHub) getContents(w http.ResponseWriter, path, ref string) {
	c, ok := f.commits[ref]
	if !ok {
		reply(w, http.StatusNotFound, message("No commit found for the ref "+ref))
		return
	}
	content, ok := f.trees[c.tree][path]
	if !ok {
		reply(w, http.StatusNotFound, message("Not Found"))
		return
	}
	reply(w, http.StatusOK, map[string]string{"type": "file", "encoding": "base64", "path": path, "content": wrapBase64(content)})
}

func (f *fakeGitHub) getCommit(w http.ResponseWriter, sha string) {
	c, ok := f.commits[sha]
	if !ok {
		reply(w, http.StatusUnprocessableEntity, message("No commit found for SHA: "+sha))
		return
	}
	before := map[string]string{}
	if c.parent != "" {
		before = f.trees[f.commits[c.parent].tree]
	}
	after := f.trees[c.tree]
	files := []map[string]string{}
	for p, content := range after {
		if old, ok := before[p]; !ok {
			files = append(files, map[string]string{"filename": p, "status": "added"})
		} else if old != content {
			files = append(files, map[string]string{"filename": p, "status": "modified"})
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			files = append(files, map[string]string{"filename": p, "status": "removed"})
		}
	}
	parents := []map[string]string{}
	if c.parent != "" {
		parents = append(parents, map[string]string{"sha": c.parent})
	}
	reply(w, http.StatusOK, map[string]any{"sha": sha, "parents": parents, "files": files})
}
