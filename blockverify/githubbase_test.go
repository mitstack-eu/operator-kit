package main

// A block that calls the GitHub API resolves a request-supplied base through an allowlist it owns, so the request never chooses the host.

import (
	"strings"
	"testing"
)

const githubCredsGo = `package main

type githubCreds struct {
	Secrets   map[string]map[string]string
	Instances map[string]map[string]string
}
`

const githubOwnerOnlyGo = `package main

func repoOwner(c githubCreds) string { return c.Secrets["github"]["owner"] }
`

const githubTokenUnguardedGo = `package main

func githubToken(c githubCreds) string { return c.Secrets["github"]["token"] }
`

const githubInstanceUnguardedGo = `package main

func githubEndpoint(c githubCreds) string { return c.Instances["github"]["endpoint"] }
`

const githubHostUnguardedGo = `package main

const githubAPI = "https://api.github.com"
`

const githubTokenInATestGo = `package main

import "testing"

func TestGitHubTokenIsRead(t *testing.T) {
	_ = githubCreds{}.Secrets["github"]["token"]
}
`

const githubThroughGhapiBaseGo = `package main

import "github.com/mitstack-eu/operator-kit/v2/ghapi"

func githubBase(c githubCreds) error {
	_, err := ghapi.Base(c.Secrets["github"]["endpoint"])
	return err
}
`

const githubThroughGhapiBaseFromEnvGo = `package main

import "github.com/mitstack-eu/operator-kit/v2/ghapi"

func githubBase(c githubCreds) error {
	_, err := ghapi.BaseFromEnv(c.Secrets["github"]["endpoint"])
	return err
}
`

const githubRawAPIBaseConcatGo = `package main

import "github.com/mitstack-eu/operator-kit/v2/ghapi"

func repoURL(c githubCreds, apiBase string) string {
	_, _ = ghapi.Base(c.Secrets["github"]["endpoint"])
	return apiBase + "/repos"
}
`

const githubRawAPIBaseInACommentGo = `package main

import "github.com/mitstack-eu/operator-kit/v2/ghapi"

func repoURL(c githubCreds) error {
	// Never build a URL as apiBase + "/repos": the request would choose the host.
	_, err := ghapi.Base(c.Secrets["github"]["endpoint"])
	return err
}
`

const urlguardAllowlistGo = `package main

import "net/url"

func allowedGitHubHosts() []string { return []string{"api.github.com"} }

func trustedGitHubBase(c githubCreds) (string, error) {
	u, err := url.Parse(c.Secrets["github"]["endpoint"])
	if err != nil {
		return "", err
	}
	for _, allowed := range allowedGitHubHosts() {
		if u.Host == allowed {
			return "https://" + allowed, nil
		}
	}
	return "https://api.github.com", nil
}
`

const urlguardRequestHostGo = `package main

import "net/url"

func allowedGitHubHosts() []string { return []string{"api.github.com"} }

func trustedGitHubBase(c githubCreds) (string, error) {
	u, err := url.Parse(c.Secrets["github"]["endpoint"])
	if err != nil {
		return "", err
	}
	return "https://" + u.Host, nil
}
`

const urlguardNoAllowlistGo = `package main

func trustedGitHubBase(c githubCreds) (string, error) {
	_ = c.Secrets["github"]["endpoint"]
	return "https://api.github.com", nil
}
`

func TestTrustPlumbingSuite_GitHubBaseAllowlist(t *testing.T) {
	tests := []struct {
		name       string
		extra      map[string]string
		wantStatus string
		wantDetail string
	}{
		{
			name:       "a module that reads no GitHub credential, instance or host is not judged",
			extra:      map[string]string{},
			wantStatus: "pass",
			wantDetail: "manifest.yaml present",
		},
		{
			name:       "a module that reads only the GitHub owner makes no API call and is not judged",
			extra:      map[string]string{"owner.go": githubOwnerOnlyGo},
			wantStatus: "pass",
			wantDetail: "manifest.yaml present",
		},
		{
			name:       "a GitHub read that lives only in a test file is not judged",
			extra:      map[string]string{"github_test.go": githubTokenInATestGo},
			wantStatus: "pass",
			wantDetail: "manifest.yaml present",
		},
		{
			name:       "a GitHub-consuming module that resolves its base through ghapi.Base passes",
			extra:      map[string]string{"github.go": githubThroughGhapiBaseGo},
			wantStatus: "pass",
			wantDetail: "ghapi",
		},
		{
			name:       "a GitHub-consuming module that resolves its base through ghapi.BaseFromEnv passes",
			extra:      map[string]string{"github.go": githubThroughGhapiBaseFromEnvGo},
			wantStatus: "pass",
			wantDetail: "ghapi",
		},
		{
			name:       "a urlguard.go module that returns the allowlist entry passes",
			extra:      map[string]string{"urlguard.go": urlguardAllowlistGo},
			wantStatus: "pass",
			wantDetail: "urlguard.go",
		},
		{
			name:       "a module that reads the GitHub token with neither urlguard.go nor ghapi fails",
			extra:      map[string]string{"github.go": githubTokenUnguardedGo},
			wantStatus: "fail",
			wantDetail: "ghapi.Base",
		},
		{
			name:       "a module that reads the GitHub instance with neither urlguard.go nor ghapi fails",
			extra:      map[string]string{"github.go": githubInstanceUnguardedGo},
			wantStatus: "fail",
			wantDetail: "ghapi.Base",
		},
		{
			name:       "a module that names the GitHub API host with neither urlguard.go nor ghapi fails",
			extra:      map[string]string{"github.go": githubHostUnguardedGo},
			wantStatus: "fail",
			wantDetail: "ghapi.Base",
		},
		{
			name:       "a urlguard.go module that returns a base built from the request host fails",
			extra:      map[string]string{"urlguard.go": urlguardRequestHostGo},
			wantStatus: "fail",
			wantDetail: "request host",
		},
		{
			name:       "a urlguard.go module with no allowedGitHubHosts fails",
			extra:      map[string]string{"urlguard.go": urlguardNoAllowlistGo},
			wantStatus: "fail",
			wantDetail: "allowedGitHubHosts",
		},
		{
			name:       "a module that concatenates the raw apiBase into a URL fails even when it adopts ghapi",
			extra:      map[string]string{"github.go": githubRawAPIBaseConcatGo},
			wantStatus: "fail",
			wantDetail: "apiBase",
		},
		{
			name:       "a whole-line comment that describes apiBase concatenation is not a violation",
			extra:      map[string]string{"github.go": githubRawAPIBaseInACommentGo},
			wantStatus: "pass",
			wantDetail: "ghapi",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := goldenFixtureFiles(t)
			files["creds.go"] = githubCredsGo
			for name, content := range tc.extra {
				files[name] = content
			}
			dir := newFixtureFrom(t, files)

			got := trustPlumbingSuite(dir)

			if got.Status != tc.wantStatus {
				t.Fatalf("trustPlumbingSuite(%s).Status = %q; want %q; detail: %s", dir, got.Status, tc.wantStatus, got.Detail)
			}
			if !strings.Contains(got.Detail, tc.wantDetail) {
				t.Errorf("trustPlumbingSuite(%s).Detail = %q; want it to contain %q", dir, got.Detail, tc.wantDetail)
			}
		})
	}
}
