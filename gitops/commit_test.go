package gitops_test

// CommitManifests commits on head what differs, retrying a moved tip, and refuses a secret before any write; Revert restores what a revision changed.

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/gitops"
	"github.com/mitstack-eu/operator-kit/v2/gitops/gitopstest"
)

const (
	nsPath     = "apps/app-alpha/namespace.yaml"
	saPath     = "apps/app-alpha/serviceaccount.yaml"
	betaPath   = "apps/app-beta/namespace.yaml"
	dbPassword = "s3cr3t-db-password-value"
)

func alphaFiles() map[string][]byte {
	return map[string][]byte{
		nsPath: []byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: app-alpha\n"),
		saPath: []byte("apiVersion: v1\nkind: ServiceAccount\nmetadata:\n  name: app-alpha-runtime\n  namespace: app-alpha\n"),
	}
}

func with(files map[string][]byte, path string, content []byte) map[string][]byte {
	files[path] = content
	return files
}

// wantFilesAt checks every file stands at revision with exactly its content, a nil content meaning absent.
func wantFilesAt(t *testing.T, repo gitops.Repository, revision string, files map[string][]byte) {
	t.Helper()
	for path, want := range files {
		got, ok, err := repo.ReadFile(context.Background(), revision, path)
		switch {
		case err != nil:
			t.Errorf("ReadFile(%s, %s): %v", revision, path, err)
		case want == nil && ok:
			t.Errorf("%s stands at %s with %q, want it absent", path, revision, got)
		case want != nil && (!ok || string(got) != string(want)):
			t.Errorf("%s at %s = %q (present %v), want %q", path, revision, got, ok, want)
		}
	}
}

