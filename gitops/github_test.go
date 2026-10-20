package gitops_test

// GitHubRepository behaves as Repository says over GitHub's REST API, and no error it returns carries the token.

import (
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/ghapi"
	"github.com/mitstack-eu/operator-kit/v2/gitops"
)

var _ gitops.Repository = (*gitops.GitHubRepository)(nil)

// newGitHubRepo serves f and returns a GitHubRepository over it.
func newGitHubRepo(t *testing.T, f *fakeGitHub) *gitops.GitHubRepository {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &gitops.GitHubRepository{Client: ghapi.NewClient(srv.URL, githubToken, srv.Client()), Repo: fakeRepo}
}

// landOn commits changes on parent and fails the test if GitHub refuses.
func landOn(t *testing.T, repo *gitops.GitHubRepository, parent string, changes map[string][]byte) string {
	t.Helper()
	rev, err := repo.Commit(context.Background(), parent, changes, "seed")
	if err != nil {
		t.Fatalf("Commit on %q: %v", parent, err)
	}
	return rev
}

func TestGitHubRepository_HeadOfARepositoryWithoutMainIsEmpty(t *testing.T) {
	repo := newGitHubRepo(t, newFakeGitHub(t, githubToken))

	head, err := repo.Head(context.Background())

	if err != nil || head != "" {
		t.Errorf("Head = %q, %v; want \"\" for a repository without main", head, err)
	}
}

func TestGitHubRepository_CommitWritesAndDeletesOnTheParentAndChangedReportsIt(t *testing.T) {
	ctx := context.Background()
	f := newFakeGitHub(t, githubToken)
	repo := newGitHubRepo(t, f)

	first := landOn(t, repo, "", map[string][]byte{"a.yaml": []byte("a1\n"), "b.yaml": []byte("b1\n")})
	firstHead := f.head()
	second := landOn(t, repo, first, map[string][]byte{"a.yaml": []byte("a2\n"), "b.yaml": nil, "c.yaml": []byte("c2\n"), "empty.yaml": {}})
	secondParent, secondPaths, err2 := repo.Changed(ctx, second)
	firstParent, firstPaths, err1 := repo.Changed(ctx, first)

	if first == "" || firstHead != first || f.head() != second || f.parentOf(second) != first {
		t.Errorf("first %q (head then %q), second %q on %q with head %q; want each commit the new head on its parent", first, firstHead, second, f.parentOf(second), f.head())
	}
	if want := map[string]string{"a.yaml": "a2\n", "c.yaml": "c2\n", "empty.yaml": ""}; !reflect.DeepEqual(f.filesAt(second), want) {
		t.Errorf("files at the second commit = %q, want %q: a nil content deletes and an empty one writes an empty file", f.filesAt(second), want)
	}
	wantFilesAt(t, repo, second, map[string][]byte{"a.yaml": []byte("a2\n"), "b.yaml": nil, "c.yaml": []byte("c2\n"), "empty.yaml": {}})
	wantFilesAt(t, repo, first, map[string][]byte{"a.yaml": []byte("a1\n"), "b.yaml": []byte("b1\n"), "c.yaml": nil})
	if err2 != nil || secondParent != first || !reflect.DeepEqual(secondPaths, []string{"a.yaml", "b.yaml", "c.yaml", "empty.yaml"}) {
		t.Errorf("Changed(second) = (%q, %q, %v), want (%q, [a.yaml b.yaml c.yaml empty.yaml], nil)", secondParent, secondPaths, err2, first)
	}
	if err1 != nil || firstParent != "" || !reflect.DeepEqual(firstPaths, []string{"a.yaml", "b.yaml"}) {
		t.Errorf("Changed(first) = (%q, %q, %v), want (\"\", [a.yaml b.yaml], nil)", firstParent, firstPaths, err1)
	}
}

func TestGitHubRepository_ACommitOnAParentNoLongerTheTipWrapsErrStaleBaseAndLandsNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		parent func(first string) string
	}{
		{"a stale parent", func(first string) string { return first }},
		{"an empty parent on an existing main", func(string) string { return "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGitHub(t, githubToken)
			repo := newGitHubRepo(t, f)
			first := landOn(t, repo, "", map[string][]byte{"a.yaml": []byte("a1\n")})
			second := landOn(t, repo, first, map[string][]byte{"a.yaml": []byte("a2\n")})

			rev, err := repo.Commit(context.Background(), tc.parent(first), map[string][]byte{"b.yaml": []byte("b\n")}, "stale")

			if !errors.Is(err, gitops.ErrStaleBase) {
				t.Fatalf("Commit on %s = (%q, %v), want an error wrapping gitops.ErrStaleBase", tc.name, rev, err)
			}
			if want := map[string]string{"a.yaml": "a2\n"}; f.head() != second || !reflect.DeepEqual(f.filesAt(f.head()), want) {
				t.Errorf("head %q with files %q, want %q unchanged with %q", f.head(), f.filesAt(f.head()), second, want)
			}
		})
	}
}

