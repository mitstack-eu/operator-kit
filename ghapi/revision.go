package ghapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mitstack-eu/operator-kit/v2/safe"
)

// ErrNotFastForward is wrapped by CommitChanges when GitHub refuses to move main because the parent is no longer the tip or main already exists for a parent of "".
var ErrNotFastForward = errors.New("ghapi: the parent is no longer the tip of main")

var fullSHA = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// checkRevision refuses anything but a full commit SHA, so no branch name, short prefix, "." or ".." reaches a request path or query.
func checkRevision(revision string) error {
	if !fullSHA.MatchString(revision) {
		return fmt.Errorf("ghapi: revision %q is not a full lowercase hex commit SHA", revision)
	}
	return nil
}

// A contents read carries a file of up to 1 MB base64-encoded, and a commits read carries each file's patch.
const (
	maxContentsBody = 4 << 20
	maxCommitBody   = 32 << 20
)

// GitHub lists at most 3000 files of a commit, 300 per page.
const maxCommitFilePages = 10

// HeadCommit returns the commit refs/heads/main points at, or "" when the repository has no main yet.
func (c *Client) HeadCommit(ctx context.Context, repoFullName string) (string, error) {
	repo, err := SafeRepo(repoFullName)
	if err != nil {
		return "", err
	}
	sha, _, err := c.mainRef(ctx, repo)
	return sha, err
}

// CommitTree returns the tree of commit revision and errors on an unknown revision or one that is not a full lowercase hex SHA, refused before any request.
func (c *Client) CommitTree(ctx context.Context, repoFullName, revision string) (string, error) {
	repo, err := SafeRepo(repoFullName)
	if err != nil {
		return "", err
	}
	if err := checkRevision(revision); err != nil {
		return "", err
	}
	return c.commitTree(ctx, repo, revision)
}

// ReadFileAt reports a missing path as absent with no error, and since GitHub also answers 404 for an unknown revision a caller that must tell them apart checks CommitTree.
func (c *Client) ReadFileAt(ctx context.Context, repoFullName, revision, path string) ([]byte, bool, error) {
	repo, err := SafeRepo(repoFullName)
	if err != nil {
		return nil, false, err
	}
	if err := checkRevision(revision); err != nil {
		return nil, false, err
	}
	escaped, err := escapeRepoPath(path)
	if err != nil {
		return nil, false, err
	}
	status, raw, _, err := c.get(ctx, "/repos/"+repo+"/contents/"+escaped+"?ref="+url.QueryEscape(revision), maxContentsBody)
	if err != nil {
		return nil, false, err
	}
	if status == http.StatusNotFound {
		return nil, false, nil
	}
	if status != http.StatusOK {
		return nil, false, fmt.Errorf("ghapi: github GET contents of %s returned %d: %s", repo, status, safe.LogSafe(string(raw), c.Token))
	}
	var file struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, false, fmt.Errorf("ghapi: decode contents of %s: %w", path, err)
	}
	if file.Type != "file" || file.Encoding != "base64" {
		return nil, false, fmt.Errorf("ghapi: contents of %s is type %q in encoding %q, want a base64-encoded file", path, file.Type, file.Encoding)
	}
	content, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\n", "", "\r", "").Replace(file.Content))
	if err != nil {
		return nil, false, fmt.Errorf("ghapi: decode contents of %s: %w", path, err)
	}
	return content, true, nil
}

// CommitChanges lands one commit on parent, where a nil content deletes its path, a parent of "" creates main, an unchanged tree makes no commit, and a refused ref update wraps ErrNotFastForward without replaying.
func (c *Client) CommitChanges(ctx context.Context, repoFullName, parent, message string, changes map[string]*string) (string, error) {
	repo, err := SafeRepo(repoFullName)
	if err != nil {
		return "", err
	}
	if parent != "" {
		if err := checkRevision(parent); err != nil {
			return "", err
		}
	}
	entries := changeEntries(changes)

	tree := map[string]any{"tree": entries}
	parents := []string{}
	var baseTree string
	if parent != "" {
		baseTree, err = c.commitTree(ctx, repo, parent)
		if err != nil {
			return "", err
		}
		tree["base_tree"] = baseTree
		parents = []string{parent}
	}
	treeSHA, err := c.postForSHA(ctx, "/repos/"+repo+"/git/trees", tree)
	if err != nil {
		return "", err
	}
	if parent != "" && treeSHA == baseTree {
		return parent, nil
	}
	commitSHA, err := c.postForSHA(ctx, "/repos/"+repo+"/git/commits", map[string]any{"message": message, "tree": treeSHA, "parents": parents})
	if err != nil {
		return "", err
	}
	if parent == "" {
		return commitSHA, c.createMainRef(ctx, repo, commitSHA)
	}
	return commitSHA, c.fastForwardMain(ctx, repo, commitSHA)
}

