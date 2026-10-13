package gitops_test

// commit_test.go pins CommitManifests and SecretValues:
//
//   - CommitManifests commits the files on top of head and returns the new
//     revision; files that already stand at head commit nothing and return
//     head unchanged;
//   - a tip that moves between reading head and committing is re-read and
//     retried, a bounded number of times;
//   - a file holding any non-empty secret value, verbatim or as its standard
//     base64 encoding, is refused before anything is written, with an error
//     wrapping ErrSecretInManifest that names the path and never the value;
//   - SecretValues collects every value a Desired carries in SecretInputs,
//     AppSecrets and Secrets.

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/gitops"
	"github.com/mitstack-eu/operator-kit/v2/gitops/gitopstest"
)

const (
	nsPath = "apps/app-alpha/namespace.yaml"
	saPath = "apps/app-alpha/serviceaccount.yaml"
)

func alphaFiles() map[string][]byte {
	return map[string][]byte{
		nsPath: []byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: app-alpha\n"),
		saPath: []byte("apiVersion: v1\nkind: ServiceAccount\nmetadata:\n  name: app-alpha-runtime\n  namespace: app-alpha\n"),
	}
}

// wantFilesAt checks every file stands at revision with exactly its content.
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

func TestCommitManifests_CommitsTheFilesOnAnEmptyRepositoryAndReturnsTheNewHead(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()

	rev, changed, err := gitops.CommitManifests(ctx, repo, alphaFiles(), "app-alpha: namespace", nil)
	if err != nil {
		t.Fatalf("CommitManifests: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true: the repository held none of the files")
	}
	head, _ := repo.Head(ctx)
	if rev == "" || rev != head {
		t.Errorf("revision = %q, want the new head %q", rev, head)
	}
	commits := repo.Commits()
	if len(commits) != 1 {
		t.Fatalf("Commits = %d, want 1", len(commits))
	}
	if commits[0].Parent != "" || commits[0].Message != "app-alpha: namespace" {
		t.Errorf("commit parent %q message %q, want parent \"\" and the message handed in", commits[0].Parent, commits[0].Message)
	}
	wantFilesAt(t, repo, rev, alphaFiles())
}

func TestCommitManifests_FilesAlreadyAtHeadCommitNothingAndReturnHeadUnchanged(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	first, _, err := gitops.CommitManifests(ctx, repo, alphaFiles(), "app-alpha: namespace", nil)
	if err != nil {
		t.Fatalf("first CommitManifests: %v", err)
	}

	rev, changed, err := gitops.CommitManifests(ctx, repo, alphaFiles(), "app-alpha: namespace", nil)
	if err != nil {
		t.Fatalf("second CommitManifests: %v", err)
	}
	if changed {
		t.Error("changed = true, want false: every file already stood at head with that content")
	}
	if rev != first {
		t.Errorf("revision = %q, want head %q", rev, first)
	}
	if got := len(repo.Commits()); got != 1 {
		t.Errorf("Commits = %d, want 1: an unchanged commit must land nothing", got)
	}
}

func TestCommitManifests_OneChangedFileCommitsOnTopOfHeadAndKeepsOtherPaths(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	if _, err := repo.Commit(ctx, "", map[string][]byte{"apps/app-beta/namespace.yaml": []byte("beta\n")}, "app-beta"); err != nil {
		t.Fatalf("seeding app-beta: %v", err)
	}
	first, _, err := gitops.CommitManifests(ctx, repo, alphaFiles(), "app-alpha", nil)
	if err != nil {
		t.Fatalf("first CommitManifests: %v", err)
	}

	files := alphaFiles()
	files[saPath] = []byte("apiVersion: v1\nkind: ServiceAccount\nmetadata:\n  name: app-alpha-runtime\n  namespace: app-alpha\n  labels: {tier: web}\n")
	rev, changed, err := gitops.CommitManifests(ctx, repo, files, "app-alpha: label", nil)
	if err != nil {
		t.Fatalf("second CommitManifests: %v", err)
	}
	if !changed || rev == first {
		t.Fatalf("revision %q changed %v, want a new revision on top of %q", rev, changed, first)
	}
	commits := repo.Commits()
	if last := commits[len(commits)-1]; last.Parent != first {
		t.Errorf("the new commit's parent = %q, want head %q", last.Parent, first)
	}
	wantFilesAt(t, repo, rev, files)
	wantFilesAt(t, repo, rev, map[string][]byte{"apps/app-beta/namespace.yaml": []byte("beta\n")})
}

