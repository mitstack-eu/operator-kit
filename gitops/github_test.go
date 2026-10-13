package gitops_test

// github_test.go pins GitHubRepository, the Repository over GitHub's REST
// API, against the in-memory GitHub in github_fake_test.go:
//
//   - Head, ReadFile, Commit and Changed behave as Repository says, a nil
//     content deleting its path and an empty one writing an empty file;
//   - a Commit whose parent is no longer the tip lands nothing and wraps
//     ErrStaleBase, so CommitManifests and Revert replay it on a fresh head;
//   - CommitManifests, Revert and a stale-parent Commit work end to end;
//   - NewGitHubRepository takes the base from the github instance through
//     the host allowlist and the token from the github secret;
//   - no error ever carries the token, even when GitHub's body echoes it.

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

func TestGitHubRepository_HeadOfARepositoryWithoutMainIsEmpty(t *testing.T) {
	repo := newGitHubRepo(t, newFakeGitHub(t, githubToken))

	head, err := repo.Head(context.Background())
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != "" {
		t.Errorf("Head = %q, want \"\" for a repository without main", head)
	}
}

func TestGitHubRepository_CommitWritesAndDeletesOnTheParentAndChangedReportsIt(t *testing.T) {
	ctx := context.Background()
	f := newFakeGitHub(t, githubToken)
	repo := newGitHubRepo(t, f)

	first, err := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a1\n"), "b.yaml": []byte("b1\n")}, "first")
	if err != nil {
		t.Fatalf("first Commit: %v", err)
	}
	if head, _ := repo.Head(ctx); first == "" || head != first {
		t.Errorf("first Commit = %q with head %q, want the new head", first, head)
	}
	second, err := repo.Commit(ctx, first, map[string][]byte{
		"a.yaml":     []byte("a2\n"),
		"b.yaml":     nil,
		"c.yaml":     []byte("c2\n"),
		"empty.yaml": {},
	}, "second")
	if err != nil {
		t.Fatalf("second Commit: %v", err)
	}
	if head, _ := repo.Head(ctx); head != second {
		t.Errorf("head = %q, want the second commit %q", head, second)
	}
	if got := f.parentOf(second); got != first {
		t.Errorf("the second commit's parent = %q, want %q", got, first)
	}
	if want := map[string]string{"a.yaml": "a2\n", "c.yaml": "c2\n", "empty.yaml": ""}; !reflect.DeepEqual(f.filesAt(second), want) {
		t.Errorf("files at the second commit = %q, want %q: a nil content deletes and an empty one writes an empty file", f.filesAt(second), want)
	}
	wantFilesAt(t, repo, second, map[string][]byte{"a.yaml": []byte("a2\n"), "b.yaml": nil, "c.yaml": []byte("c2\n"), "empty.yaml": {}})
	wantFilesAt(t, repo, first, map[string][]byte{"a.yaml": []byte("a1\n"), "b.yaml": []byte("b1\n"), "c.yaml": nil})

	parent, paths, err := repo.Changed(ctx, second)
	if err != nil {
		t.Fatalf("Changed(second): %v", err)
	}
	if parent != first || !reflect.DeepEqual(paths, []string{"a.yaml", "b.yaml", "c.yaml", "empty.yaml"}) {
		t.Errorf("Changed(second) = (%q, %q), want (%q, [a.yaml b.yaml c.yaml empty.yaml])", parent, paths, first)
	}
	parent, paths, err = repo.Changed(ctx, first)
	if err != nil {
		t.Fatalf("Changed(first): %v", err)
	}
	if parent != "" || !reflect.DeepEqual(paths, []string{"a.yaml", "b.yaml"}) {
		t.Errorf("Changed(first) = (%q, %q), want (\"\", [a.yaml b.yaml])", parent, paths)
	}
}