func TestCommitManifests(t *testing.T) {
	ctx := context.Background()
	foreign := map[string][]byte{betaPath: []byte("beta\n")}
	cases := []struct {
		name        string
		seed        func(t *testing.T, repo *gitopstest.Repository) string
		files       map[string][]byte
		secrets     []string
		wantChanged bool
		wantErr     error
		check       func(t *testing.T, repo *gitopstest.Repository, seeded, rev string, err error)
	}{
		{"on an empty repository it commits the files under the message and returns the new head", nil, alphaFiles(), nil, true, nil,
			func(t *testing.T, repo *gitopstest.Repository, _, rev string, _ error) {
				head, _ := repo.Head(ctx)
				commits := repo.Commits()
				if rev == "" || rev != head || len(commits) != 1 || commits[0].Parent != "" || commits[0].Message != "app-alpha" {
					t.Errorf("revision %q, head %q, commits %+v; want one parentless commit with the message handed in at head", rev, head, commits)
				}
				wantFilesAt(t, repo, rev, alphaFiles())
			}},
		{"files already at head commit nothing and return head", func(t *testing.T, repo *gitopstest.Repository) string {
			return mustCommit(t, repo, alphaFiles())
		}, alphaFiles(), nil, false, nil,
			func(t *testing.T, repo *gitopstest.Repository, seeded, rev string, _ error) {
				if rev != seeded || len(repo.Commits()) != 1 {
					t.Errorf("revision %q with %d commits, want head %q and no new commit", rev, len(repo.Commits()), seeded)
				}
			}},
		{"one changed file commits on top of head and keeps every other path", func(t *testing.T, repo *gitopstest.Repository) string {
			if _, err := repo.Commit(ctx, "", foreign, "app-beta"); err != nil {
				t.Fatal(err)
			}
			return mustCommit(t, repo, alphaFiles())
		}, with(alphaFiles(), saPath, []byte("kind: ServiceAccount\nlabels: {tier: web}\n")), nil, true, nil,
			func(t *testing.T, repo *gitopstest.Repository, seeded, rev string, _ error) {
				commits := repo.Commits()
				if last := commits[len(commits)-1]; rev == seeded || last.Parent != seeded {
					t.Errorf("revision %q on parent %q, want a new revision on head %q", rev, last.Parent, seeded)
				}
				wantFilesAt(t, repo, rev, with(alphaFiles(), saPath, []byte("kind: ServiceAccount\nlabels: {tier: web}\n")))
				wantFilesAt(t, repo, rev, foreign)
			}},
		{"a tip another writer moves twice is re-read and the commit lands on its latest commit", func(t *testing.T, repo *gitopstest.Repository) string {
			repo.MoveTip(2, foreign)
			return ""
		}, alphaFiles(), nil, true, nil,
			func(t *testing.T, repo *gitopstest.Repository, _, rev string, _ error) {
				commits := repo.Commits()
				if len(commits) != 3 || commits[2].Revision != rev || commits[2].Parent != commits[1].Revision {
					t.Fatalf("commits %+v, want two foreign commits, then ours %q on the second", commits, rev)
				}
				wantFilesAt(t, repo, rev, alphaFiles())
				wantFilesAt(t, repo, rev, foreign)
			}},
		{"a tip that keeps moving ends in ErrStaleBase after a bounded number of tries", func(t *testing.T, repo *gitopstest.Repository) string {
			repo.MoveTip(1000, foreign)
			return ""
		}, alphaFiles(), nil, false, gitops.ErrStaleBase,
			func(t *testing.T, repo *gitopstest.Repository, _, _ string, _ error) {
				// Each try lands one foreign commit and none of ours.
				if tries := len(repo.Commits()); tries < 2 || tries >= 1000 {
					t.Errorf("CommitManifests tried %d times, want a retry and a bound well below 1000", tries)
				}
				head, _ := repo.Head(ctx)
				wantFilesAt(t, repo, head, map[string][]byte{nsPath: nil})
			}},
		{"a file holding a secret verbatim is refused before any write", nil,
			with(alphaFiles(), "apps/app-alpha/configmap.yaml", []byte("kind: ConfigMap\ndata:\n  DATABASE_URL: postgres://app:"+dbPassword+"@db/app\n")),
			[]string{"unrelated-value", dbPassword}, false, gitops.ErrSecretInManifest, refusedNaming("apps/app-alpha/configmap.yaml")},
		{"a file holding a secret as its standard base64 is refused before any write", nil,
			with(alphaFiles(), "apps/app-alpha/secret.yaml", []byte("kind: Secret\ndata:\n  password: "+base64.StdEncoding.EncodeToString([]byte(dbPassword))+"\n")),
			[]string{dbPassword}, false, gitops.ErrSecretInManifest, refusedNaming("apps/app-alpha/secret.yaml")},
		{"an empty secret value refuses nothing", nil, alphaFiles(), []string{"", "never-in-a-file"}, true, nil,
			func(t *testing.T, repo *gitopstest.Repository, _, _ string, _ error) {
				if got := len(repo.Commits()); got != 1 {
					t.Errorf("Commits = %d, want 1", got)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := gitopstest.NewRepository()
			seeded := ""
			if tc.seed != nil {
				seeded = tc.seed(t, repo)
			}

			rev, changed, err := gitops.CommitManifests(ctx, repo, tc.files, "app-alpha", tc.secrets)

			if changed != tc.wantChanged || !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("CommitManifests = %q, %v, %v; want changed %v and error %v", rev, changed, err, tc.wantChanged, tc.wantErr)
			}
			if err != nil {
				for _, secret := range tc.secrets {
					if secret != "" && (strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), base64.StdEncoding.EncodeToString([]byte(secret)))) {
						t.Errorf("error %q carries a secret value, verbatim or base64", err)
					}
				}
			}
			tc.check(t, repo, seeded, rev, err)
		})
	}
}

func mustCommit(t *testing.T, repo *gitopstest.Repository, files map[string][]byte) string {
	t.Helper()
	rev, _, err := gitops.CommitManifests(context.Background(), repo, files, "seed", nil)
	if err != nil {
		t.Fatalf("seeding CommitManifests: %v", err)
	}
	return rev
}

// refusedNaming checks a refused manifest set names its offending path and writes nothing, not even its clean files.
func refusedNaming(path string) func(t *testing.T, repo *gitopstest.Repository, _, _ string, err error) {
	return func(t *testing.T, repo *gitopstest.Repository, _, _ string, err error) {
		t.Helper()
		if got := len(repo.Commits()); got != 0 {
			t.Errorf("Commits = %d, want 0", got)
		}
		if !strings.Contains(err.Error(), path) {
			t.Errorf("refusal %q does not name the offending path %s", err, path)
		}
	}
}