func TestGitHubRepository_ReadsAtARevision(t *testing.T) {
	ctx := context.Background()
	repo := newGitHubRepo(t, newFakeGitHub(t, githubToken))
	first := landOn(t, repo, "", map[string][]byte{"a.yaml": []byte("a\n")})
	const neverHeld = "a8db2ef26f6eba24af5a02f74ca37d18a198b309"

	absent, ok, absentErr := repo.ReadFile(ctx, first, "missing.yaml")
	_, unknownOK, unknownErr := repo.ReadFile(ctx, neverHeld, "a.yaml")
	_, _, changedErr := repo.Changed(ctx, neverHeld)

	if absentErr != nil || ok || absent != nil {
		t.Errorf("ReadFile of an absent path = (%q, %v, %v), want (nil, false, nil)", absent, ok, absentErr)
	}
	if unknownErr == nil || changedErr == nil {
		t.Errorf("ReadFile (ok %v) and Changed at a revision never held = %v, %v; want errors, as Repository says", unknownOK, unknownErr, changedErr)
	}
}

func TestGitHubRepository_CommitManifestsRevertAndAStaleParentEndToEnd(t *testing.T) {
	ctx := context.Background()
	f := newFakeGitHub(t, githubToken)
	repo := newGitHubRepo(t, f)
	commit := func(files map[string]string, message string) (string, bool, error) {
		manifests := map[string][]byte{}
		for p, c := range files {
			manifests[p] = []byte(c)
		}
		return gitops.CommitManifests(ctx, repo, manifests, message, seededSecrets)
	}

	v1, changed1, err := commit(map[string]string{"apps/app-alpha/deploy.yaml": "image: v1\n", "apps/app-alpha/service.yaml": "port: 80\n"}, "app-alpha: v1")
	if err != nil || !changed1 {
		t.Fatalf("CommitManifests v1 = (%q, %v, %v), want a new commit", v1, changed1, err)
	}
	again, changedAgain, errAgain := commit(map[string]string{"apps/app-alpha/deploy.yaml": "image: v1\n"}, "app-alpha: v1 again")
	v2, changed2, err := commit(map[string]string{"apps/app-alpha/deploy.yaml": "image: v2\n", "apps/app-alpha/config.yaml": "debug: true\n"}, "app-alpha: v2")
	if err != nil || !changed2 {
		t.Fatalf("CommitManifests v2 = (%q, %v, %v), want a new commit", v2, changed2, err)
	}
	reverted, err := gitops.Revert(ctx, repo, v2, "revert app-alpha: v2")
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	_, staleErr := repo.Commit(ctx, v2, map[string][]byte{"apps/app-alpha/deploy.yaml": []byte("image: v3\n")}, "stale")
	headAfterStale := f.head()
	var other string
	refusedBefore := f.refusals()
	f.mu.Lock()
	f.beforeRefUpdate = func(f *fakeGitHub) {
		other = f.landLocked(map[string]string{"apps/app-beta/deploy.yaml": "image: b1\n"}, "app-beta: b1")
	}
	f.mu.Unlock()
	v3, changed3, err := commit(map[string]string{"apps/app-alpha/deploy.yaml": "image: v3\n"}, "app-alpha: v3")

	if errAgain != nil || changedAgain || again != v1 {
		t.Errorf("CommitManifests of files already at head = (%q, %v, %v), want (%q, false, nil)", again, changedAgain, errAgain, v1)
	}
	if f.parentOf(v2) != v1 || f.parentOf(reverted) != v2 || headAfterStale != reverted {
		t.Errorf("v2 on %q, the revert on %q, head after the stale Commit %q; want v1 %q, v2 %q and the revert %q", f.parentOf(v2), f.parentOf(reverted), headAfterStale, v1, v2, reverted)
	}
	if want := f.filesAt(v1); !reflect.DeepEqual(f.filesAt(reverted), want) {
		t.Errorf("files after the revert = %q, want v1's %q: the changed path restored and the added one deleted", f.filesAt(reverted), want)
	}
	if !errors.Is(staleErr, gitops.ErrStaleBase) {
		t.Errorf("Commit on v2 after the revert = %v, want an error wrapping gitops.ErrStaleBase", staleErr)
	}
	if err != nil || !changed3 || f.refusals()-refusedBefore != 1 || other == "" || f.parentOf(v3) != other {
		t.Fatalf("CommitManifests v3 across a lost race = (%q, %v, %v) on %q after %d refusals; want it replayed once onto the other writer's commit %q",
			v3, changed3, err, f.parentOf(v3), f.refusals()-refusedBefore, other)
	}
	want := map[string]string{"apps/app-alpha/deploy.yaml": "image: v3\n", "apps/app-alpha/service.yaml": "port: 80\n", "apps/app-beta/deploy.yaml": "image: b1\n"}
	if got := f.filesAt(v3); !reflect.DeepEqual(got, want) {
		t.Errorf("files at v3 = %q, want %q", got, want)
	}
}