func TestGitHubRepository_ACommitOnAStaleParentWrapsErrStaleBaseAndLandsNothing(t *testing.T) {
	ctx := context.Background()
	f := newFakeGitHub(t, githubToken)
	repo := newGitHubRepo(t, f)
	first, err := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a1\n")}, "first")
	if err != nil {
		t.Fatalf("first Commit: %v", err)
	}
	second, err := repo.Commit(ctx, first, map[string][]byte{"a.yaml": []byte("a2\n")}, "second")
	if err != nil {
		t.Fatalf("second Commit: %v", err)
	}

	rev, err := repo.Commit(ctx, first, map[string][]byte{"b.yaml": []byte("b\n")}, "stale")
	if !errors.Is(err, gitops.ErrStaleBase) {
		t.Fatalf("Commit on a stale parent = (%q, %v), want an error wrapping gitops.ErrStaleBase", rev, err)
	}
	if head := f.head(); head != second {
		t.Errorf("head = %q, want %q unchanged", head, second)
	}
	if want := map[string]string{"a.yaml": "a2\n"}; !reflect.DeepEqual(f.filesAt(f.head()), want) {
		t.Errorf("files at head = %q, want %q", f.filesAt(f.head()), want)
	}
}

func TestGitHubRepository_ACommitWithAnEmptyParentOnAnExistingMainWrapsErrStaleBase(t *testing.T) {
	ctx := context.Background()
	f := newFakeGitHub(t, githubToken)
	repo := newGitHubRepo(t, f)
	first, err := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a1\n")}, "first")
	if err != nil {
		t.Fatalf("first Commit: %v", err)
	}

	if _, err := repo.Commit(ctx, "", map[string][]byte{"b.yaml": []byte("b\n")}, "racing first"); !errors.Is(err, gitops.ErrStaleBase) {
		t.Fatalf("Commit with parent \"\" on an existing main = %v, want an error wrapping gitops.ErrStaleBase", err)
	}
	if head := f.head(); head != first {
		t.Errorf("head = %q, want %q unchanged", head, first)
	}
}

func TestGitHubRepository_ReadFileOfAPathAbsentAtTheRevisionIsAbsent(t *testing.T) {
	ctx := context.Background()
	repo := newGitHubRepo(t, newFakeGitHub(t, githubToken))
	first, err := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a\n")}, "first")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	got, ok, err := repo.ReadFile(ctx, first, "missing.yaml")
	if err != nil || ok || got != nil {
		t.Errorf("ReadFile of an absent path = (%q, %v, %v), want (nil, false, nil)", got, ok, err)
	}
}

func TestGitHubRepository_ARevisionTheRepositoryNeverHeldIsAnError(t *testing.T) {
	ctx := context.Background()
	repo := newGitHubRepo(t, newFakeGitHub(t, githubToken))
	if _, err := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a\n")}, "first"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if _, ok, err := repo.ReadFile(ctx, "a8db2ef26f6eba24af5a02f74ca37d18a198b309", "a.yaml"); err == nil {
		t.Errorf("ReadFile at an unknown revision = (ok %v, nil error), want an error: Repository says a revision it never held is an error", ok)
	}
	if _, _, err := repo.Changed(ctx, "a8db2ef26f6eba24af5a02f74ca37d18a198b309"); err == nil {
		t.Error("Changed of an unknown revision = nil error")
	}
}

