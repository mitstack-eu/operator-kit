package contract_test

// blockmanifest_test.go pins contract.ParseManifest, the one manifest parser
// the kit ships. It switches on apiVersion: under mitstack/v1beta it reads
// the whole manifest strictly into BlockManifest, in the shape ADR 0085
// sets, and under mitstack/v1alpha it reads the whole manifest strictly
// into V1AlphaManifest. The worked manifests of ADR 0085 and the data-model
// page are read from the embedded manifest goldens and checked field by
// field.

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/contract"
)

func readManifestGolden(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := contract.ManifestGoldens.ReadFile(contract.ManifestGoldensDir + "/" + name)
	if err != nil {
		t.Fatalf("reading %s from contract.ManifestGoldens: %v", name, err)
	}
	return raw
}

// wantRequirement is what one parsed spec.requires entry must hold.
type wantRequirement struct {
	name      string
	types     []string
	many      bool
	reference string
}

// wantResource is what one parsed spec.resources entry must hold. typ is
// the one listed type id, empty for an entry listing several.
type wantResource struct {
	name  string
	typ   string
	types []string
}

type wantWorkedManifest struct {
	file         string
	id           string
	version      string
	releaseClass string
	releaseNote  string
	// rest is the manifest with Spec.Requires and Spec.Resources left out
	// and Spec.Description written without its trailing line break; those
	// are compared on their own.
	rest      contract.BlockManifest
	requires  []wantRequirement
	resources []wantResource
}

func oneType(id string) []string { return []string{id} }

var workedManifests = []wantWorkedManifest{
	{
		file:         "plain-v1beta.yaml",
		id:           "plain",
		version:      "3.0.0",
		releaseClass: "breaking",
		releaseNote:  "Plain runs its container on the app's EC2 host, against the Postgres database offered there.",
		rest: contract.BlockManifest{
			APIVersion: "mitstack/v1beta",
			Type:       "block",
			Metadata:   contract.ManifestMetadata{ID: "plain", Name: "Plain Tracker", Owner: "web-app-platform"},
			Spec: contract.ManifestSpec{
				Version:      "3.0.0",
				ReleaseClass: "breaking",
				ReleaseNote:  "Plain runs its container on the app's EC2 host, against the Postgres database offered there.",
				Description:  "Runs the Plain tracker container on the app's host, connected to the app's Postgres database.",
				Params: []contract.ManifestParam{
					{Name: "image", Label: "Plain tracker container image", Required: true},
				},
				Provides: []string{"aws.ec2-container"},
				Lifecycle: contract.ManifestLifecycle{
					Endpoint:   "https://api.mitstack.dev/operators/plain/hook",
					Operations: []string{"subscribe", "unsubscribe", "upgrade", "validate"},
					Contract:   "kit/v1",
				},
			},
		},
		requires: []wantRequirement{
			{types: oneType("aws.ec2-instance"), reference: "aws.ec2-instance"},
			{types: oneType("aws.ec2-postgres-database"), reference: "aws.ec2-postgres-database"},
		},
		resources: []wantResource{
			{name: "aws.ec2-container", typ: "aws.ec2-container", types: oneType("aws.ec2-container")},
		},
	},
	{
		file:         "aws-app-infra-v1beta.yaml",
		id:           "aws-app-infra",
		version:      "2.0.0",
		releaseClass: "breaking",
		releaseNote:  "AWS App Infrastructure offers its host, data volume, registry, deploy role and address as separate resources.",
		rest: contract.BlockManifest{
			APIVersion: "mitstack/v1beta",
			Type:       "block",
			Metadata:   contract.ManifestMetadata{ID: "aws-app-infra", Name: "AWS App Infrastructure"},
			Spec: contract.ManifestSpec{
				Version:      "2.0.0",
				ReleaseClass: "breaking",
				ReleaseNote:  "AWS App Infrastructure offers its host, data volume, registry, deploy role and address as separate resources.",
				Description:  "Creates the app's host, data volume and image registry in the consumer's own AWS account, and the app's public address.",
				Params: []contract.ManifestParam{
					{Name: "instanceType", Label: "EC2 instance type"},
					{Name: "domain", Label: "Your own domain pointed at the app"},
					{Name: "customDomain", Label: "Extra hostname served alongside the app's address"},
				},
				Provides: []string{"aws.ec2-instance", "aws.ebs-volume", "aws.ecr-repository", "deploy-role", "aws.route53-record"},
				Lifecycle: contract.ManifestLifecycle{
					Endpoint:   "https://api.mitstack.dev/operators/aws-app-infra/hook",
					Operations: []string{"subscribe", "unsubscribe", "upgrade", "validate"},
					Contract:   "kit/v1",
				},
			},
		},
		requires: []wantRequirement{
			{types: oneType("github.repository"), reference: "github.repository"},
		},
		resources: []wantResource{
			{name: "aws.s3-bucket", typ: "aws.s3-bucket", types: oneType("aws.s3-bucket")},
			{name: "aws.vpc", typ: "aws.vpc", types: oneType("aws.vpc")},
			{name: "aws.security-group", typ: "aws.security-group", types: oneType("aws.security-group")},
			{name: "aws.ec2-instance", typ: "aws.ec2-instance", types: oneType("aws.ec2-instance")},
			{name: "aws.ebs-volume", typ: "aws.ebs-volume", types: oneType("aws.ebs-volume")},
			{name: "aws.ecr-repository", typ: "aws.ecr-repository", types: oneType("aws.ecr-repository")},
			{name: "ecr-push-role", typ: "aws.iam-role", types: oneType("aws.iam-role")},
			{name: "deploy-role", typ: "aws.iam-role", types: oneType("aws.iam-role")},
			{name: "aws.egress-proxy", typ: "aws.egress-proxy", types: oneType("aws.egress-proxy")},
			{name: "aws.ssm-association", typ: "aws.ssm-association", types: oneType("aws.ssm-association")},
			{name: "aws.route53-record", typ: "aws.route53-record", types: oneType("aws.route53-record")},
		},
	},
}