func TestRevert(t *testing.T) {
	ctx := context.Background()
	commit := func(t *testing.T, repo *gitopstest.Repository, parent string, changes map[string][]byte) string {
		t.Helper()
		rev, err := repo.Commit(ctx, parent, changes, "seed")
		if err != nil {
			t.Fatalf("seeding a commit: %v", err)
		}
		return rev
	}
	cases := []struct {
		name string
		// history lands commits and returns the revision to revert.
		history     func(t *testing.T, repo *gitopstest.Repository) string
		wantFiles   map[string][]byte
		wantCommits int
		wantErr     bool
	}{
		{"each changed path is restored to its parent's content and an added one deleted", func(t *testing.T, repo *gitopstest.Repository) string {
			base := commit(t, repo, "", map[string][]byte{"a.yaml": []byte("a1\n"), "b.yaml": []byte("b1\n")})
			return commit(t, repo, base, map[string][]byte{"a.yaml": []byte("a2\n"), "b.yaml": nil, "c.yaml": []byte("c2\n")})
		}, map[string][]byte{"a.yaml": []byte("a1\n"), "b.yaml": []byte("b1\n"), "c.yaml": nil}, 3, false},
		{"the revert lands on the current head and leaves a later unrelated commit alone", func(t *testing.T, repo *gitopstest.Repository) string {
			base := commit(t, repo, "", map[string][]byte{"apps/app-alpha/deploy.yaml": []byte("image: v1\n")})
			bad := commit(t, repo, base, map[string][]byte{"apps/app-alpha/deploy.yaml": []byte("image: v2\n")})
			commit(t, repo, bad, map[string][]byte{"apps/app-beta/deploy.yaml": []byte("image: b1\n")})
			return bad
		}, map[string][]byte{"apps/app-alpha/deploy.yaml": []byte("image: v1\n"), "apps/app-beta/deploy.yaml": []byte("image: b1\n")}, 4, false},
		{"reverting the first commit deletes every path it added", func(t *testing.T, repo *gitopstest.Repository) string {
			return commit(t, repo, "", map[string][]byte{"a.yaml": []byte("a\n"), "b.yaml": []byte("b\n")})
		}, map[string][]byte{"a.yaml": nil, "b.yaml": nil}, 2, false},
		{"a head already matching the parent commits nothing and returns head", func(t *testing.T, repo *gitopstest.Repository) string {
			base := commit(t, repo, "", map[string][]byte{"a.yaml": []byte("a1\n")})
			bad := commit(t, repo, base, map[string][]byte{"a.yaml": []byte("a2\n")})
			commit(t, repo, bad, map[string][]byte{"a.yaml": []byte("a1\n")})
			return bad
		}, map[string][]byte{"a.yaml": []byte("a1\n")}, 3, false},
		{"a revision the repository never held fails and commits nothing", func(t *testing.T, repo *gitopstest.Repository) string {
			commit(t, repo, "", map[string][]byte{"a.yaml": []byte("a\n")})
			return "no-such-revision"
		}, nil, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := gitopstest.NewRepository()
			reverted := tc.history(t, repo)
			headBefore, _ := repo.Head(ctx)

			rev, err := gitops.Revert(ctx, repo, reverted, "revert")

			commits := repo.Commits()
			if (err != nil) != tc.wantErr || len(commits) != tc.wantCommits {
				t.Fatalf("Revert(%s) = %q, %v with %d commits; want error %v and %d commits", reverted, rev, err, len(commits), tc.wantErr, tc.wantCommits)
			}
			if tc.wantErr {
				return
			}
			if last := commits[len(commits)-1]; last.Revision != rev || (rev != headBefore && (last.Parent != headBefore || last.Message != "revert")) {
				t.Errorf("revert %q is commit %+v; want it head, on %q with the message handed in, or head itself when nothing changed", rev, last, headBefore)
			}
			wantFilesAt(t, repo, rev, tc.wantFiles)
		})
	}
}