func TestGitHubRepository_CommitManifestsRevertAndAStaleParentEndToEnd(t *testing.T) {
	ctx := context.Background()
	f := newFakeGitHub(t, githubToken)
	repo := newGitHubRepo(t, f)

	v1, changed, err := gitops.CommitManifests(ctx, repo, map[string][]byte{
		"apps/app-alpha/deploy.yaml":  []byte("image: v1\n"),
		"apps/app-alpha/service.yaml": []byte("port: 80\n"),
	}, "app-alpha: v1", seededSecrets)
	if err != nil || !changed {
		t.Fatalf("CommitManifests v1 = (%q, %v, %v), want a new commit", v1, changed, err)
	}

	again, changed, err := gitops.CommitManifests(ctx, repo, map[string][]byte{
		"apps/app-alpha/deploy.yaml": []byte("image: v1\n"),
	}, "app-alpha: v1 again", seededSecrets)
	if err != nil || changed || again != v1 {
		t.Errorf("CommitManifests of files already at head = (%q, %v, %v), want (%q, false, nil)", again, changed, err, v1)
	}

	v2, changed, err := gitops.CommitManifests(ctx, repo, map[string][]byte{
		"apps/app-alpha/deploy.yaml": []byte("image: v2\n"),
		"apps/app-alpha/config.yaml": []byte("debug: true\n"),
	}, "app-alpha: v2", seededSecrets)
	if err != nil || !changed {
		t.Fatalf("CommitManifests v2 = (%q, %v, %v), want a new commit", v2, changed, err)
	}
	if got := f.parentOf(v2); got != v1 {
		t.Errorf("v2's parent = %q, want v1 %q", got, v1)
	}

	reverted, err := gitops.Revert(ctx, repo, v2, "revert app-alpha: v2")
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if got := f.parentOf(reverted); got != v2 {
		t.Errorf("the revert's parent = %q, want v2 %q", got, v2)
	}
	if want := f.filesAt(v1); !reflect.DeepEqual(f.filesAt(reverted), want) {
		t.Errorf("files after the revert = %q, want v1's %q: the changed path restored and the added one deleted", f.filesAt(reverted), want)
	}
	if head := f.head(); head != reverted {
		t.Errorf("head = %q, want the revert %q", head, reverted)
	}

	if _, err := repo.Commit(ctx, v2, map[string][]byte{"apps/app-alpha/deploy.yaml": []byte("image: v3\n")}, "stale"); !errors.Is(err, gitops.ErrStaleBase) {
		t.Fatalf("Commit on v2 after the revert = %v, want an error wrapping gitops.ErrStaleBase", err)
	}
	if head := f.head(); head != reverted {
		t.Errorf("head after the stale Commit = %q, want %q unchanged", head, reverted)
	}

	var other string
	refusedBefore := f.refusals()
	f.mu.Lock()
	f.beforeRefUpdate = func(f *fakeGitHub) {
		other = f.landLocked(map[string]string{"apps/app-beta/deploy.yaml": "image: b1\n"}, "app-beta: b1")
	}
	f.mu.Unlock()
	v3, changed, err := gitops.CommitManifests(ctx, repo, map[string][]byte{
		"apps/app-alpha/deploy.yaml": []byte("image: v3\n"),
	}, "app-alpha: v3", seededSecrets)
	if err != nil || !changed {
		t.Fatalf("CommitManifests v3 across a lost race = (%q, %v, %v), want it replayed onto the other writer's commit", v3, changed, err)
	}
	if n := f.refusals() - refusedBefore; n != 1 {
		t.Errorf("fast forward refusals = %d, want exactly 1: the lost race, then a replay on the fresh head", n)
	}
	if got := f.parentOf(v3); other == "" || got != other {
		t.Errorf("v3's parent = %q, want the other writer's commit %q", got, other)
	}
	want := map[string]string{
		"apps/app-alpha/deploy.yaml":  "image: v3\n",
		"apps/app-alpha/service.yaml": "port: 80\n",
		"apps/app-beta/deploy.yaml":   "image: b1\n",
	}
	if got := f.filesAt(v3); !reflect.DeepEqual(got, want) {
		t.Errorf("files at v3 = %q, want %q", got, want)
	}
}

func TestGitHubRepository_ErrorsNeverCarryTheTokenEvenWhenGitHubEchoesIt(t *testing.T) {
	ctx := context.Background()
	f := newFakeGitHub(t, githubToken)
	repo := newGitHubRepo(t, f)
	first, err := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a\n")}, "first")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	f.mu.Lock()
	f.failStatus = 500
	f.mu.Unlock()

	calls := map[string]func() error{
		"Head": func() error { _, err := repo.Head(ctx); return err },
		"ReadFile": func() error {
			_, _, err := repo.ReadFile(ctx, first, "a.yaml")
			return err
		},
		"Commit": func() error {
			_, err := repo.Commit(ctx, first, map[string][]byte{"a.yaml": []byte("a2\n")}, "m")
			return err
		},
		"Changed": func() error { _, _, err := repo.Changed(ctx, first); return err },
		"CommitManifests": func() error {
			_, _, err := gitops.CommitManifests(ctx, repo, map[string][]byte{"a.yaml": []byte("a2\n")}, "m", nil)
			return err
		},
		"Revert": func() error { _, err := gitops.Revert(ctx, repo, first, "m"); return err },
	}
	for name, call := range calls {
		err := call()
		if err == nil {
			t.Errorf("%s = nil error against a failing GitHub", name)
			continue
		}
		if strings.Contains(err.Error(), githubToken) {
			t.Errorf("%s error carries the token: %v", name, err)
		}
	}
}

