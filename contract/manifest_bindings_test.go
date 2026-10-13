package contract_test

// manifest_bindings_test.go pins how a mitstack/v1beta manifest's
// requirements and resources are judged against the platform's type
// snapshot, and how a binding a block's code reads through them is judged:
//
//   - CheckManifestTypes refuses a requirement or resource listing a type
//     the snapshot does not hold, naming the entry and the type;
//   - CheckBinding judges requires.<reference>.outputs.<output> and
//     resources.<name>.outputs.<output>, where a reference and an output may
//     each hold dots. It refuses a reference or name the block does not
//     have, and an output any listed type does not declare, naming that type
//     and the output. The outputs it reads are the snapshot's, not the kit's
//     own catalogue's.
//
// A requirement's reference, its name when set and its type id otherwise,
// appears once in spec.requires. The refusal names both entries' indexes
// and the shared reference.

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

const (
	awsAppInfraGolden = "aws-app-infra-v1beta.yaml"
	plainGolden       = "plain-v1beta.yaml"
)

// editGolden returns the manifest golden called name with each old, new
// pair of replacements applied once. It fails the test when an old text is
// not in the manifest, so an edit can never silently become a no-op.
func editGolden(t *testing.T, name string, pairs ...string) []byte {
	t.Helper()
	out := string(readManifestGolden(t, name))
	for i := 0; i+1 < len(pairs); i += 2 {
		if !strings.Contains(out, pairs[i]) {
			t.Fatalf("fixture edit: %q is not in %s", pairs[i], name)
		}
		out = strings.Replace(out, pairs[i], pairs[i+1], 1)
	}
	return []byte(out)
}

// platformSnapshot reads the platform's type snapshot fixture.
func platformSnapshot(t *testing.T) contract.TypeSnapshot {
	t.Helper()
	raw, err := os.ReadFile("testdata/type-snapshot.json")
	if err != nil {
		t.Fatalf("reading the type snapshot fixture: %v", err)
	}
	snap, err := contract.ParseTypeSnapshot(raw)
	if err != nil {
		t.Fatalf("ParseTypeSnapshot(testdata/type-snapshot.json): %v", err)
	}
	return snap
}

// specOf parses raw and returns its spec.
func specOf(t *testing.T, raw []byte) contract.ManifestSpec {
	t.Helper()
	return parseBeta(t, raw).Spec
}

func requireErrorNaming(t *testing.T, what string, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s accepted, want an error naming %q", what, wants)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s error = %q, want it to name %q", what, err, want)
		}
	}
}

func TestParseManifest_ParsesEveryManifestGolden(t *testing.T) {
	entries, err := fs.ReadDir(contract.ManifestGoldens, contract.ManifestGoldensDir)
	if err != nil {
		t.Fatalf("fs.ReadDir(contract.ManifestGoldens, contract.ManifestGoldensDir): %v", err)
	}
	read := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		read++
		t.Run(e.Name(), func(t *testing.T) {
			if _, err := contract.ParseManifest(readManifestGolden(t, e.Name())); err != nil {
				t.Errorf("ParseManifest(%s): %v", e.Name(), err)
			}
		})
	}
	if read == 0 {
		t.Fatal("manifest-goldens holds no file, so parsing every golden proves nothing")
	}
}

// --- CheckManifestTypes ------------------------------------------------------

func TestCheckManifestTypes_AcceptsEveryTypeTheSnapshotHolds(t *testing.T) {
	snap := platformSnapshot(t)
	for label, raw := range map[string][]byte{
		awsAppInfraGolden: readManifestGolden(t, awsAppInfraGolden),
		plainGolden:       readManifestGolden(t, plainGolden),
		"typed manifest":  []byte(typedManifest),
	} {
		t.Run(label, func(t *testing.T) {
			if err := contract.CheckManifestTypes(specOf(t, raw), snap); err != nil {
				t.Errorf("CheckManifestTypes: %v", err)
			}
		})
	}
}