func requireRequirements(t *testing.T, got []contract.ManifestRequirement, want []wantRequirement) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("spec.requires has %d entries %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i, w := range want {
		g := got[i]
		if g.Name != w.name || !slices.Equal(g.Types, w.types) || g.Many != w.many {
			t.Errorf("spec.requires[%d] name, types, many = %q, %q, %v; want %q, %q, %v", i, g.Name, g.Types, g.Many, w.name, w.types, w.many)
		}
		if ref := g.Reference(); ref != w.reference {
			t.Errorf("spec.requires[%d].Reference() = %q, want %q", i, ref, w.reference)
		}
		if g.Kind != "" || g.In != nil {
			t.Errorf("spec.requires[%d] kind, in = %q, %q; want both empty, since ADR 0085 retires them", i, g.Kind, g.In)
		}
	}
}

func requireResources(t *testing.T, got []contract.ManifestResource, want []wantResource) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("spec.resources has %d entries %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i, w := range want {
		g := got[i]
		if g.Name != w.name || g.Type != w.typ || !slices.Equal(g.Types, w.types) {
			t.Errorf("spec.resources[%d] name, type, types = %q, %q, %q; want %q, %q, %q", i, g.Name, g.Type, g.Types, w.name, w.typ, w.types)
		}
		if g.Inputs != nil || g.On != "" {
			t.Errorf("spec.resources[%d] inputs, on = %+v, %q; want both empty, since ADR 0085 retires them", i, g.Inputs, g.On)
		}
	}
}

func TestManifestAPIVersions_AreV1AlphaAndV1Beta(t *testing.T) {
	if contract.ManifestAPIVersionV1Alpha != "mitstack/v1alpha" {
		t.Errorf("contract.ManifestAPIVersionV1Alpha = %q, want %q", contract.ManifestAPIVersionV1Alpha, "mitstack/v1alpha")
	}
	if contract.ManifestAPIVersion != "mitstack/v1beta" {
		t.Errorf("contract.ManifestAPIVersion = %q, want %q", contract.ManifestAPIVersion, "mitstack/v1beta")
	}
}

func TestParseManifest_ReadsTheWorkedV1BetaManifests(t *testing.T) {
	for _, w := range workedManifests {
		t.Run(w.file, func(t *testing.T) {
			m, err := contract.ParseManifest(readManifestGolden(t, w.file))
			if err != nil {
				t.Fatalf("ParseManifest(%s): %v", w.file, err)
			}
			if m.APIVersion != contract.ManifestAPIVersion {
				t.Errorf("APIVersion = %q, want %q", m.APIVersion, contract.ManifestAPIVersion)
			}
			if m.V1Alpha != nil {
				t.Errorf("V1Alpha = %+v, want nil under %s", *m.V1Alpha, contract.ManifestAPIVersion)
			}
			if m.Block == nil {
				t.Fatalf("Block is nil under %s", contract.ManifestAPIVersion)
			}
			if m.ID() != w.id || m.Version() != w.version || m.ReleaseClass() != w.releaseClass || m.ReleaseNote() != w.releaseNote {
				t.Errorf("ID, Version, ReleaseClass, ReleaseNote = %q, %q, %q, %q; want %q, %q, %q, %q",
					m.ID(), m.Version(), m.ReleaseClass(), m.ReleaseNote(), w.id, w.version, w.releaseClass, w.releaseNote)
			}

			got := *m.Block
			gotRequires, gotResources := got.Spec.Requires, got.Spec.Resources
			got.Spec.Requires, got.Spec.Resources = nil, nil
			got.Spec.Description = strings.TrimRight(got.Spec.Description, "\n")
			if !reflect.DeepEqual(got, w.rest) {
				t.Errorf("manifest outside requires and resources =\n%+v\nwant\n%+v", got, w.rest)
			}
			requireRequirements(t, gotRequires, w.requires)
			requireResources(t, gotResources, w.resources)
		})
	}
}

// typedManifest lists a requirement and a resource in every form ADR 0085
// gives an entry: a type id alone, a list of types with a name, one type
// with a name, and a block list of types.
const typedManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: typed-block
  name: Typed Block
spec:
  version: 1.0.0
  requires:
    - github.repository
    - { name: host, type: [aws.ec2-instance, hetzner.server] }
    - { name: primary, type: aws.iam-role }
    - name: secondary
      type: aws.iam-role
      many: true
  resources:
    - aws.vpc
    - { name: app-container, type: [aws.ec2-container, hetzner.server-container] }
    - name: state
      type: aws.s3-bucket
    - name: worker
      type:
        - aws.ec2-container
        - hetzner.server-container
  provides: [app-container, aws.vpc]
  lifecycle:
    endpoint: https://typed-block.example/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`

