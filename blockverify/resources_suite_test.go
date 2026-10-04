package main

// resources_suite_test.go pins the resources suite: for a block whose
// manifest is mitstack/v1beta, the suite requires a TestDeclaredResources
// test that calls kittest.AssertDeclaredResources, runs it, and passes only
// when every declared resource survives ensure twice, observe, delete and
// the absence proof, and every supplied resource still stands afterwards. A
// failure names the resource. A manifest that is not mitstack/v1beta
// declares no resources, so the suite is skipped.
//
// Each fixture below is a compiling module written as literal strings into
// t.TempDir(), replacing the kit module with this checkout the way
// fixture_test.go does, with in-memory resources in its test file.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resourcesFixtureManifest declares network then instance.
const resourcesFixtureManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: blockverify-resources-fixture
  name: blockverify resources fixture
  owner: blockverify-tests
spec:
  version: 1.0.0
  description: A fixture block that declares two resources.
  resources:
    - name: network
      type: aws.vpc
    - name: instance
      type: aws.ec2-instance
      inputs: { network: resources.network }
  provides: [instance]
  lifecycle:
    endpoint: https://api.mitstack.dev/operators/blockverify-resources-fixture/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`

// suppliedFixtureManifest requires a git-repository the app supplies and
// declares a role that trusts it.
const suppliedFixtureManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: blockverify-supplied-fixture
  name: blockverify supplied fixture
  owner: blockverify-tests
spec:
  version: 1.0.0
  description: A fixture block that requires a supplied repository.
  requires:
    - kind: git-repository
      in: github
  resources:
    - name: ecr-push-role
      type: aws.iam-role
      inputs: { trustedRepository: requires.git-repository.outputs.repo }
  lifecycle:
    endpoint: https://api.mitstack.dev/operators/blockverify-supplied-fixture/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`

const resourcesFixtureMainGo = `package main

func main() {}
`

// twoResourcesTestGo returns a TestDeclaredResources over network and
// instance, with instanceFields set on the instance's kittest.FakeResource.
func twoResourcesTestGo(instanceFields string) string {
	return `package main

import (
	"os"
	"testing"

	"github.com/mitstack-eu/operator-kit/contract"
	"github.com/mitstack-eu/operator-kit/handler"
	"github.com/mitstack-eu/operator-kit/kittest"
)

func TestDeclaredResources(t *testing.T) {
	manifest, err := os.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	network := &kittest.FakeResource{Outputs: map[string]string{"vpcId": "vpc-0abc"}}
	instance := &kittest.FakeResource{Outputs: map[string]string{"publicIp": "203.0.113.7"}` + instanceFields + `}
	kittest.AssertDeclaredResources(t, kittest.DeclaredResources{
		Manifest:  manifest,
		Resources: map[string]handler.DeclaredResource{"network": network, "instance": instance},
		Desired: contract.Desired{
			Identity: contract.Identity{SubscriptionID: "sub-fixture", BlockID: "blockverify-resources-fixture", OrgID: "org-fixture"},
		},
	})
}
`
}

