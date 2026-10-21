package gitops_test

// The test block is one kubernetes.namespace whose Namespace and ServiceAccount a block-level handler commits and a gitops.Resource observes.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/gitops"
	"github.com/mitstack-eu/operator-kit/v3/gitops/gitopstest"
)

const (
	resourceName = "app-namespace"
	resourceType = "kubernetes.namespace"
	clusterParam = "k3s-fsn1"

	githubToken   = "ghs_platformMintedInstallationToken0123"
	secretInput   = "secret-input-value-4567"
	appSecret     = "app-secret-value-89ab"
	hetznerSecret = "hetzner-api-token-cdef"

	alphaNamespacePath      = "apps/app-alpha/namespace.yaml"
	alphaServiceAccountPath = "apps/app-alpha/serviceaccount.yaml"
)

var seededSecrets = []string{githubToken, secretInput, appSecret, hetznerSecret}

func readNamespaceManifest(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/kubernetes-namespace/manifest.yaml")
	if err != nil {
		t.Fatalf("reading the test block's manifest: %v", err)
	}
	return raw
}

func desiredFor(app string) contract.Desired {
	return contract.Desired{
		Identity: contract.Identity{
			SubscriptionID: "sub-" + app,
			BlockID:        "gitops-app-namespace",
			BlockVersion:   "1.0.0",
			OrgID:          "org-1",
			AppID:          app,
		},
		Params:       map[string]string{"appName": app, "cluster": clusterParam},
		SecretInputs: map[string]string{"dbPassword": secretInput},
		AppSecrets:   map[string]string{"STRIPE_KEY": appSecret},
		Secrets: map[string]map[string]string{
			"github":  {"token": githubToken},
			"hetzner": {"apiToken": hetznerSecret},
		},
	}
}

func renderNamespace(d contract.Desired) (gitops.Rendered, error) {
	cluster := d.Params["cluster"]
	if cluster == "" {
		return gitops.Rendered{}, errors.New("no cluster param")
	}
	if d.AppID == "" {
		return gitops.Rendered{}, errors.New("no app id")
	}
	return renderApp(d.AppID, cluster), nil
}

func renderApp(app, cluster string) gitops.Rendered {
	sa := app + "-runtime"
	return gitops.Rendered{
		App: app,
		Files: map[string][]byte{
			"apps/" + app + "/namespace.yaml": []byte(fmt.Sprintf(
				"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n  annotations:\n    mitstack.dev/cluster: %s\n", app, cluster)),
			"apps/" + app + "/serviceaccount.yaml": []byte(fmt.Sprintf(
				"apiVersion: v1\nkind: ServiceAccount\nmetadata:\n  name: %s\n  namespace: %s\n", sa, app)),
		},
		Outputs: map[string]string{"namespace": app, "serviceAccount": sa},
	}
}

func alphaOutputs() map[string]string {
	return map[string]string{"namespace": "app-alpha", "serviceAccount": "app-alpha-runtime"}
}

type gitopsBlock struct {
	repo     *gitopstest.Repository
	notes    *gitopstest.Notifications
	resource *gitops.Resource
}

// newBlock builds the block over a notification source answering Succeeded and Healthy for every revision of each autoApps app.
func newBlock(render func(contract.Desired) (gitops.Rendered, error), autoApps ...string) *gitopsBlock {
	b := &gitopsBlock{repo: gitopstest.NewRepository(), notes: gitopstest.NewNotifications()}
	for _, app := range autoApps {
		b.notes.SucceedCommits(app, b.repo)
	}
	b.resource = &gitops.Resource{
		Repository: func(context.Context, contract.Desired) (gitops.Repository, error) {
			return b.repo, nil
		},
		Notifications: func(context.Context, contract.Desired) (gitops.Notifications, error) {
			return b.notes, nil
		},
		Render: render,
	}
	return b
}

func newNamespaceBlock() *gitopsBlock {
	return newBlock(renderNamespace, "app-alpha", "app-beta")
}

func (b *gitopsBlock) head(t *testing.T) string {
	t.Helper()
	head, err := b.repo.Head(context.Background())
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	return head
}

// commitApp commits d's render through CommitManifests, as a block-level handler does, and returns the revision holding it.
func commitApp(t *testing.T, b *gitopsBlock, d contract.Desired) string {
	t.Helper()
	out, err := b.resource.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	rev, _, err := gitops.CommitManifests(context.Background(), b.repo, out.Files, out.App+": update manifests", gitops.SecretValues(d))
	if err != nil {
		t.Fatalf("CommitManifests: %v", err)
	}
	return rev
}

func removeApp(t *testing.T, b *gitopsBlock, d contract.Desired) string {
	t.Helper()
	out, err := b.resource.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	removal := make(map[string][]byte, len(out.Files))
	for path := range out.Files {
		removal[path] = nil
	}
	rev, _, err := gitops.CommitManifests(context.Background(), b.repo, removal, out.App+": remove manifests", nil)
	if err != nil {
		t.Fatalf("CommitManifests of the removal: %v", err)
	}
	return rev
}

func refFor(d contract.Desired) contract.ResourceRef {
	return contract.ResourceRef{Name: resourceName, Type: resourceType, Identity: map[string]string{"app": d.AppID}, Desired: d}
}
