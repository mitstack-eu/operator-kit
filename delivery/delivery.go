// Package delivery, an optional helper, is the operator-side mirror of the dispatcher's delivery record, with a Client that fetches one, and shares testdata/delivery.golden.json with the dispatcher's tests.
package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Conclusion values a build result carries.
const (
	ConclusionSuccess = "success"
	ConclusionFailure = "failure"
)

// Phase values a delivery record carries, in the precedence the dispatcher derives them: deploy-state transition, then failed build, failed gates, passed gates, successful build.
const (
	PhaseBuilding    = "building"
	PhaseBuildFailed = "build-failed"
	PhaseGatesFailed = "gates-failed"
	PhaseDeploying   = "deploying"
	PhaseHealthy     = "healthy"
	PhaseRolledBack  = "rolled-back"
)

// BuildResult is how a build itself concluded, distinct from any deploy that followed.
type BuildResult struct {
	Conclusion string `json:"conclusion"`
	RunURL     string `json:"runUrl"`
}

// Record is the joined delivery record the dispatcher serves, keeping Verdict as raw JSON and the golden fixture's field order so a round trip is byte for byte.
type Record struct {
	AppID       string          `json:"appId"`
	SHA         string          `json:"sha"`
	Repo        string          `json:"repo"`
	Verdict     json.RawMessage `json:"verdict"`
	DeployState *string         `json:"deployState"`
	BuildResult *BuildResult    `json:"buildResult"`
	Phase       string          `json:"phase"`
}

// ErrNotFound is the error a 404 response wraps.
var ErrNotFound = errors.New("delivery: no registered build found")

// Bounds the error body so a runaway or malicious response cannot grow the error.
const maxErrorBodyBytes = 4096

// StatusError is returned for any response status Client.Get does not interpret, which is neither 200 nor 404.
type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("delivery: unexpected status %d: %s", e.StatusCode, e.Body)
}

// Client fetches a delivery Record from the dispatcher.
type Client struct {
	HTTP  *http.Client
	Base  string
	Token string
}

// NewClient builds a Client against baseURL, authenticating with token and falling back to http.DefaultClient when httpClient is nil.
func NewClient(baseURL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{HTTP: httpClient, Base: baseURL, Token: token}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// Get fetches the delivery record for appID at sha, returning ErrNotFound on 404 and a *StatusError on any other non-200, neither of which includes the bearer token.
func (c *Client) Get(ctx context.Context, appID, sha string) (*Record, error) {
	url := strings.TrimRight(c.Base, "/") + "/delivery/" + appID + "/" + sha

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("delivery: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("delivery: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w for %s/%s", ErrNotFound, appID, sha)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return nil, &StatusError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var rec Record
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		return nil, fmt.Errorf("delivery: decode response: %w", err)
	}
	return &rec, nil
}