func TestSecretValues_CollectsEveryValueInSecretInputsAppSecretsAndSecrets(t *testing.T) {
	d := contract.Desired{
		Params:       map[string]string{"region": "fsn1"},
		SecretInputs: map[string]string{"dbPassword": "secret-input-1"},
		AppSecrets:   map[string]string{"STRIPE_KEY": "app-secret-1"},
		Secrets: map[string]map[string]string{
			"github":  {"token": "ghs_platformMintedToken"},
			"hetzner": {"apiToken": "hz-token", "sshKey": "ssh-key-material"},
		},
	}

	got := gitops.SecretValues(d)

	sort.Strings(got)
	if want := []string{"app-secret-1", "ghs_platformMintedToken", "hz-token", "secret-input-1", "ssh-key-material"}; !reflect.DeepEqual(got, want) {
		t.Errorf("SecretValues = %q, want %q and never a param value", got, want)
	}
}

const (
	sliceProjectPath = "cluster/app-s4168-c1-app-project.json"
	sliceGithubToken = "ghs_a1B2c3D4e5F6g7H8i9J0k1L2m3N4o5P6q7R8"
	sliceClusterCred = "k3s-credential-Zm9vYmFyYmF6cXV4"
	slicePAT         = "github_pat_fake0123456789"
)

func sliceRequest() contract.Desired {
	return contract.Desired{
		Params: map[string]string{"appName": "s4168-c1", "repoUrl": "https://github.com/mitstack-eu/k4168c", "quotaCpu": "1", "quotaMemory": "1Gi"},
		Secrets: map[string]map[string]string{
			"github":  {"token": sliceGithubToken, "owner": "mitstack-eu"},
			"cluster": {"clusterCredential": sliceClusterCred},
		},
	}
}

func TestCommitManifests_TheSliceRequestsSecrets(t *testing.T) {
	project, err := os.ReadFile("testdata/slice-app-project/app-s4168-c1-app-project.json")
	if err != nil {
		t.Fatalf("reading the rendered AppProject: %v", err)
	}
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	cases := []struct {
		name  string
		files map[string][]byte
		want  error
	}{
		{"the rendered AppProject of a github org repo commits, since the owner is no credential", map[string][]byte{sliceProjectPath: project}, nil},
		{"the github token verbatim is refused", map[string][]byte{"cluster/leak.json": []byte(`{"value":"` + sliceGithubToken + `"}`)}, gitops.ErrSecretInManifest},
		{"the github token as standard base64 is refused", map[string][]byte{"cluster/leak.json": []byte(`{"value":"` + b64(sliceGithubToken) + `"}`)}, gitops.ErrSecretInManifest},
		{"the cluster credential verbatim is refused", map[string][]byte{"cluster/leak.json": []byte(`{"value":"` + sliceClusterCred + `"}`)}, gitops.ErrSecretInManifest},
		{"the cluster credential as standard base64 is refused", map[string][]byte{"cluster/leak.json": []byte(`{"value":"` + b64(sliceClusterCred) + `"}`)}, gitops.ErrSecretInManifest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := gitops.CommitManifests(context.Background(), gitopstest.NewRepository(), tc.files, "app-s4168-c1", gitops.SecretValues(sliceRequest()))

			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Errorf("CommitManifests = %v, want %v", err, tc.want)
			}
			for _, v := range gitops.SecretValues(sliceRequest()) {
				if _, _, single := gitops.CommitManifests(context.Background(), gitopstest.NewRepository(), tc.files, "probe", []string{v}); tc.want == nil && err != nil && single != nil {
					t.Errorf("the secret value %q alone refuses the AppProject: %v", v, single)
				}
			}
		})
	}
}

func TestSecretValues_CollectsTheCredentialsAndNeverTheGithubOwner(t *testing.T) {
	d := sliceRequest()
	d.Secrets["github"]["pat"] = slicePAT

	got := gitops.SecretValues(d)

	for _, want := range []string{sliceGithubToken, sliceClusterCred, slicePAT} {
		if !slices.Contains(got, want) {
			t.Errorf("SecretValues = %q, want it to contain the credential %q", got, want)
		}
	}
	if slices.Contains(got, "mitstack-eu") {
		t.Errorf("SecretValues = %q, want the github owner omitted", got)
	}
}
