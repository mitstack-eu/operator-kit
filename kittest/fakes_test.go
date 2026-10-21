package kittest_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/kittest"
)

// A permissive fake makes every test pass, so the fakes must refuse the paths they do not model.

func TestFakeSTS_ReturnsUsableCredentials(t *testing.T) {
	f := kittest.NewFakeSTS(t)

	resp, err := http.PostForm(f.URL, map[string][]string{
		"Action":  {"AssumeRoleWithWebIdentity"},
		"RoleArn": {"arn:aws:iam::123456789012:role/app"},
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if f.AssumeCount() != 1 {
		t.Errorf("AssumeCount = %d, want 1", f.AssumeCount())
	}
	if f.Calls[0] != "arn:aws:iam::123456789012:role/app" {
		t.Errorf("recorded role %q", f.Calls[0])
	}
}

func TestFakeGitHub_MintsAnInstallationToken(t *testing.T) {
	f := kittest.NewFakeGitHub(t)

	req, _ := http.NewRequest(http.MethodPost, f.URL+"/app/installations/42/access_tokens", nil)
	req.Header.Set("Authorization", "Bearer jwt")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["token"] == "" || body["token"] == nil {
		t.Errorf("no token returned: %v", body)
	}
}

func TestFakeGitHub_SecondRepoCreateIs422LikeTheRealAPI(t *testing.T) {
	// A fake that returned 201 twice would hide a broken retry.
	f := kittest.NewFakeGitHub(t)

	create := func() int {
		req, _ := http.NewRequest(http.MethodPost, f.URL+"/orgs/acme/repos",
			strings.NewReader(`{"name":"app"}`))
		req.Header.Set("Authorization", "token ghs_x")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if got := create(); got != http.StatusCreated {
		t.Fatalf("first create = %d, want 201", got)
	}
	if got := create(); got != http.StatusUnprocessableEntity {
		t.Errorf("second create = %d, want 422 — an operator's retry path depends on this shape", got)
	}
}

func TestFakes_AnswerLoudlyWhatTheyDoNotModelOrAreToldToFail(t *testing.T) {
	form := func(values string) func(url string) *http.Request {
		return func(url string) *http.Request {
			req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(values))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			return req
		}
	}
	github := func(method, path, auth string) func(url string) *http.Request {
		return func(url string) *http.Request {
			req, _ := http.NewRequest(method, url+path, nil)
			if auth != "" {
				req.Header.Set("Authorization", auth)
			}
			return req
		}
	}
	sts := func(t *testing.T) string { return kittest.NewFakeSTS(t).URL }
	cases := []struct {
		name       string
		serve      func(t *testing.T) string
		req        func(url string) *http.Request
		wantStatus int
	}{
		{"STS refuses an action it does not model rather than invent a plausible answer", sts, form("Action=GetCallerIdentity"), http.StatusNotImplemented},
		{"STS answers 403 when told to fail", func(t *testing.T) string {
			f := kittest.NewFakeSTS(t)
			f.FailWith = "AccessDenied"
			return f.URL
		}, form("Action=AssumeRoleWithWebIdentity&RoleArn=arn:x"), http.StatusForbidden},
		{"GitHub refuses a call without the installation token an operator forgot", func(t *testing.T) string { return kittest.NewFakeGitHub(t).URL },
			github(http.MethodGet, "/repos/acme/app", ""), http.StatusUnauthorized},
		{"GitHub refuses a route it does not model, since a permissive fake makes every test pass", func(t *testing.T) string { return kittest.NewFakeGitHub(t).URL },
			github(http.MethodPost, "/some/route/nobody/modelled", "token ghs_x"), http.StatusNotImplemented},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req(tc.serve(t))

			resp, err := http.DefaultClient.Do(req)

			if err != nil {
				t.Fatalf("%s %s: %v", req.Method, req.URL, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("%s %s = %d, want %d", req.Method, req.URL.Path, resp.StatusCode, tc.wantStatus)
			}
		})
	}
}

func TestFakeGitHub_RecordsCommitsAndRevocation(t *testing.T) {
	f := kittest.NewFakeGitHub(t)

	req, _ := http.NewRequest(http.MethodPost, f.URL+"/repos/acme/app/git/commits",
		strings.NewReader(`{"message":"scaffold: initial commit"}`))
	req.Header.Set("Authorization", "token ghs_x")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	if len(f.Commits) != 1 || f.Commits[0] != "scaffold: initial commit" {
		t.Errorf("Commits = %v", f.Commits)
	}

	del, _ := http.NewRequest(http.MethodDelete, f.URL+"/installation/token", nil)
	del.Header.Set("Authorization", "token ghs_x")
	resp2, _ := http.DefaultClient.Do(del)
	resp2.Body.Close()

	if !f.TokenRevoked {
		t.Error("TokenRevoked = false, want disconnect to revoke at the provider")
	}
	if !f.Saw(http.MethodDelete, "/installation/token") {
		t.Error("Saw() did not record the revocation")
	}
}
