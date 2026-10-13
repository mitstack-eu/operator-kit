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
		{
			name:     "present section spec.lifecycle under v1beta",
			manifest: v1betaManifest,
			read:     section("spec.lifecycle"),
			want: contract.ManifestLifecycle{
				Endpoint:   "https://example.test/hook",
				Operations: []string{"subscribe", "unsubscribe"},
				Contract:   "kit/v1",
			},
			wantOK: true,
		},
		{
			name:     "present scalar metadata.id",
			manifest: v1betaManifest,
			read:     section("metadata.id"),
			want:     "sample-block",
			wantOK:   true,
		},
		{
			name:     "root type under v1beta",
			manifest: v1betaManifest,
			read:     section("type"),
			want:     "block",
			wantOK:   true,
		},
		{
			name:     "missing pointer section spec.observability",
			manifest: v1betaManifest,
			read:     section("spec.observability"),
			want:     nil,
			wantOK:   false,
		},
		{
			name:     "missing slice section spec.provides",
			manifest: v1betaManifest,
			read:     section("spec.provides"),
			want:     nil,
			wantOK:   false,
		},
		{
			name:     "missing scalar metadata.owner",
			manifest: v1betaManifest,
			read:     section("metadata.owner"),
			want:     nil,
			wantOK:   false,
		},
		{
			name:     "unknown key path fails naming the path",
			manifest: v1betaManifest,
			read:     section("spec.nosuch"),
			wantFail: []string{"spec.nosuch"},
		},
		{
			name:     "segment below a scalar fails naming the path",
			manifest: v1betaManifest,
			read:     section("metadata.id.more"),
			wantFail: []string{"metadata.id.more"},
		},
		{
			name:     "kind is not a v1beta root key",
			manifest: v1betaManifest,
			read:     section("kind"),
			wantFail: []string{"kind"},
		},
		{
			name:     "spec scalar version",
			manifest: v1betaManifest,
			read:     specScalar("version"),
			want:     "1.2.0",
			wantOK:   true,
		},
		{
			name:     "spec scalar folded block description reads as one trimmed line",
			manifest: v1betaManifest,
			read:     specScalar("description"),
			want:     "Creates the sample's network in the consumer's account.",
			wantOK:   true,
		},
		{
			name:     "spec scalar blank after trimming reads absent",
			manifest: blankNoteManifest,
			read:     specScalar("releaseNote"),
			want:     "",
			wantOK:   false,
		},
		{
			name:     "spec scalar releaseNote absent",
			manifest: v1betaManifest,
			read:     specScalar("releaseNote"),
			want:     "",
			wantOK:   false,
		},
		{
			name:     "spec scalar on a section fails",
			manifest: v1betaManifest,
			read:     specScalar("lifecycle"),
			wantFail: []string{"spec.lifecycle", "not a scalar"},
		},
		{
			name:     "spec scalar on an unknown key fails naming the path",
			manifest: v1betaManifest,
			read:     specScalar("nosuch"),
			wantFail: []string{"spec.nosuch"},
		},
		{
			name:     "v1alpha root kind by its yaml path",
			manifest: v1alphaManifest,
			read:     section("kind"),
			want:     "Block",
			wantOK:   true,
		},
		{
			name:     "v1alpha map key under a pointer section",
			manifest: v1alphaManifest,
			read:     section("spec.lifecycle.verbBudgets.subscribe"),
			want:     "30s",
			wantOK:   true,
		},
		{
			name:     "v1alpha map key the manifest does not set",
			manifest: v1alphaManifest,
			read:     section("spec.lifecycle.verbBudgets.upgrade"),
			want:     nil,
			wantOK:   false,
		},
		{
			name:     "v1alpha integer through two pointers",
			manifest: v1alphaManifest,
			read:     section("spec.naming.appName.maxLength"),
			want:     40,
			wantOK:   true,
		},
		{
			name:     "v1alpha boolean spec scalar",
			manifest: v1alphaManifest,
			read:     specScalar("receivesAppSecrets"),
			want:     "true",
			wantOK:   true,
		},
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
