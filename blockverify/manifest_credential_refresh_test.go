package main

// manifest_credential_refresh_test.go pins that blockverify passes a mitstack/v1beta block declaring spec.lifecycle.needsCredentialRefresh.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/contract"
)

const needsCredentialRefreshDir = "manifest-needs-credential-refresh"

func requireNeedsCredentialRefreshFixture(t *testing.T) string {
	t.Helper()
	raw := readFixtureManifest(t, needsCredentialRefreshDir)
	if !strings.HasPrefix(raw, "apiVersion: "+contract.ManifestAPIVersion+"\n") {
		t.Fatalf("the fixture is not a %s manifest", contract.ManifestAPIVersion)
	}
	if !strings.Contains(raw, "  lifecycle:\n") || !strings.Contains(raw, "    needsCredentialRefresh: true\n") {
		t.Fatalf("the fixture does not set spec.lifecycle.needsCredentialRefresh to true:\n%s", raw)
	}
	return raw
}

func TestRun_PassesTheManifestSuiteOnABlockThatNeedsCredentialRefresh(t *testing.T) {
	requireNeedsCredentialRefreshFixture(t)

	stdout, _ := runBlockverify(t, "--json", "--standalone", filepath.Join("testdata", needsCredentialRefreshDir))
	m := suiteByName(t, decodeReport(t, stdout), "manifest")
	if m.Status != "pass" {
		t.Errorf("manifest status = %q, want %q; detail: %s", m.Status, "pass", m.Detail)
	}
}

func TestRun_PassesBlockverifyOnAFixtureThatNeedsCredentialRefresh(t *testing.T) {
	files := goldenFixtureFiles(t)
	files["manifest.yaml"] = requireNeedsCredentialRefreshFixture(t)

	stdout, exit := runBlockverify(t, "--json", "--standalone", newFixtureFrom(t, files))
	r := decodeReport(t, stdout)
	if exit != 0 || r.Verdict != "pass" {
		t.Errorf("exit %d, verdict %q; want 0 and pass\nstdout:\n%s", exit, r.Verdict, stdout)
	}
	for _, name := range []string{"trust-plumbing", "contract", "manifest", "naming"} {
		if s := suiteByName(t, r, name); s.Status != "pass" {
			t.Errorf("%s status = %q, want %q; detail: %s", name, s.Status, "pass", s.Detail)
		}
	}
}