func TestCommitManifests_AMovedTipIsReReadAndRetriedOnTopOfTheOtherWritersCommit(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	foreign := map[string][]byte{"apps/app-beta/namespace.yaml": []byte("beta\n")}
	repo.MoveTip(2, foreign)

	rev, changed, err := gitops.CommitManifests(ctx, repo, alphaFiles(), "app-alpha", nil)
	if err != nil {
		t.Fatalf("CommitManifests after two moved tips: %v, want it to retry and land", err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	commits := repo.Commits()
	if len(commits) != 3 {
		t.Fatalf("Commits = %d, want 3: two foreign commits, then ours", len(commits))
	}
	ours := commits[2]
	if ours.Revision != rev || ours.Parent != commits[1].Revision {
		t.Errorf("our commit %q on parent %q, want %q on the second foreign commit %q", ours.Revision, ours.Parent, rev, commits[1].Revision)
	}
	wantFilesAt(t, repo, rev, alphaFiles())
	wantFilesAt(t, repo, rev, foreign)
}

func TestCommitManifests_ATipThatKeepsMovingEndsInErrStaleBaseAfterABoundedNumberOfTries(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	const forever = 1000
	repo.MoveTip(forever, map[string][]byte{"apps/app-beta/namespace.yaml": []byte("beta\n")})

	_, _, err := gitops.CommitManifests(ctx, repo, alphaFiles(), "app-alpha", nil)
	if !errors.Is(err, gitops.ErrStaleBase) {
		t.Fatalf("err = %v, want one wrapping gitops.ErrStaleBase", err)
	}
	tries := len(repo.Commits()) // each try lands one foreign commit and none of ours
	if tries < 2 {
		t.Errorf("CommitManifests tried %d time(s), want it to retry at least once", tries)
	}
	if tries >= forever {
		t.Errorf("CommitManifests tried %d times, want a bound well below %d", tries, forever)
	}
	head, _ := repo.Head(ctx)
	if _, ok, _ := repo.ReadFile(ctx, head, nsPath); ok {
		t.Error("app-alpha's namespace landed although every commit was stale")
	}
}

// secretManifest is a Kubernetes Secret carrying value in its data map, the
// standard base64 form, so a verbatim scan alone misses it.
func secretManifest(value string) []byte {
	return []byte("apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\ndata:\n  password: " +
		base64.StdEncoding.EncodeToString([]byte(value)) + "\n")
}

func TestCommitManifests_RefusesAFileHoldingASecretValueVerbatimBeforeAnyWrite(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	const secret = "s3cr3t-db-password-value"
	const leaky = "apps/app-alpha/configmap.yaml"
	files := alphaFiles()
	files[leaky] = []byte("apiVersion: v1\nkind: ConfigMap\ndata:\n  DATABASE_URL: postgres://app:" + secret + "@db/app\n")

	_, _, err := gitops.CommitManifests(ctx, repo, files, "app-alpha", []string{"unrelated-value", secret})
	wantSecretRefusal(t, err, leaky, secret)
	if got := len(repo.Commits()); got != 0 {
		t.Errorf("Commits = %d, want 0: a refused manifest set writes nothing, not even its clean files", got)
	}
}

func TestCommitManifests_RefusesAFileHoldingASecretValueBase64EncodedBeforeAnyWrite(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	const secret = "s3cr3t-db-password-value"
	const leaky = "apps/app-alpha/secret.yaml"
	files := alphaFiles()
	files[leaky] = secretManifest(secret)

	_, _, err := gitops.CommitManifests(ctx, repo, files, "app-alpha", []string{secret})
	wantSecretRefusal(t, err, leaky, secret)
	if got := len(repo.Commits()); got != 0 {
		t.Errorf("Commits = %d, want 0", got)
	}
}

// wantSecretRefusal checks err wraps ErrSecretInManifest, names path and
// carries the secret neither verbatim nor base64-encoded.
func wantSecretRefusal(t *testing.T, err error, path, secret string) {
	t.Helper()
	if !errors.Is(err, gitops.ErrSecretInManifest) {
		t.Fatalf("err = %v, want one wrapping gitops.ErrSecretInManifest", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, path) {
		t.Errorf("error %q does not name the offending path %s", msg, path)
	}
	if strings.Contains(msg, secret) {
		t.Error("the refusal's error text carries the secret value verbatim")
	}
	if strings.Contains(msg, base64.StdEncoding.EncodeToString([]byte(secret))) {
		t.Error("the refusal's error text carries the secret value base64-encoded")
	}
}

func TestCommitManifests_AnEmptySecretValueRefusesNothing(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()

	if _, _, err := gitops.CommitManifests(ctx, repo, alphaFiles(), "app-alpha", []string{"", "never-in-a-file"}); err != nil {
		t.Fatalf("CommitManifests with an empty secret value: %v, want the commit to land", err)
	}
	if got := len(repo.Commits()); got != 1 {
		t.Errorf("Commits = %d, want 1", got)
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
	want := []string{"app-secret-1", "ghs_platformMintedToken", "hz-token", "secret-input-1", "ssh-key-material"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SecretValues = %q, want %q (and never a param value)", got, want)
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
		Params: map[string]string{
			"appName":     "s4168-c1",
			"repoUrl":     "https://github.com/mitstack-eu/k4168c",
			"quotaCpu":    "1",
			"quotaMemory": "1Gi",
		},
		Secrets: map[string]map[string]string{
			"github":  {"token": sliceGithubToken, "owner": "mitstack-eu"},
			"cluster": {"clusterCredential": sliceClusterCred},
		},
	}
}

func sliceProject(t *testing.T) map[string][]byte {
	t.Helper()
	body, err := os.ReadFile("testdata/slice-app-project/app-s4168-c1-app-project.json")
	if err != nil {
		t.Fatalf("reading the rendered AppProject: %v", err)
	}
	return map[string][]byte{sliceProjectPath: body}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestCommitManifests_CommitsTheRenderedAppProjectOfAGithubOrgRepoUnderTheSliceRequest(t *testing.T) {
	d := sliceRequest()
	files := sliceProject(t)

	_, _, err := gitops.CommitManifests(context.Background(), gitopstest.NewRepository(), files, "app-s4168-c1: project", gitops.SecretValues(d))

	if err == nil {
		return
	}
	t.Errorf("CommitManifests refused the AppProject: %v (ErrSecretInManifest: %v)", err, errors.Is(err, gitops.ErrSecretInManifest))
	probe := func(where, value string) {
		_, _, single := gitops.CommitManifests(context.Background(), gitopstest.NewRepository(), files, "probe", []string{value})
		if single != nil {
			t.Errorf("refused by %s = %q (ErrSecretInManifest: %v)", where, value, errors.Is(single, gitops.ErrSecretInManifest))
		}
	}
	for _, k := range sortedKeys(d.SecretInputs) {
		probe("SecretInputs["+k+"]", d.SecretInputs[k])
	}
	for _, k := range sortedKeys(d.AppSecrets) {
		probe("AppSecrets["+k+"]", d.AppSecrets[k])
	}
	for _, g := range sortedKeys(d.Secrets) {
		for _, k := range sortedKeys(d.Secrets[g]) {
			probe(`Secrets["`+g+`"]["`+k+`"]`, d.Secrets[g][k])
		}
	}
}

func TestCommitManifests_RefusesAGithubOrClusterCredentialInAManifestEvenWhenAnOwnerIsPresent(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"the github token verbatim", sliceGithubToken},
		{"the github token as standard base64", base64.StdEncoding.EncodeToString([]byte(sliceGithubToken))},
		{"the cluster credential verbatim", sliceClusterCred},
		{"the cluster credential as standard base64", base64.StdEncoding.EncodeToString([]byte(sliceClusterCred))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string][]byte{"cluster/leak.json": []byte(`{"value":"` + tc.value + `"}`)}

			_, _, err := gitops.CommitManifests(context.Background(), gitopstest.NewRepository(), files, "leak", gitops.SecretValues(sliceRequest()))

			if !errors.Is(err, gitops.ErrSecretInManifest) {
				t.Errorf("CommitManifests carrying %s: err = %v, want ErrSecretInManifest", tc.name, err)
			}
		})
	}
}

func TestSecretValues_CollectsTheCredentialsAndNeverTheGithubOwner(t *testing.T) {
	d := sliceRequest()
	d.Secrets["github"]["pat"] = slicePAT

	got := gitops.SecretValues(d)

	has := func(v string) bool {
		for _, g := range got {
			if g == v {
				return true
			}
		}
		return false
	}
	for _, want := range []string{sliceGithubToken, sliceClusterCred, slicePAT} {
		if !has(want) {
			t.Errorf("SecretValues = %q, want it to contain the credential %q", got, want)
		}
	}
	if has("mitstack-eu") {
		t.Errorf("SecretValues contains the github owner %q, want it omitted", "mitstack-eu")
	}
}
