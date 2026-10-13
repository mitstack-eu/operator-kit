package kittest_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
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

func TestFakeSTS_RefusesAnUnmodelledAction(t *testing.T) {
	// The property that makes the fake trustworthy: it does not invent a
	// plausible answer for something it does not model.
	f := kittest.NewFakeSTS(t)

	resp, err := http.PostForm(f.URL, map[string][]string{"Action": {"GetCallerIdentity"}})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501 — an unmodelled call must be loud, not plausible", resp.StatusCode)
	}
}

func TestFakeSTS_CanSimulateAFailure(t *testing.T) {
	f := kittest.NewFakeSTS(t)
	f.FailWith = "AccessDenied"

	resp, err := http.PostForm(f.URL, map[string][]string{
		"Action": {"AssumeRoleWithWebIdentity"}, "RoleArn": {"arn:x"},
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
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

func TestFakeGitHub_RequiresAuthorization(t *testing.T) {
	// An operator that forgets the installation token should find out here.
	f := kittest.NewFakeGitHub(t)

	resp, err := http.Get(f.URL + "/repos/acme/app")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an unauthenticated call", resp.StatusCode)
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

func TestFakeGitHub_RefusesAnUnmodelledCall(t *testing.T) {
	f := kittest.NewFakeGitHub(t)

	req, _ := http.NewRequest(http.MethodPost, f.URL+"/some/route/nobody/modelled", nil)
	req.Header.Set("Authorization", "token ghs_x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501 — this is the MIT-1484 failure mode: a permissive fake makes every test pass", resp.StatusCode)
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
		t.Error("TokenRevoked = false — disconnect must revoke at the provider (T-624)")
	}
	if !f.Saw(http.MethodDelete, "/installation/token") {
		t.Error("Saw() did not record the revocation")
	}
}