// suppliedResourceTestGo returns a TestDeclaredResources over the
// ecr-push-role, whose Delete also removes the supplied repository when
// removesRepository is "true".
func suppliedResourceTestGo(removesRepository string) string {
	return `package main

import (
	"context"
	"os"
	"testing"

	"github.com/mitstack-eu/operator-kit/contract"
	"github.com/mitstack-eu/operator-kit/handler"
	"github.com/mitstack-eu/operator-kit/kittest"
)

// repository is the git repository the app supplies to the block.
type repository struct{ present bool }

func (r *repository) Observe(ctx context.Context, id contract.Identity) (contract.Observation, error) {
	if r.present {
		return contract.Observation{State: contract.Present, Outputs: map[string]string{"repo": "acme/app"}}, nil
	}
	return contract.Observation{State: contract.Absent}, nil
}

// pushRole is the role the block creates. When removesRepository is set,
// its Delete also deletes the repository the role trusts.
type pushRole struct {
	*kittest.FakeResource
	repo              *repository
	removesRepository bool
}

func (p *pushRole) Delete(ctx context.Context, id contract.Identity) error {
	if p.removesRepository {
		p.repo.present = false
	}
	return p.FakeResource.Delete(ctx, id)
}

func TestDeclaredResources(t *testing.T) {
	manifest, err := os.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	repo := &repository{present: true}
	role := &pushRole{
		FakeResource:      &kittest.FakeResource{Outputs: map[string]string{"roleArn": "arn:aws:iam::123456789012:role/push"}},
		repo:              repo,
		removesRepository: ` + removesRepository + `,
	}
	kittest.AssertDeclaredResources(t, kittest.DeclaredResources{
		Manifest:  manifest,
		Resources: map[string]handler.DeclaredResource{"ecr-push-role": role},
		Supplied:  map[string]kittest.SuppliedResource{"git-repository": repo},
		Desired: contract.Desired{
			Identity: contract.Identity{SubscriptionID: "sub-fixture", BlockID: "blockverify-supplied-fixture", OrgID: "org-fixture"},
		},
	})
}
`
}

// resourcesFixture writes a module holding manifest and testGo as its
// resources test, and returns its directory.
func resourcesFixture(t *testing.T, manifest, testGo string) string {
	t.Helper()
	goMod := "module fixture\n\ngo 1.25\n\n" +
		"require github.com/mitstack-eu/operator-kit v0.1.0\n\n" +
		"replace github.com/mitstack-eu/operator-kit => " + kitModuleDir(t) + "\n"
	return newFixtureFrom(t, map[string]string{
		"go.mod":            goMod,
		"main.go":           resourcesFixtureMainGo,
		"manifest.yaml":     manifest,
		"resources_test.go": testGo,
	})
}

func requireResourcesStatus(t *testing.T, got SuiteResult, status string) {
	t.Helper()
	if got.Name != "resources" {
		t.Errorf("suite name = %q, want %q", got.Name, "resources")
	}
	if got.Status != status {
		t.Errorf("resources status = %q, want %q; detail: %s", got.Status, status, got.Detail)
	}
	if got.Detail == "" {
		t.Error("the resources suite must say what it found in detail")
	}
}

func requireResourcesFailNaming(t *testing.T, got SuiteResult, want string) {
	t.Helper()
	requireResourcesStatus(t, got, "fail")
	if !strings.Contains(got.Detail, want) {
		t.Errorf("resources detail = %q, want it to name %s", got.Detail, want)
	}
}

// --- which blocks the suite applies to --------------------------------------

func TestResourcesSuite_SkipsAManifestThatIsNotV1Beta(t *testing.T) {
	got := resourcesSuite(manifestDir(t, manifestHead))
	requireResourcesStatus(t, got, "skipped")
	if !strings.Contains(got.Detail, "mitstack/v1beta") {
		t.Errorf("resources detail = %q, want it to say the manifest is not mitstack/v1beta", got.Detail)
	}
}

func TestResourcesSuite_FailsWhenTheBlockDefinesNoTestDeclaredResources(t *testing.T) {
	dir := resourcesFixture(t, resourcesFixtureManifest, "package main\n\nimport \"testing\"\n\nfunc TestSomethingElse(t *testing.T) {}\n")
	requireResourcesFailNaming(t, resourcesSuite(dir), "TestDeclaredResources")
}

func TestResourcesSuite_FailsWhenTestDeclaredResourcesNeverCallsTheKitCheck(t *testing.T) {
	dir := resourcesFixture(t, resourcesFixtureManifest, "package main\n\nimport \"testing\"\n\nfunc TestDeclaredResources(t *testing.T) {}\n")
	requireResourcesFailNaming(t, resourcesSuite(dir), "kittest.AssertDeclaredResources")
}

// --- declared resources ---------------------------------------------------------

