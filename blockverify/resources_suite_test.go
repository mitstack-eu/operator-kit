package main

// The resources suite requires a v1beta block's TestDeclaredResources to call kittest.AssertBlock, and skips any other manifest.

import (
	"strings"
	"testing"
)

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
  provides: [instance]
  lifecycle:
    endpoint: https://api.mitstack.dev/operators/blockverify-resources-fixture/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`

const resourcesFixtureMainGo = `package main

func main() {}
`

// resourcesFixture writes a module holding manifest and testGo as its
// resources test, and returns its directory.
func resourcesFixture(t *testing.T, manifest, testGo string) string {
	t.Helper()
	goMod := "module fixture\n\ngo 1.25\n\n" +
		"require github.com/mitstack-eu/operator-kit/v2 v2.0.0\n\n" +
		"replace github.com/mitstack-eu/operator-kit/v2 => " + kitModuleDir(t) + "\n"
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
	requireResourcesFailNaming(t, resourcesSuite(dir), "kittest.AssertBlock")
}

func TestResourcesSuite_FailsWhenTestDeclaredResourcesCallsOnlyTheRetiredPerResourceCheck(t *testing.T) {
	testGo := "package main\n\nimport \"testing\"\n\nfunc TestDeclaredResources(t *testing.T) {\n\t_ = \"kittest.AssertDeclaredResources(\"\n}\n"
	dir := resourcesFixture(t, resourcesFixtureManifest, testGo)

	got := resourcesSuite(dir)

	requireResourcesFailNaming(t, got, "kittest.AssertBlock")
}