// CommitParentAndPaths returns revision's first parent, "" for a root commit, and the sorted paths it touched, with a rename naming both.
func (c *Client) CommitParentAndPaths(ctx context.Context, repoFullName, revision string) (string, []string, error) {
	repo, err := SafeRepo(repoFullName)
	if err != nil {
		return "", nil, err
	}
	if err := checkRevision(revision); err != nil {
		return "", nil, err
	}
	base := "/repos/" + repo + "/commits/" + url.PathEscape(revision)
	var parent string
	seen := map[string]bool{}
	for page := 1; ; page++ {
		if page > maxCommitFilePages {
			return "", nil, fmt.Errorf("ghapi: commit %s of %s lists files on more than %d pages", revision, repo, maxCommitFilePages)
		}
		path := base
		if page > 1 {
			path += "?page=" + strconv.Itoa(page)
		}
		status, raw, header, err := c.get(ctx, path, maxCommitBody)
		if err != nil {
			return "", nil, err
		}
		if status != http.StatusOK {
			return "", nil, fmt.Errorf("ghapi: github GET commit %s of %s returned %d: %s", revision, repo, status, safe.LogSafe(string(raw), c.Token))
		}
		var commit struct {
			Parents []struct {
				SHA string `json:"sha"`
			} `json:"parents"`
			Files []struct {
				Filename         string `json:"filename"`
				PreviousFilename string `json:"previous_filename"`
			} `json:"files"`
		}
		if err := json.Unmarshal(raw, &commit); err != nil {
			return "", nil, fmt.Errorf("ghapi: decode commit %s: %w", revision, err)
		}
		if page == 1 && len(commit.Parents) > 0 {
			parent = commit.Parents[0].SHA
		}
		for _, f := range commit.Files {
			for _, p := range []string{f.Filename, f.PreviousFilename} {
				if p != "" {
					seen[p] = true
				}
			}
		}
		if !hasNextPage(header.Get("Link")) {
			break
		}
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return parent, paths, nil
}

func changeEntries(changes map[string]*string) []map[string]any {
	paths := make([]string, 0, len(changes))
	for p := range changes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	entries := make([]map[string]any, 0, len(paths))
	for _, p := range paths {
		entry := map[string]any{"path": p, "mode": "100644", "type": "blob"}
		if content := changes[p]; content != nil {
			entry["content"] = *content
		} else {
			entry["sha"] = nil
		}
		entries = append(entries, entry)
	}
	return entries
}

func (c *Client) fastForwardMain(ctx context.Context, repo, commitSHA string) error {
	path := "/repos/" + repo + "/git/refs/heads/main"
	status, raw, err := c.patchStatus(ctx, path, map[string]any{"sha": commitSHA})
	if err != nil {
		return err
	}
	if status < 300 {
		return nil
	}
	if status == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(raw), "fast forward") {
		return fmt.Errorf("%w: github PATCH %s refused the update: %s", ErrNotFastForward, path, safe.LogSafe(raw, c.Token))
	}
	return fmt.Errorf("ghapi: github PATCH %s returned %d: %s", path, status, safe.LogSafe(raw, c.Token))
}

func (c *Client) createMainRef(ctx context.Context, repo, commitSHA string) error {
	path := "/repos/" + repo + "/git/refs"
	resp, err := c.doJSON(ctx, http.MethodPost, path, map[string]any{"ref": "refs/heads/main", "sha": commitSHA})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(string(raw)), "reference already exists") {
		return fmt.Errorf("%w: github POST %s found main already created: %s", ErrNotFastForward, path, safe.LogSafe(string(raw), c.Token))
	}
	return fmt.Errorf("ghapi: github POST %s returned %d: %s", path, resp.StatusCode, safe.LogSafe(string(raw), c.Token))
}

func (c *Client) mainRef(ctx context.Context, repo string) (sha string, haveMain bool, err error) {
	status, raw, _, err := c.get(ctx, "/repos/"+repo+"/git/ref/heads/main", 8<<10)
	if err != nil {
		return "", false, err
	}
	if status == http.StatusNotFound {
		return "", false, nil
	}
	if status != http.StatusOK {
		return "", false, fmt.Errorf("ghapi: github GET git/ref/heads/main returned %d: %s", status, safe.LogSafe(string(raw), c.Token))
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		return "", false, fmt.Errorf("ghapi: decode ref: %w", err)
	}
	return ref.Object.SHA, true, nil
}

func (c *Client) commitTree(ctx context.Context, repo, sha string) (string, error) {
	status, raw, _, err := c.get(ctx, "/repos/"+repo+"/git/commits/"+url.PathEscape(sha), 8<<10)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("ghapi: github GET git/commits returned %d: %s", status, safe.LogSafe(string(raw), c.Token))
	}
	var commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(raw, &commit); err != nil {
		return "", fmt.Errorf("ghapi: decode commit: %w", err)
	}
	return commit.Tree.SHA, nil
}

// get returns the body unscrubbed, so a caller must pass it through safe.LogSafe.
func (c *Client) get(ctx context.Context, path string, limit int64) (int, []byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path, nil)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("ghapi: build request: %w", err)
	}
	c.headers(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("ghapi: github unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("ghapi: read response: %w", err)
	}
	return resp.StatusCode, raw, resp.Header, nil
}

// escapeRepoPath refuses an empty, "." or ".." segment so none can climb out of the contents API.
func escapeRepoPath(path string) (string, error) {
	segments := strings.Split(path, "/")
	for i, s := range segments {
		if s == "" || s == "." || s == ".." {
			return "", fmt.Errorf("ghapi: invalid repository path %q", path)
		}
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/"), nil
}

func hasNextPage(link string) bool {
	for _, part := range strings.Split(link, ",") {
		if strings.Contains(part, `rel="next"`) {
			return true
		}
	}
	return false
}