func TestCheckManifestTypes_RefusesATypeTheSnapshotDoesNotHoldNamingTheEntryAndTheType(t *testing.T) {
	cases := []struct {
		label, old, new string
		wants           []string
	}{
		{"a resource", "    - aws.vpc\n", "    - aws.vpcs\n",
			[]string{"spec.resources[1]", "aws.vpcs"}},
		{"a named resource", "    - { name: ecr-push-role, type: aws.iam-role }\n", "    - { name: ecr-push-role, type: aws.iam-roles }\n",
			[]string{"spec.resources[6]", "aws.iam-roles"}},
		{"a requirement", "  requires: [github.repository]\n", "  requires: [github.repositories]\n",
			[]string{"spec.requires[0]", "github.repositories"}},
		{"one type of a requirement's list", "  requires: [github.repository]\n",
			"  requires: [github.repository, { name: host, type: [aws.ec2-instance, hetzner.cloud-server] }]\n",
			[]string{"spec.requires[1]", "hetzner.cloud-server"}},
	}
	snap := platformSnapshot(t)
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			spec := specOf(t, editGolden(t, awsAppInfraGolden, tc.old, tc.new))
			requireErrorNaming(t, "CheckManifestTypes", contract.CheckManifestTypes(spec, snap), tc.wants...)
		})
	}
}

// --- CheckBinding ----------------------------------------------------------------

// bindingSpec requires a repository by its type, a host listing both host
// types and two roles of one type by name, and declares a container listing
// both container types beside aws-app-infra's own resources.
func bindingSpec(t *testing.T) contract.ManifestSpec {
	t.Helper()
	return specOf(t, editGolden(t, awsAppInfraGolden,
		"  requires: [github.repository]\n",
		"  requires:\n    - github.repository\n    - aws.ec2-instance\n"+
			"    - { name: host, type: [aws.ec2-instance, hetzner.server] }\n"+
			"    - { name: primary, type: aws.iam-role }\n",
		"    - aws.route53-record\n",
		"    - aws.route53-record\n    - { name: app-container, type: [aws.ec2-container, hetzner.server-container] }\n"))
}

func TestCheckBinding_AcceptsAnOutputEveryListedTypeDeclares(t *testing.T) {
	spec, snap := bindingSpec(t), platformSnapshot(t)
	for _, binding := range []string{
		"requires.aws.ec2-instance.outputs.publicIp",
		"requires.aws.ec2-instance.outputs.cloud.region",
		"requires.github.repository.outputs.repo",
		"requires.host.outputs.hostId",
		"requires.host.outputs.cloud.region",
		"requires.primary.outputs.roleArn",
		"resources.deploy-role.outputs.roleArn",
		"resources.aws.route53-record.outputs.appUrl",
		"resources.aws.ec2-instance.outputs.publicIp",
		"resources.app-container.outputs.url",
	} {
		t.Run(binding, func(t *testing.T) {
			if err := contract.CheckBinding(spec, snap, binding); err != nil {
				t.Errorf("CheckBinding(%s): %v", binding, err)
			}
		})
	}
}

func TestCheckBinding_RefusesAnOutputAListedTypeDoesNotDeclareNamingTheTypeAndTheOutput(t *testing.T) {
	cases := []struct {
		binding string
		wants   []string
	}{
		{"requires.aws.ec2-instance.outputs.dbName", []string{"aws.ec2-instance", "dbName"}},
		{"requires.host.outputs.publicIp", []string{"hetzner.server", "publicIp"}},
		{"requires.github.repository.outputs.registryHost", []string{"github.repository", "registryHost"}},
		{"resources.deploy-role.outputs.publicIp", []string{"aws.iam-role", "publicIp"}},
		{"resources.aws.vpc.outputs.vpcId", []string{"aws.vpc", "vpcId"}},
	}
	spec, snap := bindingSpec(t), platformSnapshot(t)
	for _, tc := range cases {
		t.Run(tc.binding, func(t *testing.T) {
			requireErrorNaming(t, "CheckBinding("+tc.binding+")", contract.CheckBinding(spec, snap, tc.binding), tc.wants...)
		})
	}
}

func TestCheckBinding_RefusesAReferenceOrResourceTheBlockDoesNotHave(t *testing.T) {
	cases := []struct {
		binding, missing string
	}{
		{"requires.missing.outputs.repo", "missing"},
		{"requires.github.repositry.outputs.repo", "github.repositry"},
		{"requires.hetzner.server.outputs.hostId", "hetzner.server"},
		{"resources.missing.outputs.publicIp", "missing"},
		{"resources.aws.iam-role.outputs.roleArn", "aws.iam-role"},
	}
	spec, snap := bindingSpec(t), platformSnapshot(t)
	for _, tc := range cases {
		t.Run(tc.binding, func(t *testing.T) {
			requireErrorNaming(t, "CheckBinding("+tc.binding+")", contract.CheckBinding(spec, snap, tc.binding), tc.missing)
		})
	}
}

