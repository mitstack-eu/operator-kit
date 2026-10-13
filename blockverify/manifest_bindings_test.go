package main

// manifest_bindings_test.go pins that blockverify fails its manifest suite on
// a mitstack/v1beta manifest whose requirements or resources the kit's
// parser refuses, printing the parser's reason. Each fixture this file names
// holds one manifest.yaml: ADR 0085's worked aws-app-infra manifest with one
// edit, so the edit is the only defect.
//
//   - manifest-requirement-carries-kind gives the repository requirement the
//     kind ADR 0085 retires;
//   - manifest-resource-carries-inputs gives the security group the inputs
//     ADR 0085 retires;
//   - manifest-alternatives-without-a-name lists two types for the egress
//     proxy without the name such an entry must carry;
//   - manifest-shared-requirement-reference requires github.repository
//     twice, so two requirements share one reference.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// bindingFixtures are the testdata directories, the edits that turn the
// aws-app-infra golden into each fixture's manifest, and the parts the
// manifest suite's detail must name.
var bindingFixtures = []struct {
	dir   string
	edits []string
	wants []string
}{
	{
		dir: "manifest-requirement-carries-kind",
		edits: []string{
			"  requires: [github.repository]\n",
			"  requires:\n    - { type: github.repository, kind: git-repository }\n",
		},
		wants: []string{"spec.requires[0]", `"kind"`},
	},
	{
		dir: "manifest-resource-carries-inputs",
		edits: []string{
			"    - aws.security-group\n",
			"    - { type: aws.security-group, inputs: { network: resources.aws.vpc } }\n",
		},
		wants: []string{"spec.resources[2]", `"inputs"`},
	},
	{
		dir: "manifest-alternatives-without-a-name",
		edits: []string{
			"    - aws.egress-proxy\n",
			"    - { type: [aws.egress-proxy, hetzner.firewall] }\n",
		},
		wants: []string{"spec.resources[8]", "name"},
	},
	{
		dir: "manifest-shared-requirement-reference",
		edits: []string{
			"  requires: [github.repository]\n",
			"  requires: [github.repository, github.repository]\n",
		},
		wants: []string{"spec.requires[1]", `"github.repository"`},
	},
}

// awsAppInfraWithEdits returns the aws-app-infra manifest golden with each
// old, new pair of edits applied once, failing the test when an old text is
// not in it.
func awsAppInfraWithEdits(t *testing.T, edits []string) string {
	t.Helper()
	raw, err := contract.ManifestGoldens.ReadFile(contract.ManifestGoldensDir + "/aws-app-infra-v1beta.yaml")
	if err != nil {
		t.Fatalf("reading aws-app-infra-v1beta.yaml from contract.ManifestGoldens: %v", err)
	}
	out := string(raw)
	for i := 0; i+1 < len(edits); i += 2 {
		if !strings.Contains(out, edits[i]) {
			t.Fatalf("fixture edit: %q is not in aws-app-infra-v1beta.yaml", edits[i])
		}
		out = strings.Replace(out, edits[i], edits[i+1], 1)
	}
	return out
}

func TestRun_FailsTheManifestSuiteOnAnEntryTheParserRefuses(t *testing.T) {
	for _, fx := range bindingFixtures {
		t.Run(fx.dir, func(t *testing.T) {
			dir := filepath.Join("testdata", fx.dir)
			raw, err := os.ReadFile(filepath.Join(dir, manifestFile))
			if err != nil {
				t.Fatalf("reading the fixture: %v", err)
			}
			if want := awsAppInfraWithEdits(t, fx.edits); string(raw) != want {
				t.Fatalf("%s/%s is not the aws-app-infra golden with its one edit, so it may carry a second defect; want:\n%s",
					dir, manifestFile, want)
			}

			stdout, _ := runBlockverify(t, "--json", "--standalone", dir)
			m := suiteByName(t, decodeReport(t, stdout), "manifest")
			if m.Status != "fail" {
				t.Fatalf("manifest status = %q, want %q; detail: %s", m.Status, "fail", m.Detail)
			}
			for _, want := range append([]string{manifestFile}, fx.wants...) {
				if !strings.Contains(m.Detail, want) {
					t.Errorf("manifest detail = %q, want it to name %q", m.Detail, want)
				}
			}
			_, perr := contract.ParseManifest(raw)
			if perr == nil {
				t.Fatalf("contract.ParseManifest accepts %s/%s, so the suite's failure is not the parser's reason", dir, manifestFile)
			}
			if !strings.Contains(m.Detail, perr.Error()) {
				t.Errorf("manifest detail = %q, want it to carry the parser's reason %q", m.Detail, perr.Error())
			}
		})
	}
}