func TestParseManifest_V1BetaReadsRequirementsAndResourcesByExactType(t *testing.T) {
	b := parseBeta(t, []byte(typedManifest))
	requireRequirements(t, b.Spec.Requires, []wantRequirement{
		{types: oneType("github.repository"), reference: "github.repository"},
		{name: "host", types: []string{"aws.ec2-instance", "hetzner.server"}, reference: "host"},
		{name: "primary", types: oneType("aws.iam-role"), reference: "primary"},
		{name: "secondary", types: oneType("aws.iam-role"), many: true, reference: "secondary"},
	})
	requireResources(t, b.Spec.Resources, []wantResource{
		{name: "aws.vpc", typ: "aws.vpc", types: oneType("aws.vpc")},
		{name: "app-container", types: []string{"aws.ec2-container", "hetzner.server-container"}},
		{name: "state", typ: "aws.s3-bucket", types: oneType("aws.s3-bucket")},
		{name: "worker", types: []string{"aws.ec2-container", "hetzner.server-container"}},
	})
	if want := []string{"app-container", "aws.vpc"}; !slices.Equal(b.Spec.Provides, want) {
		t.Errorf("spec.provides = %q, want %q", b.Spec.Provides, want)
	}
}

func TestParseManifest_V1BetaReadsTheShortRequiresListOfTypeIDs(t *testing.T) {
	b := parseBeta(t, []byte(strings.Replace(typedManifest,
		"  requires:\n    - github.repository\n    - { name: host, type: [aws.ec2-instance, hetzner.server] }\n"+
			"    - { name: primary, type: aws.iam-role }\n    - name: secondary\n      type: aws.iam-role\n      many: true\n",
		"  requires: [github.repository]\n", 1)))
	requireRequirements(t, b.Spec.Requires, []wantRequirement{
		{types: oneType("github.repository"), reference: "github.repository"},
	})
}

// The parser judges the shape and the id grammar only: whether the
// platform's catalogue holds a type is CheckManifestTypes' question.
func TestParseManifest_V1BetaNeedsNoSnapshotToReadATypeTheCatalogueLacks(t *testing.T) {
	raw := strings.Replace(typedManifest, "    - aws.vpc\n", "    - aws.not-in-any-catalogue\n", 1)
	raw = strings.Replace(raw, "  provides: [app-container, aws.vpc]\n", "  provides: [app-container, aws.not-in-any-catalogue]\n", 1)
	b := parseBeta(t, []byte(raw))
	if got := b.Spec.Resources[0].Name; got != "aws.not-in-any-catalogue" {
		t.Errorf("spec.resources[0] name = %q, want the type id it lists", got)
	}
}

// retiredKeyManifest requires a named host at spec.requires[1] and declares
// a named container at spec.resources[1], so a retired key added to either
// is refused naming that index.
const retiredKeyManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: retired-keys
  name: Retired Keys
spec:
  version: 1.0.0
  params:
    - name: image
      label: Container image
  requires:
    - github.repository
    - name: host
      type: aws.ec2-instance
  resources:
    - aws.vpc
    - name: app-container
      type: aws.ec2-container
  provides: [app-container]
  lifecycle:
    endpoint: https://retired-keys.example/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`

func editRetired(t *testing.T, old, new string) []byte {
	t.Helper()
	if !strings.Contains(retiredKeyManifest, old) {
		t.Fatalf("fixture edit: %q is not in the manifest", old)
	}
	return []byte(strings.Replace(retiredKeyManifest, old, new, 1))
}

func TestParseManifest_V1BetaRefusesARetiredKeyNamingTheFieldAndTheEntry(t *testing.T) {
	parseBeta(t, []byte(retiredKeyManifest))
	const hostType = "      type: aws.ec2-instance\n"
	const containerType = "      type: aws.ec2-container\n"
	cases := []struct {
		label, old, new, field, entry string
	}{
		{"kind on a requirement", hostType, hostType + "      kind: host\n", "kind", "spec.requires[1]"},
		{"in on a requirement", hostType, hostType + "      in: aws\n", "in", "spec.requires[1]"},
		{"in as a list on a requirement", hostType, hostType + "      in: [aws, hetzner]\n", "in", "spec.requires[1]"},
		{"on on a requirement", hostType, hostType + "      on: requires.github.repository\n", "on", "spec.requires[1]"},
		{"inputs on a requirement", hostType, hostType + "      inputs: { region: params.image }\n", "inputs", "spec.requires[1]"},
		{"a requirement by kind alone", "    - github.repository\n", "    - kind: git-repository\n", "kind", "spec.requires[0]"},
		{"inputs on a resource", containerType, containerType + "      inputs: { image: params.image }\n", "inputs", "spec.resources[1]"},
		{"nested inputs on a resource", containerType, containerType + "      inputs:\n        env:\n          DB_HOST: requires.host.outputs.hostId\n", "inputs", "spec.resources[1]"},
		{"on on a resource", containerType, containerType + "      on: requires.host\n", "on", "spec.resources[1]"},
		{"kind on a resource", containerType, containerType + "      kind: container\n", "kind", "spec.resources[1]"},
		{"in on a resource", containerType, containerType + "      in: aws\n", "in", "spec.resources[1]"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			requireRefusedNaming(t, editRetired(t, tc.old, tc.new), `"`+tc.field+`"`, tc.entry)
		})
	}
}

