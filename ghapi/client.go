package ghapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/mitstack-eu/operator-kit/v2/safe"
)

// Client is a GitHub REST API client whose Base is already resolved, so it carries no host-trust logic and a test can supply an httptest URL.
type Client struct {
	HTTP  *http.Client
	Base  string
	Token string
}

// NewClient builds a Client from a resolved base URL, a token and an *http.Client where nil uses http.DefaultClient.
func NewClient(baseURL, token string, httpClient *http.Client) *Client {
	return &Client{HTTP: httpClient, Base: baseURL, Token: token}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) headers(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

// EnsureRepo idempotently creates a private repo, treating a 200 GET as already existing and a 404 as a create.
func (c *Client) EnsureRepo(ctx context.Context, org, name string) (string, error) {
	fullName := org + "/" + name
	repo, err := SafeRepo(fullName)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/repos/"+repo, nil)
	if err != nil {
		return "", fmt.Errorf("ghapi: build request: %w", err)
	}
	c.headers(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("ghapi: github unreachable: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return fullName, nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return "", fmt.Errorf("ghapi: github GET /repos/%s returned %d", repo, resp.StatusCode)
	}

	cresp, err := c.doJSON(ctx, http.MethodPost, "/orgs/"+url.PathEscape(org)+"/repos", map[string]any{
		"name":      name,
		"private":   true,
		"auto_init": true,
	})
	if err != nil {
		return "", err
	}
	defer cresp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(cresp.Body, 8<<10))
	if cresp.StatusCode >= 300 {
		return "", fmt.Errorf("ghapi: github POST /orgs/%s/repos returned %d: %s", org, cresp.StatusCode, safe.LogSafe(string(raw), c.Token))
	}
	var out struct {
		FullName string `json:"full_name"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.FullName != "" {
		return out.FullName, nil
	}
	return fullName, nil
}

// DeleteRepo treats a 404 as success so a teardown can repeat safely.
func (c *Client) DeleteRepo(ctx context.Context, org, name string) error {
	repo, err := SafeRepo(org + "/" + name)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.Base+"/repos/"+repo, nil)
	if err != nil {
		return fmt.Errorf("ghapi: build request: %w", err)
	}
	c.headers(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("ghapi: github unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return fmt.Errorf("ghapi: github DELETE /repos/%s returned %d: %s", repo, resp.StatusCode, safe.LogSafe(string(raw), c.Token))
}

const maxFastForwardAttempts = 3

var errStaleBase = errors.New("ghapi: main moved before the ref update")

// CommitFiles makes no commit when the tree is unchanged and replays a lost fast-forward race by rebuilding the tree and commit rather than retrying the PATCH alone.
func (c *Client) CommitFiles(ctx context.Context, repoFullName, message string, files map[string]string) error {
	repo, err := SafeRepo(repoFullName)
	if err != nil {
		return err
	}

	baseSHA, baseTreeSHA, haveBase, err := c.baseCommit(ctx, repo)
	if err != nil {
		return err
	}

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	entries := make([]map[string]string, 0, len(paths))
	for _, p := range paths {
		entries = append(entries, map[string]string{"path": p, "mode": "100644", "type": "blob", "content": files[p]})
	}

	if !haveBase {
		return c.createMain(ctx, repo, message, entries)
	}
	for attempt := 1; ; attempt++ {
		err = c.commitOnto(ctx, repo, message, entries, baseSHA, baseTreeSHA)
		if !errors.Is(err, errStaleBase) || attempt == maxFastForwardAttempts {
			return err
		}
		baseSHA, baseTreeSHA, haveBase, err = c.baseCommit(ctx, repo)
		if err != nil {
			return err
		}
		if !haveBase {
			return fmt.Errorf("ghapi: main of %s disappeared while replaying a lost fast forward race", repo)
		}
	}
}

func (c *Client) createMain(ctx context.Context, repo, message string, entries []map[string]string) error {
	treeSHA, err := c.postForSHA(ctx, "/repos/"+repo+"/git/trees", map[string]any{"tree": entries})
	if err != nil {
		return err
	}
	commitSHA, err := c.postForSHA(ctx, "/repos/"+repo+"/git/commits", map[string]any{"message": message, "tree": treeSHA})
	if err != nil {
		return err
	}
	_, err = c.postForSHA(ctx, "/repos/"+repo+"/git/refs", map[string]any{"ref": "refs/heads/main", "sha": commitSHA})
	return err
}

// commitOnto wraps errStaleBase only when GitHub refuses the PATCH as not a fast forward.
func (c *Client) commitOnto(ctx context.Context, repo, message string, entries []map[string]string, baseSHA, baseTreeSHA string) error {
	treeSHA, err := c.postForSHA(ctx, "/repos/"+repo+"/git/trees", map[string]any{"tree": entries, "base_tree": baseTreeSHA})
	if err != nil {
		return err
	}
	if treeSHA == baseTreeSHA {
		return nil
	}
	commitSHA, err := c.postForSHA(ctx, "/repos/"+repo+"/git/commits", map[string]any{"message": message, "tree": treeSHA, "parents": []string{baseSHA}})
	if err != nil {
		return err
	}
	path := "/repos/" + repo + "/git/refs/heads/main"
	status, raw, err := c.patchStatus(ctx, path, map[string]any{"sha": commitSHA})
	if err != nil {
		return err
	}
	if status < 300 {
		return nil
	}
	if status == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(raw), "fast forward") {
		return fmt.Errorf("%w: github PATCH %s lost the fast forward race: %s", errStaleBase, path, safe.LogSafe(raw, c.Token))
	}
	return fmt.Errorf("ghapi: github PATCH %s returned %d: %s", path, status, safe.LogSafe(raw, c.Token))
}

// baseCommit reports haveBase false for an empty repo with no heads/main ref yet.
func (c *Client) baseCommit(ctx context.Context, repo string) (commitSHA, treeSHA string, haveBase bool, err error) {
	commitSHA, haveBase, err = c.mainRef(ctx, repo)
	if err != nil || !haveBase {
		return "", "", false, err
	}
	treeSHA, err = c.commitTree(ctx, repo, commitSHA)
	if err != nil {
		return "", "", false, err
	}
	return commitSHA, treeSHA, true, nil
}

func (c *Client) postForSHA(ctx context.Context, path string, payload any) (string, error) {
	resp, err := c.doJSON(ctx, http.MethodPost, path, payload)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("ghapi: github POST %s returned %d: %s", path, resp.StatusCode, safe.LogSafe(string(raw), c.Token))
	}
	var out struct {
		SHA string `json:"sha"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.SHA, nil
}

func (c *Client) patch(ctx context.Context, path string, payload any) error {
	status, raw, err := c.patchStatus(ctx, path, payload)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("ghapi: github PATCH %s returned %d: %s", path, status, safe.LogSafe(raw, c.Token))
	}
	return nil
}

// patchStatus returns the body unscrubbed, so a caller must pass it through safe.LogSafe.
func (c *Client) patchStatus(ctx context.Context, path string, payload any) (int, string, error) {
	resp, err := c.doJSON(ctx, http.MethodPatch, path, payload)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, string(raw), nil
}

// SetVariable validates name before any HTTP call, falls back to PATCH on a 409, and on an ambiguous 422 patches only when the message says "already exists".
func (c *Client) SetVariable(ctx context.Context, repoFullName, name, value string) error {
	repo, err := SafeRepo(repoFullName)
	if err != nil {
		return err
	}
	name, err = SafeVariableName(name)
	if err != nil {
		return err
	}

	resp, err := c.doJSON(ctx, http.MethodPost, "/repos/"+repo+"/actions/variables", map[string]any{
		"name":  name,
		"value": value,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode != http.StatusConflict && resp.StatusCode != http.StatusUnprocessableEntity {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("ghapi: github POST /repos/%s/actions/variables returned %d: %s", repo, resp.StatusCode, safe.LogSafe(string(raw), c.Token))
	}
	if resp.StatusCode == http.StatusUnprocessableEntity {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		var body struct {
			Message string `json:"message"`
		}
		if jsonErr := json.Unmarshal(raw, &body); jsonErr != nil || !strings.Contains(strings.ToLower(body.Message), "already exists") {
			return fmt.Errorf("ghapi: github POST /repos/%s/actions/variables returned 422: %s", repo, safe.LogSafe(string(raw), c.Token))
		}
	}

	return c.patch(ctx, "/repos/"+repo+"/actions/variables/"+name, map[string]any{
		"name":  name,
		"value": value,
	})
}

func (c *Client) doJSON(ctx context.Context, method, path string, payload any) (*http.Response, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("ghapi: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("ghapi: build request: %w", err)
	}
	c.headers(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("ghapi: github call failed: %w", err)
	}
	return resp, nil
}
