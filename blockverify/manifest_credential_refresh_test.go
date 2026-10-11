package main

// manifest_credential_refresh_test.go pins that blockverify passes a mitstack/v1beta block declaring spec.lifecycle.needsCredentialRefresh.

import (
	"path/filepath"
	"reflect"
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

// The golden handler predates kit/v1, so a v1beta manifest fails suites the field never reaches; comparing with the same manifest unset isolates the field.
func TestRun_NeedsCredentialRefreshChangesNoSuiteStatus(t *testing.T) {
	const line = "    needsCredentialRefresh: true\n"
	set := requireNeedsCredentialRefreshFixture(t)
	statuses := func(manifest string) (map[string]string, report) {
		files := goldenFixtureFiles(t)
		files["manifest.yaml"] = manifest
		stdout, _ := runBlockverify(t, "--json", "--standalone", newFixtureFrom(t, files))
		r := decodeReport(t, stdout)
		out := make(map[string]string, len(r.Suites))
		for _, s := range r.Suites {
			out[s.Name] = s.Status
		}
		return out, r
	}

	withField, r := statuses(set)
	without, _ := statuses(strings.Replace(set, line, "", 1))
	if !reflect.DeepEqual(withField, without) {
		t.Errorf("suite statuses with the field = %v, without it = %v; want the field to change none", withField, without)
	}
	if m := suiteByName(t, r, "manifest"); m.Status != "pass" {
		t.Errorf("manifest status = %q, want %q; detail: %s", m.Status, "pass", m.Detail)
	}
	for _, s := range r.Suites {
		if strings.Contains(s.Detail, "needsCredentialRefresh") {
			t.Errorf("%s detail names the field: %s", s.Name, s.Detail)
		}
	}
}