func TestParseManifest_V1BetaRefusesAnEntryItCannotTellApartOrName(t *testing.T) {
	cases := []struct {
		label, old, new string
		wants           []string
	}{
		{"a requirement listing types without a name",
			"    - name: host\n      type: aws.ec2-instance\n",
			"    - type: [aws.ec2-instance, hetzner.server]\n",
			[]string{"spec.requires[1]", "name"}},
		{"a resource listing types without a name",
			"    - name: app-container\n      type: aws.ec2-container\n",
			"    - type: [aws.ec2-container, hetzner.server-container]\n",
			[]string{"spec.resources[1]", "name"}},
		{"a requirement list naming one type twice",
			"      type: aws.ec2-instance\n",
			"      type: [aws.ec2-instance, aws.ec2-instance]\n",
			[]string{"spec.requires[1]", "aws.ec2-instance"}},
		{"a resource list naming one type twice",
			"      type: aws.ec2-container\n",
			"      type: [aws.ec2-container, aws.ec2-container]\n",
			[]string{"spec.resources[1]", "aws.ec2-container"}},
		{"two unnamed resources of one type",
			"    - aws.vpc\n",
			"    - aws.vpc\n    - aws.vpc\n",
			[]string{"spec.resources[1]", "aws.vpc"}},
		{"two unnamed requirements of one type",
			"    - github.repository\n",
			"    - github.repository\n    - github.repository\n",
			[]string{"spec.requires[1]", "github.repository"}},
		{"two resources of one name",
			"    - aws.vpc\n",
			"    - { name: app-container, type: aws.vpc }\n",
			[]string{"app-container"}},
		{"a requirement mapping with no type",
			"      type: aws.ec2-instance\n",
			"",
			[]string{"spec.requires[1].type"}},
		{"a resource mapping with no type",
			"      type: aws.ec2-container\n",
			"",
			[]string{"spec.resources[1].type"}},
		{"an empty list of types",
			"      type: aws.ec2-container\n",
			"      type: []\n",
			[]string{"spec.resources[1].type"}},
		{"a provides entry naming no resource",
			"  provides: [app-container]\n",
			"  provides: [app-container, no-such-resource]\n",
			[]string{"spec.provides", "no-such-resource"}},
		{"a provides entry naming the type of a named resource",
			"  provides: [app-container]\n",
			"  provides: [aws.ec2-container]\n",
			[]string{"spec.provides", "aws.ec2-container"}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			requireRefusedNaming(t, editRetired(t, tc.old, tc.new), tc.wants...)
		})
	}
}

func TestParseManifest_V1BetaRefusesATypeIDOutsideTheGrammarNamingIt(t *testing.T) {
	cases := []struct {
		label, old, new, id, entry string
	}{
		{"a resource type id", "    - aws.vpc\n", "    - AWS.VPC\n", "AWS.VPC", "spec.resources[0]"},
		{"a resource type id with no capability", "    - aws.vpc\n", "    - vpc\n", "vpc", "spec.resources[0]"},
		{"a requirement type id", "    - github.repository\n", "    - github_repository\n", "github_repository", "spec.requires[0]"},
		{"a type id inside a list", "      type: aws.ec2-container\n", "      type: [aws.ec2-container, hetzner.Server_Container]\n",
			"hetzner.Server_Container", "spec.resources[1]"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			requireRefusedNaming(t, editRetired(t, tc.old, tc.new), tc.id, tc.entry)
		})
	}
}

// betaManifest is a small valid v1beta manifest the behaviour tests edit.
const betaManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: fixture-block
  name: Fixture Block
spec:
  version: 1.0.0
  params:
    - name: image
      label: Container image
  requires:
    - type: aws.ec2-instance
  resources:
    - name: fallback-network
      type: aws.vpc
    - name: app-security-group
      type: aws.security-group
  provides: [app-security-group]
  lifecycle:
    endpoint: https://example.test/hook
    operations: [subscribe, unsubscribe]
    contract: kit/v1