// snapshotOf parses a snapshot holding the given types, failing the test
// when it is refused.
func snapshotOf(t *testing.T, types string) contract.TypeSnapshot {
	t.Helper()
	snap, err := contract.ParseTypeSnapshot([]byte(`{"version": 7, "types": [` + types + `]}`))
	if err != nil {
		t.Fatalf("ParseTypeSnapshot: %v", err)
	}
	return snap
}

// A snapshot decides both bindings, so the outputs CheckBinding reads are the
// snapshot's.
func TestCheckBinding_ReadsEachOutputFromTheSnapshot(t *testing.T) {
	snap := snapshotOf(t,
		`{"id": "aws.ec2-instance", "capability": "aws", "kind": "host", "scope": "per-app", "pricing": {"priced": false},`+
			` "outputs": [{"name": "hostId", "secret": false, "required": true}], "version": 1},`+
			`{"id": "aws.s3-bucket", "capability": "aws", "kind": "object-storage", "scope": "per-app", "stateful": true, "pricing": {"priced": false},`+
			` "outputs": [{"name": "bucketName", "secret": false, "required": true}], "version": 1}`)
	spec := specOf(t, []byte(strings.Replace(typedManifest, "    - github.repository\n", "    - aws.ec2-instance\n", 1)))

	requireErrorNaming(t, "CheckBinding through a snapshot without publicIp",
		contract.CheckBinding(spec, snap, "requires.aws.ec2-instance.outputs.publicIp"), "aws.ec2-instance", "publicIp")
	if err := contract.CheckBinding(spec, snap, "resources.state.outputs.bucketName"); err != nil {
		t.Errorf("CheckBinding(resources.state.outputs.bucketName) through a snapshot declaring it: %v", err)
	}
}

// --- requirement references ---------------------------------------------------------

// The plain golden's requires line, which requires a host at spec.requires[0]
// and a database at spec.requires[1].
const plainRequires = "  requires: [aws.ec2-instance, aws.ec2-postgres-database]\n"

func TestParseManifest_V1BetaRefusesTwoRequirementsSharingAReference(t *testing.T) {
	cases := []struct {
		label, ref, first, second, requires string
	}{
		{"two unnamed requirements of one type", "aws.ec2-postgres-database", "spec.requires[1]", "spec.requires[2]",
			"  requires: [aws.ec2-instance, aws.ec2-postgres-database, aws.ec2-postgres-database]\n"},
		{"a requirement named for the type of an unnamed one", "aws.ec2-instance", "spec.requires[0]", "spec.requires[2]",
			"  requires:\n    - aws.ec2-instance\n    - aws.ec2-postgres-database\n" +
				"    - { name: aws.ec2-instance, type: aws.ec2-postgres-database }\n"},
		{"two requirements of one name", "tracker-db", "spec.requires[1]", "spec.requires[2]",
			"  requires:\n    - aws.ec2-instance\n" +
				"    - { name: tracker-db, type: aws.ec2-postgres-database }\n" +
				"    - { name: tracker-db, type: aws.ec2-postgres-database }\n"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			raw := editGolden(t, plainGolden, plainRequires, tc.requires)
			requireRefusedNaming(t, raw, tc.first, tc.second, `"`+tc.ref+`"`, "appears once")
		})
	}
}

// Requirements of one type parse while their references differ, and each is
// bound through its own reference.
func TestParseManifest_V1BetaAcceptsRequirementsOfOneTypeWithDistinctReferences(t *testing.T) {
	cases := map[string]string{
		"two named requirements": "  requires:\n    - aws.ec2-instance\n" +
			"    - { name: tracker-db, type: aws.ec2-postgres-database }\n" +
			"    - { name: audit-db, type: aws.ec2-postgres-database }\n",
		"a named and an unnamed requirement": "  requires:\n    - aws.ec2-instance\n    - aws.ec2-postgres-database\n" +
			"    - { name: audit-db, type: aws.ec2-postgres-database }\n",
	}
	snap := platformSnapshot(t)
	for label, requires := range cases {
		t.Run(label, func(t *testing.T) {
			b := parseBeta(t, editGolden(t, plainGolden, plainRequires, requires))
			if len(b.Spec.Requires) != 3 {
				t.Fatalf("spec.requires = %+v, want the host and both database requirements", b.Spec.Requires)
			}
			if err := contract.CheckBinding(b.Spec, snap, "requires.audit-db.outputs.dbName"); err != nil {
				t.Errorf("CheckBinding(requires.audit-db.outputs.dbName): %v", err)
			}
		})
	}
}