// --- NewGitHubRepository -----------------------------------------------------

// githubDesired is a Desired carrying the github instance at endpoint and
// the github token.
func githubDesired(endpoint string) contract.Desired {
	d := desiredFor("app-alpha")
	d.Instances = map[string]contract.InstanceConfig{"github": {Name: "github", Endpoint: endpoint}}
	return d
}

func TestNewGitHubRepository_TakesTheBaseFromTheGitHubInstanceAndTheTokenFromTheGitHubSecret(t *testing.T) {
	t.Setenv(ghapi.HostsEnv, "")

	repo, err := gitops.NewGitHubRepository(githubDesired("https://API.github.com/"), fakeRepo)
	if err != nil {
		t.Fatalf("NewGitHubRepository: %v", err)
	}
	if repo.Repo != fakeRepo {
		t.Errorf("Repo = %q, want %q", repo.Repo, fakeRepo)
	}
	if repo.Client == nil {
		t.Fatal("Client = nil")
	}
	if repo.Client.Base != "https://api.github.com" {
		t.Errorf("Client.Base = %q, want the allowlist entry %q", repo.Client.Base, "https://api.github.com")
	}
	if repo.Client.Token != githubToken {
		t.Error("Client.Token is not the github secret's token")
	}
}

func TestNewGitHubRepository_AcceptsAHostTheOperatorAllowsThroughTheEnvironment(t *testing.T) {
	t.Setenv(ghapi.HostsEnv, "ghe.example-corp.com")

	repo, err := gitops.NewGitHubRepository(githubDesired("https://ghe.example-corp.com"), fakeRepo)
	if err != nil {
		t.Fatalf("NewGitHubRepository with an operator-allowed host: %v", err)
	}
	if repo.Client.Base != "https://ghe.example-corp.com" {
		t.Errorf("Client.Base = %q, want %q", repo.Client.Base, "https://ghe.example-corp.com")
	}
}

func TestNewGitHubRepository_IsAnErrorNeverCarryingTheTokenWhenTheBaseOrTokenIsMissingOrRefused(t *testing.T) {
	t.Setenv(ghapi.HostsEnv, "")
	for _, tc := range []struct {
		name string
		d    func() contract.Desired
	}{
		{"no instances", func() contract.Desired { d := desiredFor("app-alpha"); d.Instances = nil; return d }},
		{"no github instance", func() contract.Desired {
			d := desiredFor("app-alpha")
			d.Instances = map[string]contract.InstanceConfig{"hetzner": {Endpoint: "https://api.hetzner.cloud"}}
			return d
		}},
		{"an empty endpoint", func() contract.Desired { return githubDesired("") }},
		{"a host off the allowlist", func() contract.Desired { return githubDesired("https://evil.example.com") }},
		{"plain http", func() contract.Desired { return githubDesired("http://api.github.com") }},
		{"a loopback host", func() contract.Desired { return githubDesired("https://127.0.0.1") }},
		{"no github secret group", func() contract.Desired {
			d := githubDesired("https://api.github.com")
			delete(d.Secrets, "github")
			return d
		}},
		{"no token in the github group", func() contract.Desired {
			d := githubDesired("https://api.github.com")
			d.Secrets["github"] = map[string]string{"other": githubToken}
			return d
		}},
		{"an empty token", func() contract.Desired {
			d := githubDesired("https://api.github.com")
			d.Secrets["github"] = map[string]string{"token": ""}
			return d
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, err := gitops.NewGitHubRepository(tc.d(), fakeRepo)
			if err == nil {
				t.Fatalf("NewGitHubRepository = %+v, want an error", repo)
			}
			if strings.Contains(err.Error(), githubToken) {
				t.Errorf("the error carries the token: %v", err)
			}
		})
	}
}