`

// editBeta returns betaManifest with each old, new pair of replacements
// applied once. It fails the test when an old text is not in the manifest,
// so a fixture edit can never silently become a no-op.
func editBeta(t *testing.T, pairs ...string) []byte {
	t.Helper()
	out := betaManifest
	for i := 0; i+1 < len(pairs); i += 2 {
		if !strings.Contains(out, pairs[i]) {
			t.Fatalf("fixture edit: %q is not in the manifest", pairs[i])
		}
		out = strings.Replace(out, pairs[i], pairs[i+1], 1)
	}
	return []byte(out)
}

// parseBeta parses raw and returns its Block, failing the test when raw is
// refused or is not read as a v1beta manifest.
func parseBeta(t *testing.T, raw []byte) *contract.BlockManifest {
	t.Helper()
	m, err := contract.ParseManifest(raw)
	if err != nil {
		t.Fatalf("ParseManifest: %v\n%s", err, raw)
	}
	if m.Block == nil {
		t.Fatalf("ParseManifest gave no Block for a %s manifest: %+v", contract.ManifestAPIVersion, m)
	}
	return m.Block
}

// requireRefusedNaming fails the test unless ParseManifest refuses raw with
// an error containing every one of wants.
func requireRefusedNaming(t *testing.T, raw []byte, wants ...string) {
	t.Helper()
	m, err := contract.ParseManifest(raw)
	if err == nil {
		t.Fatalf("ParseManifest accepted the manifest (%+v), want an error naming %q:\n%s", m, wants, raw)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ParseManifest error = %q, want it to name %q", err, want)
		}
	}
}

func TestParseManifest_V1BetaAcceptsTheBaseFixture(t *testing.T) {
	if b := parseBeta(t, []byte(betaManifest)); len(b.Spec.Resources) != 2 {
		t.Fatalf("betaManifest gave resources %+v, want two", b.Spec.Resources)
	}
}

// The keys ADR 0083 takes out of the manifest stay out because v1beta is
// strict: a key the types do not name is refused, naming its path.
func TestParseManifest_V1BetaRefusesAnUnknownKeyNamingItsPath(t *testing.T) {
	cases := []struct {
		path  string
		edits []string
	}{
		{"metadata.labels", []string{"  name: Fixture Block\n", "  name: Fixture Block\n  labels: none\n"}},
		{"spec.outputs", []string{"  requires:\n", "  outputs: [host]\n  requires:\n"}},
		{"spec.providedContracts", []string{"  provides: [app-security-group]\n",
			"  provides: [app-security-group]\n  providedContracts:\n    - capability: app-host\n      format: config\n"}},
		{"spec.dependsOn", []string{"  requires:\n", "  dependsOn:\n    - weather-app\n  requires:\n"}},
		{"spec.params[0].type", []string{"      label: Container image\n", "      label: Container image\n      type: string\n"}},
		{"spec.requires[0].capability", []string{"    - type: aws.ec2-instance\n", "    - type: aws.ec2-instance\n      capability: app-host\n"}},
		{"spec.requires[0].reads", []string{"    - type: aws.ec2-instance\n", "    - type: aws.ec2-instance\n      reads: [region]\n"}},
		{"spec.resources[1].ownership", []string{"      type: aws.security-group\n", "      type: aws.security-group\n      ownership: created\n"}},
		{"spec.lifecycle.upgradeStrategy", []string{"    contract: kit/v1\n", "    contract: kit/v1\n    upgradeStrategy: rolling\n"}},
		{"spec.observability.resources", []string{"  lifecycle:\n", "  observability:\n    resources:\n      - name: fallback-network\n  lifecycle:\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			requireRefusedNaming(t, editBeta(t, tc.edits...), tc.path)
		})
	}
}

func TestParseManifest_V1BetaRefusesAMissingRequiredFieldNamingItsPath(t *testing.T) {
	cases := []struct {
		label string
		wants []string
		edits []string
	}{
		{"type absent", []string{"type"}, []string{"type: block\n", ""}},
		{"type not block", []string{"type", "block"}, []string{"type: block\n", "type: component\n"}},
		{"metadata.id absent", []string{"metadata.id"}, []string{"  id: fixture-block\n", ""}},
		{"spec.version absent", []string{"spec.version"}, []string{"  version: 1.0.0\n", ""}},
		{"a requirement without a type", []string{"spec.requires[0].type"}, []string{"    - type: aws.ec2-instance\n", "    - name: primary\n"}},
		{"a resource without a type", []string{"spec.resources[1].type"}, []string{"      type: aws.security-group\n", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			requireRefusedNaming(t, editBeta(t, tc.edits...), tc.wants...)
		})
	}
}

// ADR 0085 names a resource only where its type cannot: an entry with no
// name is named by the type it lists.
func TestParseManifest_V1BetaNamesAResourceWithNoNameByItsType(t *testing.T) {
	b := parseBeta(t, editBeta(t,
		"    - name: fallback-network\n      type: aws.vpc\n", "    - type: aws.vpc\n"))
	if got := b.Spec.Resources[0].Name; got != "aws.vpc" {
		t.Errorf("spec.resources[0] name = %q, want the type it lists, %q", got, "aws.vpc")
	}
}

func TestParseManifest_V1BetaManyTakesTrueOrFalseOnly(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"", false},
		{"      many: true\n", true},
		{"      many: false\n", false},
	} {
		t.Run("accepts "+strings.TrimSpace(tc.line), func(t *testing.T) {
			b := parseBeta(t, editBeta(t, "    - type: aws.ec2-instance\n", "    - type: aws.ec2-instance\n"+tc.line))
			if got := b.Spec.Requires[0].Many; got != tc.want {
				t.Errorf("spec.requires[0].many = %v, want %v", got, tc.want)
			}
		})
	}
	for _, value := range []string{"yes", "1", `"true"`, "sometimes"} {
		t.Run("refuses "+value, func(t *testing.T) {
			requireRefusedNaming(t, editBeta(t, "    - type: aws.ec2-instance\n", "    - type: aws.ec2-instance\n      many: "+value+"\n"),
				"spec.requires[0].many")
		})
	}
}

func TestParseManifest_V1BetaRefusesADuplicateResourceName(t *testing.T) {
	raw := editBeta(t,
		"    - name: app-security-group\n", "    - name: fallback-network\n",
		"  provides: [app-security-group]\n", "  provides: [fallback-network]\n")
	requireRefusedNaming(t, raw, "fallback-network")
}

func TestParseManifest_V1BetaRefusesAProvidesEntryNamingNoResource(t *testing.T) {
	raw := editBeta(t, "  provides: [app-security-group]\n", "  provides: [app-security-group, no-such-resource]\n")
	requireRefusedNaming(t, raw, "spec.provides", "no-such-resource")
}

// An alias or a merge key brings in text from elsewhere in the document,
// which the reader does not follow, so it refuses the manifest rather than
// guessing what it declares, and says which it found and where.
func TestParseManifest_RefusesAnAliasOrAMergeKey(t *testing.T) {
	parseBeta(t, []byte(betaManifest))
	cases := []struct {
		label string
		raw   []byte
		wants []string
	}{
		{"an alias as a resource type", editBeta(t,
			"      type: aws.vpc\n", "      type: &net aws.vpc\n",
			"      type: aws.security-group\n", "      type: *net\n"),
			[]string{"spec.resources[1].type", "alias"}},
		{"a merge key in a resource", editBeta(t,
			"    - name: app-security-group\n      type: aws.security-group\n",
			"    - name: app-security-group\n      <<: { type: aws.security-group }\n"),
			[]string{"spec.resources[1]", "merge key"}},
		{"an alias as a list", editBeta(t,
			"  provides: [app-security-group]\n", "  provides: &provided [app-security-group]\n",
			"    operations: [subscribe, unsubscribe]\n", "    operations: *provided\n"),
			[]string{"spec.lifecycle.operations", "alias"}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			requireRefusedNaming(t, tc.raw, tc.wants...)
		})
	}
}

// The parser accepts nothing manifest.schema.json refuses: a null value, a
// plain number or boolean where the schema wants a string, and an empty
// list where a block must declare at least one entry. Each refusal names
// its path.
func TestParseManifest_V1BetaRefusesWhatTheSchemaRefusesNamingItsPath(t *testing.T) {
	cases := []struct {
		label, path string
		edits       []string
	}{
		{"a null resource type", "spec.resources[1].type", []string{
			"      type: aws.security-group\n", "      type:\n"}},
		{"a null optional field", "metadata.owner", []string{
			"  name: Fixture Block\n", "  name: Fixture Block\n  owner:\n"}},
		{"a plain number in a string field", "metadata.name", []string{
			"  name: Fixture Block\n", "  name: 2048\n"}},
		{"a plain number as a param default", "spec.params[0].default", []string{
			"      label: Container image\n", "      label: Container image\n      default: 8080\n"}},
		{"a plain boolean in a string field", "spec.params[0].label", []string{
			"      label: Container image\n", "      label: true\n"}},
		{"no resources", "spec.resources", []string{
			"  resources:\n    - name: fallback-network\n      type: aws.vpc\n    - name: app-security-group\n" +
				"      type: aws.security-group\n  provides: [app-security-group]\n",
			"  resources: []\n"}},
		{"no lifecycle operations", "spec.lifecycle.operations", []string{
			"    operations: [subscribe, unsubscribe]\n", "    operations: []\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			requireRefusedNaming(t, editBeta(t, tc.edits...), tc.path)
		})
	}
}

// A quoted value is a string wherever it is written.
func TestParseManifest_V1BetaAcceptsAQuotedString(t *testing.T) {
	b := parseBeta(t, editBeta(t, "      label: Container image\n", "      label: Container image\n      default: \"8080\"\n"))
	if got := b.Spec.Params[0].Default; got != "8080" {
		t.Errorf("spec.params[0].default = %q, want %q", got, "8080")
	}
}

// v1alphaManifest carries a padded quoted id and version, a folded release
// note, and outputs and reads in both list forms.
const v1alphaManifest = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: " reads-consumer"
  name: Reads Consumer
spec:
  layer: backend
  version: " 1.2.0 "
  releaseClass: feature
  releaseNote: >
    Adds the reads.
  outputs:
    - dbName
    - host.type
  requires:
    - capability: database
      reads: [host, port]
    - capability: github
    - capability: app-host
      reads:
        - region
`