func TestGitHubRepository_ErrorsNeverCarryTheTokenEvenWhenGitHubEchoesIt(t *testing.T) {
	ctx := context.Background()
	f := newFakeGitHub(t, githubToken)
	repo := newGitHubRepo(t, f)
	first := landOn(t, repo, "", map[string][]byte{"a.yaml": []byte("a\n")})
	f.mu.Lock()
	f.failStatus = 500
	f.mu.Unlock()
	errs := map[string]error{}

	_, errs["Head"] = repo.Head(ctx)
	_, _, errs["ReadFile"] = repo.ReadFile(ctx, first, "a.yaml")
	_, errs["Commit"] = repo.Commit(ctx, first, map[string][]byte{"a.yaml": []byte("a2\n")}, "m")
	_, _, errs["Changed"] = repo.Changed(ctx, first)
	_, _, errs["CommitManifests"] = gitops.CommitManifests(ctx, repo, map[string][]byte{"a.yaml": []byte("a2\n")}, "m", nil)
	_, errs["Revert"] = gitops.Revert(ctx, repo, first, "m")

	for name, err := range errs {
		if err == nil || strings.Contains(err.Error(), githubToken) {
			t.Errorf("%s against a failing GitHub = %v, want an error carrying no token", name, err)
		}
	}
}

// githubDesired is a Desired carrying the github instance at endpoint and the github token.
func githubDesired(endpoint string) contract.Desired {
	d := desiredFor("app-alpha")
	d.Instances = map[string]contract.InstanceConfig{"github": {Name: "github", Endpoint: endpoint}}
	return d
}

func TestNewGitHubRepository_TakesTheBaseFromTheGitHubInstanceAndTheTokenFromTheGitHubSecret(t *testing.T) {
	for _, tc := range []struct{ name, hosts, endpoint, wantBase string }{
		{"the allowlist entry for github.com", "", "https://API.github.com/", "https://api.github.com"},
		{"a host the operator allows through the environment", "ghe.example-corp.com", "https://ghe.example-corp.com", "https://ghe.example-corp.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ghapi.HostsEnv, tc.hosts)

			repo, err := gitops.NewGitHubRepository(githubDesired(tc.endpoint), fakeRepo)

			if err != nil || repo.Client == nil {
				t.Fatalf("NewGitHubRepository(%s) = %+v, %v", tc.endpoint, repo, err)
			}
			if repo.Repo != fakeRepo || repo.Client.Base != tc.wantBase || repo.Client.Token != githubToken {
				t.Errorf("Repo %q, Client.Base %q, token set %v; want %q, %q and the github secret's token", repo.Repo, repo.Client.Base, repo.Client.Token == githubToken, fakeRepo, tc.wantBase)
			}
		})
	}
}

func TestNewGitHubRepository_IsAnErrorNeverCarryingTheTokenWhenTheBaseOrTokenIsMissingOrRefused(t *testing.T) {
	t.Setenv(ghapi.HostsEnv, "")
	withSecrets := func(github map[string]string) contract.Desired {
		d := githubDesired("https://api.github.com")
		d.Secrets["github"] = github
		if github == nil {
			delete(d.Secrets, "github")
		}
		return d
	}
	for name, d := range map[string]contract.Desired{
		"no instances": func() contract.Desired { d := desiredFor("app-alpha"); d.Instances = nil; return d }(),
		"no github instance": func() contract.Desired {
			d := githubDesired("")
			d.Instances = map[string]contract.InstanceConfig{"hetzner": {Endpoint: "https://api.hetzner.cloud"}}
			return d
		}(),
		"an empty endpoint":            githubDesired(""),
		"a host off the allowlist":     githubDesired("https://evil.example.com"),
		"plain http":                   githubDesired("http://api.github.com"),
		"a loopback host":              githubDesired("https://127.0.0.1"),
		"no github secret group":       withSecrets(nil),
		"no token in the github group": withSecrets(map[string]string{"other": githubToken}),
		"an empty token":               withSecrets(map[string]string{"token": ""}),
	} {
		t.Run(name, func(t *testing.T) {
			repo, err := gitops.NewGitHubRepository(d, fakeRepo)

			if err == nil || strings.Contains(err.Error(), githubToken) {
				t.Errorf("NewGitHubRepository = %+v, %v; want an error carrying no token", repo, err)
			}
		})
	}
}