func TestResourcesSuite_PassesAgainstABlockWhoseResourcesConverge(t *testing.T) {
	dir := resourcesFixture(t, resourcesFixtureManifest, twoResourcesTestGo(""))
	requireResourcesStatus(t, resourcesSuite(dir), "pass")
}

func TestResourcesSuite_FailsNamingAResourceItsSecondEnsureChanges(t *testing.T) {
	dir := resourcesFixture(t, resourcesFixtureManifest, twoResourcesTestGo(", ClobberOnUpgrade: true"))
	requireResourcesFailNaming(t, resourcesSuite(dir), `resource "instance"`)
}

func TestResourcesSuite_FailsNamingAResourceItsDeleteLeavesStanding(t *testing.T) {
	dir := resourcesFixture(t, resourcesFixtureManifest, twoResourcesTestGo(", LeakOnDelete: true"))
	requireResourcesFailNaming(t, resourcesSuite(dir), `resource "instance"`)
}

// --- a supplied resource -----------------------------------------------------------

func TestResourcesSuite_FailsNamingASuppliedResourceTheBlockDeletes(t *testing.T) {
	dir := resourcesFixture(t, suppliedFixtureManifest, suppliedResourceTestGo("true"))
	requireResourcesFailNaming(t, resourcesSuite(dir), `supplied resource "git-repository"`)
}

func TestResourcesSuite_PassesWhenTheBlockLeavesASuppliedResourceStanding(t *testing.T) {
	dir := resourcesFixture(t, suppliedFixtureManifest, suppliedResourceTestGo("false"))
	requireResourcesStatus(t, resourcesSuite(dir), "pass")
}

// --- a resource the manifest no longer declares -------------------------------

// droppedByTypeFixtureDir holds a block whose manifest declares network
// alone and whose resources test deletes firewall, which it no longer
// declares, through the handler it registers for firewall's recorded type.
const droppedByTypeFixtureDir = "testdata/resources-dropped-by-type"

// droppedTypeHandler is the line registering that handler, and
// noDroppedTypeHandler the same line registering none.
const (
	droppedTypeHandler   = `Types:     map[string]handler.DeclaredResource{"aws.security-group": firewall},`
	noDroppedTypeHandler = `Types:     map[string]handler.DeclaredResource{},`
)

// droppedByTypeFixture writes the testdata block as a module with each old,
// new pair of edits applied once to its resources test, failing the test
// when an old text is not in it.
func droppedByTypeFixture(t *testing.T, edits ...string) string {
	t.Helper()
	manifest, err := os.ReadFile(filepath.Join(droppedByTypeFixtureDir, manifestFile))
	if err != nil {
		t.Fatalf("reading the fixture manifest: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(droppedByTypeFixtureDir, "resources_test.go"))
	if err != nil {
		t.Fatalf("reading the fixture resources test: %v", err)
	}
	testGo := string(raw)
	for i := 0; i+1 < len(edits); i += 2 {
		if !strings.Contains(testGo, edits[i]) {
			t.Fatalf("fixture edit: %q is not in %s/resources_test.go", edits[i], droppedByTypeFixtureDir)
		}
		testGo = strings.Replace(testGo, edits[i], edits[i+1], 1)
	}
	return resourcesFixture(t, string(manifest), testGo)
}

func TestResourcesSuite_PassesAgainstABlockThatDeletesADroppedResourceByItsTypeAndProvesItAbsent(t *testing.T) {
	requireResourcesStatus(t, resourcesSuite(droppedByTypeFixture(t)), "pass")
}

func TestResourcesSuite_FailsNamingTheTypeWhenTheBlockHasNoHandlerForADroppedResourcesType(t *testing.T) {
	got := resourcesSuite(droppedByTypeFixture(t, droppedTypeHandler, noDroppedTypeHandler))
	requireResourcesFailNaming(t, got, `resource "firewall"`)
	if !strings.Contains(got.Detail, `"aws.security-group"`) {
		t.Errorf("resources detail = %q, want it to name the type %q no handler is registered for", got.Detail, "aws.security-group")
	}
}