// Under v1alpha the parser reads the fields blockverify's manifest suite
// checks into V1AlphaManifest's own fields, under either top-level spelling.
func TestParseManifest_V1AlphaReadsOutputsAndRequiresReads(t *testing.T) {
	for label, raw := range map[string]string{
		"kind: Block": v1alphaManifest,
		"type: block": strings.Replace(v1alphaManifest, "kind: Block\n", "type: block\n", 1),
	} {
		t.Run(label, func(t *testing.T) {
			m, err := contract.ParseManifest([]byte(raw))
			if err != nil {
				t.Fatalf("ParseManifest: %v", err)
			}
			if m.APIVersion != contract.ManifestAPIVersionV1Alpha {
				t.Errorf("APIVersion = %q, want %q", m.APIVersion, contract.ManifestAPIVersionV1Alpha)
			}
			if m.Block != nil {
				t.Errorf("Block = %+v, want nil under %s", *m.Block, contract.ManifestAPIVersionV1Alpha)
			}
			if m.V1Alpha == nil {
				t.Fatalf("V1Alpha is nil under %s", contract.ManifestAPIVersionV1Alpha)
			}
			a := m.V1Alpha
			if a.ID != " reads-consumer" || m.ID() != " reads-consumer" {
				t.Errorf("V1Alpha.ID, ID() = %q, %q; want the untrimmed %q", a.ID, m.ID(), " reads-consumer")
			}
			if a.Version != "1.2.0" || m.Version() != "1.2.0" {
				t.Errorf("V1Alpha.Version, Version() = %q, %q; want the trimmed %q", a.Version, m.Version(), "1.2.0")
			}
			if a.ReleaseClass != "feature" || m.ReleaseClass() != "feature" {
				t.Errorf("V1Alpha.ReleaseClass, ReleaseClass() = %q, %q; want %q", a.ReleaseClass, m.ReleaseClass(), "feature")
			}
			if strings.TrimSpace(a.ReleaseNote) != "Adds the reads." || strings.TrimSpace(m.ReleaseNote()) != "Adds the reads." {
				t.Errorf("V1Alpha.ReleaseNote, ReleaseNote() = %q, %q; want %q", a.ReleaseNote, m.ReleaseNote(), "Adds the reads.")
			}
			if want := []string{"dbName", "host.type"}; !slices.Equal(a.Outputs, want) {
				t.Errorf("V1Alpha.Outputs = %q, want %q", a.Outputs, want)
			}
			wantRequires := []contract.V1AlphaRequirement{
				{Capability: "database", Reads: []string{"host", "port"}},
				{Capability: "github"},
				{Capability: "app-host", Reads: []string{"region"}},
			}
			if len(a.Requires) != len(wantRequires) {
				t.Fatalf("V1Alpha.Requires = %+v, want %+v", a.Requires, wantRequires)
			}
			for i, want := range wantRequires {
				if a.Requires[i].Capability != want.Capability || !slices.Equal(a.Requires[i].Reads, want.Reads) {
					t.Errorf("V1Alpha.Requires[%d] = %+v, want %+v", i, a.Requires[i], want)
				}
			}
		})
	}
}

