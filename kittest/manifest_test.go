package kittest_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

const v1betaManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: sample-block
  name: Sample Block
spec:
  version: 1.2.0
  description: >
    Creates the sample's network
    in the consumer's account.
  params:
    - name: region
      label: Region
  resources:
    - name: net
      type: aws.vpc
  lifecycle:
    endpoint: https://example.test/hook
    operations: [subscribe, unsubscribe]
    contract: kit/v1
`

const blankNoteManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: blank-note-block
  name: Blank Note Block
spec:
  version: 1.2.0
  releaseNote: "   "
  resources:
    - name: net
      type: aws.vpc
  lifecycle:
    endpoint: https://example.test/hook
    operations: [subscribe]
    contract: kit/v1
`

const v1alphaManifest = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: alpha-block
  name: Alpha Block
spec:
  version: 0.3.0
  receivesAppSecrets: true
  lifecycle:
    endpoint: https://example.test/hook
    operations: [subscribe]
    verbBudgets:
      subscribe: 30s
  naming:
    appName:
      maxLength: 40
`

func writeManifest(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("writing manifest fixture: %v", err)
	}
	return path
}

type manifestRead struct {
	value  any
	ok     bool
	failed bool
	logs   string
}

func readThrough(path string, read func(m kittest.Manifest) (any, bool)) manifestRead {
	var got manifestRead
	tb := runAndCollect(func(tb testing.TB) {
		m := kittest.ReadManifest(tb, path)
		got.value, got.ok = read(m)
	})
	got.failed = tb.failed
	got.logs = strings.Join(tb.logs, "\n")
	return got
}

func section(path string) func(kittest.Manifest) (any, bool) {
	return func(m kittest.Manifest) (any, bool) { return m.Section(path) }
}

func specScalar(key string) func(kittest.Manifest) (any, bool) {
	return func(m kittest.Manifest) (any, bool) { return m.SpecScalar(key) }
}

func TestManifest_ReadsSectionsAndScalarsByKeyPath(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		read     func(kittest.Manifest) (any, bool)
		want     any
		wantOK   bool
		// wantFail lists text the tb failure must carry; empty means the read must not fail.
		wantFail []string
	}{
		{"present section spec.lifecycle under v1beta", v1betaManifest, section("spec.lifecycle"),
			contract.ManifestLifecycle{Endpoint: "https://example.test/hook", Operations: []string{"subscribe", "unsubscribe"}, Contract: "kit/v1"}, true, nil},
		{"present scalar metadata.id", v1betaManifest, section("metadata.id"), "sample-block", true, nil},
		{"root type under v1beta", v1betaManifest, section("type"), "block", true, nil},
		{"missing pointer section spec.observability", v1betaManifest, section("spec.observability"), nil, false, nil},
		{"missing slice section spec.provides", v1betaManifest, section("spec.provides"), nil, false, nil},
		{"missing scalar metadata.owner", v1betaManifest, section("metadata.owner"), nil, false, nil},
		{"unknown key path fails naming the path", v1betaManifest, section("spec.nosuch"), nil, false, []string{"spec.nosuch"}},
		{"segment below a scalar fails naming the path", v1betaManifest, section("metadata.id.more"), nil, false, []string{"metadata.id.more"}},
		{"kind is not a v1beta root key", v1betaManifest, section("kind"), nil, false, []string{"kind"}},
		{"spec scalar version", v1betaManifest, specScalar("version"), "1.2.0", true, nil},
		{"spec scalar folded block description reads as one trimmed line", v1betaManifest, specScalar("description"), "Creates the sample's network in the consumer's account.", true, nil},
		{"spec scalar blank after trimming reads absent", blankNoteManifest, specScalar("releaseNote"), "", false, nil},
		{"spec scalar releaseNote absent", v1betaManifest, specScalar("releaseNote"), "", false, nil},
		{"spec scalar on a section fails", v1betaManifest, specScalar("lifecycle"), nil, false, []string{"spec.lifecycle", "not a scalar"}},
		{"spec scalar on an unknown key fails naming the path", v1betaManifest, specScalar("nosuch"), nil, false, []string{"spec.nosuch"}},
		{"v1alpha root kind by its yaml path", v1alphaManifest, section("kind"), "Block", true, nil},
		{"v1alpha map key under a pointer section", v1alphaManifest, section("spec.lifecycle.verbBudgets.subscribe"), "30s", true, nil},
		{"v1alpha map key the manifest does not set", v1alphaManifest, section("spec.lifecycle.verbBudgets.upgrade"), nil, false, nil},
		{"v1alpha integer through two pointers", v1alphaManifest, section("spec.naming.appName.maxLength"), 40, true, nil},
		{"v1alpha boolean spec scalar", v1alphaManifest, specScalar("receivesAppSecrets"), "true", true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := readThrough(writeManifest(t, tc.manifest), tc.read)

			if len(tc.wantFail) > 0 {
				if !got.failed {
					t.Fatalf("read returned (%#v, %v) without failing tb, want a failure naming %q", got.value, got.ok, tc.wantFail)
				}
				for _, s := range tc.wantFail {
					if !strings.Contains(got.logs, s) {
						t.Errorf("failure %q does not name %q", got.logs, s)
					}
				}
				return
			}
			if got.failed {
				t.Fatalf("read failed tb: %s", got.logs)
			}
			if got.ok != tc.wantOK {
				t.Errorf("ok = %v, want %v (value %#v)", got.ok, tc.wantOK, got.value)
			}
			if !reflect.DeepEqual(got.value, tc.want) {
				t.Errorf("value = %#v (%T), want %#v (%T)", got.value, got.value, tc.want, tc.want)
			}
		})
	}
}

func TestManifest_SectionReturnsTypedResources(t *testing.T) {
	got := readThrough(writeManifest(t, v1betaManifest), section("spec.resources"))
	if got.failed {
		t.Fatalf("read failed tb: %s", got.logs)
	}
	resources, isTyped := got.value.([]contract.ManifestResource)
	if !got.ok || !isTyped {
		t.Fatalf("Section(spec.resources) = (%#v, %v), want a set []contract.ManifestResource", got.value, got.ok)
	}
	if len(resources) != 1 || resources[0].Name != "net" || resources[0].Type != "aws.vpc" {
		t.Errorf("resources = %+v, want one resource net of type aws.vpc", resources)
	}
}

func TestManifest_EmbedsTheParsedContractManifest(t *testing.T) {
	path := writeManifest(t, v1betaManifest)
	var m kittest.Manifest
	tb := runAndCollect(func(tb testing.TB) { m = kittest.ReadManifest(tb, path) })
	if tb.failed {
		t.Fatalf("ReadManifest failed tb: %v", tb.logs)
	}
	if m.Block == nil || m.ID() != "sample-block" || m.Version() != "1.2.0" {
		t.Errorf("embedded contract.Manifest = %+v, want Block set with id sample-block and version 1.2.0", m.Manifest)
	}
}

func TestReadManifest_MalformedManifestFailsNamingPathAndParserError(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "unknown root key", raw: "apiVersion: mitstack/v1beta\ntype: block\nbogus: 1\n"},
		{name: "no apiVersion", raw: "type: block\nmetadata:\n  id: x\n"},
		{name: "not a mapping", raw: "- just\n- a list\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, parseErr := contract.ParseManifest([]byte(tc.raw))
			if parseErr == nil {
				t.Fatalf("fixture %q parses; a malformed-manifest case needs one contract.ParseManifest refuses", tc.raw)
			}
			path := writeManifest(t, tc.raw)
			tb := runAndCollect(func(tb testing.TB) { kittest.ReadManifest(tb, path) })
			if !tb.failed {
				t.Fatal("ReadManifest did not fail tb on a manifest contract.ParseManifest refuses")
			}
			logs := strings.Join(tb.logs, "\n")
			if !strings.Contains(logs, path) {
				t.Errorf("failure %q does not name the manifest path %q", logs, path)
			}
			if !strings.Contains(logs, parseErr.Error()) {
				t.Errorf("failure %q does not carry the parser's error %q", logs, parseErr.Error())
			}
		})
	}
}

func TestReadManifest_UnreadableFileFailsNamingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "manifest.yaml")
	tb := runAndCollect(func(tb testing.TB) { kittest.ReadManifest(tb, path) })
	if !tb.failed {
		t.Fatal("ReadManifest did not fail tb on a file that does not exist")
	}
	if logs := strings.Join(tb.logs, "\n"); !strings.Contains(logs, path) {
		t.Errorf("failure %q does not name the path %q", logs, path)
	}
}
