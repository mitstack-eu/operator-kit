package main

// blockverify's manifest suite reads a kubernetes.namespace block that requires a hetzner.k3s-cluster, and refuses one placing the namespace with on:.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

const (
	namespaceOnClusterDir = "manifest-kubernetes-namespace-on-cluster"
	namespaceCarriesOnDir = "manifest-namespace-carries-on"
)

// namespaceCarriesOnEdits turn the on-cluster fixture into the one carrying
// on:, as an old, new pair applied once.
var namespaceCarriesOnEdits = []string{
	"  resources: [kubernetes.namespace]\n",
	"  resources:\n    - { type: kubernetes.namespace, on: requires.hetzner.k3s-cluster }\n",
}

// readFixtureManifest returns the manifest of the testdata fixture dir.
func readFixtureManifest(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", dir, manifestFile))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	return string(raw)
}

func TestRun_PassesTheManifestSuiteOnAKubernetesNamespaceBlock(t *testing.T) {
	raw := readFixtureManifest(t, namespaceOnClusterDir)
	if !strings.HasPrefix(raw, "apiVersion: "+contract.ManifestAPIVersion+"\n") {
		t.Fatalf("the fixture is not a %s manifest", contract.ManifestAPIVersion)
	}
	stdout, _ := runBlockverify(t, "--json", "--standalone", filepath.Join("testdata", namespaceOnClusterDir))
	m := suiteByName(t, decodeReport(t, stdout), "manifest")
	if m.Status != "pass" {
		t.Errorf("manifest status = %q, want %q; detail: %s", m.Status, "pass", m.Detail)
	}
}

func TestRun_FailsTheManifestSuiteOnANamespacePlacedWithOn(t *testing.T) {
	want := readFixtureManifest(t, namespaceOnClusterDir)
	for i := 0; i+1 < len(namespaceCarriesOnEdits); i += 2 {
		if !strings.Contains(want, namespaceCarriesOnEdits[i]) {
			t.Fatalf("fixture edit: %q is not in %s/%s", namespaceCarriesOnEdits[i], namespaceOnClusterDir, manifestFile)
		}
		want = strings.Replace(want, namespaceCarriesOnEdits[i], namespaceCarriesOnEdits[i+1], 1)
	}
	raw := readFixtureManifest(t, namespaceCarriesOnDir)
	if raw != want {
		t.Fatalf("%s/%s is not the on-cluster fixture with on: added, so it may carry a second defect; want:\n%s",
			namespaceCarriesOnDir, manifestFile, want)
	}

	stdout, _ := runBlockverify(t, "--json", "--standalone", filepath.Join("testdata", namespaceCarriesOnDir))
	m := suiteByName(t, decodeReport(t, stdout), "manifest")
	if m.Status != "fail" {
		t.Fatalf("manifest status = %q, want %q; detail: %s", m.Status, "fail", m.Detail)
	}
	for _, part := range []string{manifestFile, "spec.resources[0]", `"on"`} {
		if !strings.Contains(m.Detail, part) {
			t.Errorf("manifest detail = %q, want it to name %q", m.Detail, part)
		}
	}
	_, perr := contract.ParseManifest([]byte(raw))
	if perr == nil {
		t.Fatalf("contract.ParseManifest accepts %s/%s, so the suite's failure is not the parser's reason", namespaceCarriesOnDir, manifestFile)
	}
	if !strings.Contains(m.Detail, perr.Error()) {
		t.Errorf("manifest detail = %q, want it to carry the parser's reason %q", m.Detail, perr.Error())
	}
}