func TestParseManifest_RefusesAnUnknownOrMissingAPIVersionNamingBothKnownVersions(t *testing.T) {
	body := "type: block\nmetadata:\n  id: fixture-block\nspec:\n  version: 1.0.0\n"
	cases := []struct {
		label, raw string
		wants      []string
	}{
		{"absent", body, nil},
		{"empty", "apiVersion: \"\"\n" + body, nil},
		{"a later version", "apiVersion: mitstack/v2\n" + body, []string{"mitstack/v2"}},
		{"another group", "apiVersion: example.test/v1beta\n" + body, []string{"example.test/v1beta"}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			wants := append([]string{contract.ManifestAPIVersionV1Alpha, contract.ManifestAPIVersion}, tc.wants...)
			requireRefusedNaming(t, []byte(tc.raw), wants...)
		})
	}
}

// specReceivesAppSecrets reads ManifestSpec.ReceivesAppSecrets by reflection,
// so a missing field fails its own test instead of the whole package build.
func specReceivesAppSecrets(t *testing.T, b *contract.BlockManifest) bool {
	t.Helper()
	f := reflect.ValueOf(b.Spec).FieldByName("ReceivesAppSecrets")
	if !f.IsValid() {
		t.Fatal("ManifestSpec has no ReceivesAppSecrets field")
	}
	if f.Kind() != reflect.Bool {
		t.Fatalf("ManifestSpec.ReceivesAppSecrets kind = %v, want bool", f.Kind())
	}
	return f.Bool()
}

func TestParseManifest_V1BetaReadsReceivesAppSecretsAsAnOptionalFlag(t *testing.T) {
	for _, tc := range []struct {
		label, line string
		want        bool
	}{
		{"absent reads false", "", false},
		{"true reads true", "  receivesAppSecrets: true\n", true},
		{"false reads false", "  receivesAppSecrets: false\n", false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			b := parseBeta(t, editBeta(t, "  version: 1.0.0\n", "  version: 1.0.0\n"+tc.line))

			if got := specReceivesAppSecrets(t, b); got != tc.want {
				t.Errorf("spec.receivesAppSecrets after %q = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

func TestParseManifest_V1BetaReceivesAppSecretsTakesTrueOrFalseOnly(t *testing.T) {
	for _, value := range []string{"yes", "1", `"true"`, "sometimes", "null"} {
		t.Run("refuses "+value, func(t *testing.T) {
			raw := editBeta(t, "  version: 1.0.0\n", "  version: 1.0.0\n  receivesAppSecrets: "+value+"\n")

			requireRefusedNaming(t, raw, "spec.receivesAppSecrets")
		})
	}
}

func TestManifestSpec_ReceivesAppSecretsMarshalsUnderItsOptionalKey(t *testing.T) {
	f, ok := reflect.TypeOf(contract.ManifestSpec{}).FieldByName("ReceivesAppSecrets")
	if !ok {
		t.Fatal("ManifestSpec has no ReceivesAppSecrets field")
	}

	if got, want := f.Tag.Get("json"), "receivesAppSecrets,omitempty"; got != want {
		t.Errorf("ManifestSpec.ReceivesAppSecrets json tag = %q, want %q", got, want)
	}
}

// lifecycleNeedsCredentialRefresh reads the field by reflection, so a missing field fails its own test instead of the package build.
func lifecycleNeedsCredentialRefresh(t *testing.T, b *contract.BlockManifest) bool {
	t.Helper()
	f := reflect.ValueOf(b.Spec.Lifecycle).FieldByName("NeedsCredentialRefresh")
	if !f.IsValid() {
		t.Fatal("ManifestLifecycle has no NeedsCredentialRefresh field")
	}
	if f.Kind() != reflect.Bool {
		t.Fatalf("ManifestLifecycle.NeedsCredentialRefresh kind = %v, want bool", f.Kind())
	}
	return f.Bool()
}

func TestParseManifest_V1BetaReadsNeedsCredentialRefreshAsAnOptionalLifecycleFlag(t *testing.T) {
	for _, tc := range []struct {
		label, line string
		want        bool
	}{
		{"absent reads false", "", false},
		{"true reads true", "    needsCredentialRefresh: true\n", true},
		{"false reads false", "    needsCredentialRefresh: false\n", false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			b := parseBeta(t, editBeta(t, "    contract: kit/v1\n", "    contract: kit/v1\n"+tc.line))

			if got := lifecycleNeedsCredentialRefresh(t, b); got != tc.want {
				t.Errorf("spec.lifecycle.needsCredentialRefresh after %q = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

func TestParseManifest_V1BetaNeedsCredentialRefreshTakesTrueOrFalseOnly(t *testing.T) {
	for _, value := range []string{"yes", "1", `"true"`, "sometimes", "null"} {
		t.Run("refuses "+value, func(t *testing.T) {
			raw := editBeta(t, "    contract: kit/v1\n", "    contract: kit/v1\n    needsCredentialRefresh: "+value+"\n")

			requireRefusedNaming(t, raw, "spec.lifecycle.needsCredentialRefresh")
		})
	}
}

func TestParseManifest_V1BetaRefusesAnUnknownLifecycleKeyBesideNeedsCredentialRefresh(t *testing.T) {
	raw := editBeta(t, "    contract: kit/v1\n", "    contract: kit/v1\n    needsCredentialRefresh: true\n    bogus: 1\n")

	requireRefusedNaming(t, raw, "spec.lifecycle.bogus")
}

func TestManifestLifecycle_NeedsCredentialRefreshMarshalsUnderItsOptionalKey(t *testing.T) {
	f, ok := reflect.TypeOf(contract.ManifestLifecycle{}).FieldByName("NeedsCredentialRefresh")
	if !ok {
		t.Fatal("ManifestLifecycle has no NeedsCredentialRefresh field")
	}

	if got, want := f.Tag.Get("json"), "needsCredentialRefresh,omitempty"; got != want {
		t.Errorf("ManifestLifecycle.NeedsCredentialRefresh json tag = %q, want %q", got, want)
	}
}

func TestParseManifest_SettleBudget(t *testing.T) {
	beta := func(line string) []byte {
		return editBeta(t, "    contract: kit/v1\n", "    contract: kit/v1\n"+line)
	}
	for _, tc := range []struct {
		label   string
		raw     []byte
		want    time.Duration
		refusal []string
	}{
		{"absent under v1beta reads zero", beta(""), 0, nil},
		{"75m under v1beta reads 75 minutes", beta("    settleBudget: 75m\n"), 75 * time.Minute, nil},
		{"0s is refused", beta("    settleBudget: 0s\n"), 0, []string{"spec.lifecycle.settleBudget", "positive duration"}},
		{"-5m is refused", beta("    settleBudget: -5m\n"), 0, []string{"spec.lifecycle.settleBudget", "positive duration"}},
		{"soon is refused", beta("    settleBudget: soon\n"), 0, []string{"spec.lifecycle.settleBudget", "positive duration"}},
		{"30 is refused", beta("    settleBudget: 30\n"), 0, []string{"spec.lifecycle.settleBudget", "positive duration"}},
		{"v1alpha refuses the key", []byte(alphaHead + "  lifecycle:\n    endpoint: https://fixture.example.test/hook\n" +
			"    settleBudget: 75m\n"), 0, []string{"spec.lifecycle.settleBudget"}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			if tc.refusal != nil {
				requireRefusedNaming(t, tc.raw, tc.refusal...)
				return
			}

			lc := parseBeta(t, tc.raw).Spec.Lifecycle
			raw, err := json.Marshal(lc)
			var back contract.ManifestLifecycle
			var fields map[string]any
			if err == nil {
				err = errors.Join(json.Unmarshal(raw, &back), json.Unmarshal(raw, &fields))
			}

			if err != nil {
				t.Fatalf("JSON round trip of %+v: %v", lc, err)
			}
			if lc.SettleBudget != tc.want {
				t.Errorf("spec.lifecycle.settleBudget = %v, want %v", lc.SettleBudget, tc.want)
			}
			if !reflect.DeepEqual(back, lc) {
				t.Errorf("ManifestLifecycle after a JSON round trip through %s = %+v, want %+v", raw, back, lc)
			}
			written, _ := fields["settleBudget"].(string)
			if tc.want == 0 {
				return
			}
			if again := parseBeta(t, beta("    settleBudget: "+written+"\n")).Spec.Lifecycle.SettleBudget; again != tc.want {
				t.Errorf("JSON settleBudget %#v re-parses as %v, want %v (%s)", fields["settleBudget"], again, tc.want, raw)
			}
		})
	}
}
