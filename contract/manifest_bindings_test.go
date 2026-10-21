package contract_test

// CheckManifestTypes and CheckBinding judge a v1beta manifest's requirements, resources and bindings against the platform's type snapshot.

import (
	"io/fs"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
)

const (
	awsAppInfraGolden = "aws-app-infra-v1beta.yaml"
	plainGolden       = "plain-v1beta.yaml"
)

func editGolden(t *testing.T, name string, pairs ...string) []byte {
	t.Helper()
	return edit(t, string(readManifestGolden(t, name)), pairs...)
}

func snapshotOf(t *testing.T, types string) contract.TypeSnapshot {
	t.Helper()
	snap, err := contract.ParseTypeSnapshot([]byte(`{"version": 7, "types": [` + types + `]}`))
	if err != nil {
		t.Fatalf("ParseTypeSnapshot: %v", err)
	}
	return snap
}

func TestParseManifest_ParsesEveryManifestGolden(t *testing.T) {
	entries, err := fs.ReadDir(contract.ManifestGoldens, contract.ManifestGoldensDir)
	if err != nil {
		t.Fatalf("fs.ReadDir(contract.ManifestGoldens, contract.ManifestGoldensDir): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("manifest-goldens holds no file, so parsing every golden proves nothing")
	}
	for _, e := range entries {
		t.Run(e.Name(), func(t *testing.T) {
			_, err := contract.ParseManifest(readManifestGolden(t, e.Name()))

			if err != nil {
				t.Errorf("ParseManifest(%s): %v", e.Name(), err)
			}
		})
	}
}

func TestCheckManifestTypes_RefusesExactlyTheTypesTheSnapshotLacksNamingTheEntry(t *testing.T) {
	cases := []struct {
		label string
		raw   []byte
		wants []string
	}{
		{awsAppInfraGolden, readManifestGolden(t, awsAppInfraGolden), nil},
		{plainGolden, readManifestGolden(t, plainGolden), nil},
		{"the typed manifest", []byte(typedManifest), nil},
		{"a resource", editGolden(t, awsAppInfraGolden, "    - aws.vpc\n", "    - aws.vpcs\n"), []string{"spec.resources[1]", "aws.vpcs"}},
		{"a named resource", editGolden(t, awsAppInfraGolden, "    - { name: ecr-push-role, type: aws.iam-role }\n",
			"    - { name: ecr-push-role, type: aws.iam-roles }\n"), []string{"spec.resources[6]", "aws.iam-roles"}},
		{"a requirement", editGolden(t, awsAppInfraGolden, "  requires: [github.repository]\n", "  requires: [github.repositories]\n"),
			[]string{"spec.requires[0]", "github.repositories"}},
		{"one type of a requirement's list", editGolden(t, awsAppInfraGolden, "  requires: [github.repository]\n",
			"  requires: [github.repository, { name: host, type: [aws.ec2-instance, hetzner.cloud-server] }]\n"),
			[]string{"spec.requires[1]", "hetzner.cloud-server"}},
	}
	snap := platformSnapshot(t)
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			spec := parseBeta(t, tc.raw).Spec

			err := contract.CheckManifestTypes(spec, snap)

			if tc.wants == nil {
				if err != nil {
					t.Errorf("CheckManifestTypes: %v", err)
				}
				return
			}
			requireErrorNaming(t, "CheckManifestTypes", err, tc.wants...)
		})
	}
}

// bindingSpec requires a repository by its type, a host listing both host types and a role by name, and declares a container listing both container types beside aws-app-infra's own resources.
func bindingSpec(t *testing.T) contract.ManifestSpec {
	t.Helper()
	return parseBeta(t, editGolden(t, awsAppInfraGolden,
		"  requires: [github.repository]\n",
		"  requires:\n    - github.repository\n    - aws.ec2-instance\n"+
			"    - { name: host, type: [aws.ec2-instance, hetzner.server] }\n"+
			"    - { name: primary, type: aws.iam-role }\n",
		"    - aws.route53-record\n",
		"    - aws.route53-record\n    - { name: app-container, type: [aws.ec2-container, hetzner.server-container] }\n")).Spec
}

func TestCheckBinding_AcceptsOnlyAnOutputEveryListedTypeDeclares(t *testing.T) {
	spec, snap := bindingSpec(t), platformSnapshot(t)
	// Two types of the platform's, the instance's without publicIp, so the outputs CheckBinding reads are the snapshot's.
	small := snapshotOf(t,
		`{"id": "aws.ec2-instance", "capability": "aws", "kind": "host", "scope": "per-app", "pricing": {"priced": false},`+
			` "outputs": [{"name": "hostId", "secret": false, "required": true}], "version": 1},`+
			`{"id": "aws.s3-bucket", "capability": "aws", "kind": "object-storage", "scope": "per-app", "stateful": true, "pricing": {"priced": false},`+
			` "outputs": [{"name": "bucketName", "secret": false, "required": true}], "version": 1}`)
	typed := parseBeta(t, edit(t, typedManifest, "    - github.repository\n", "    - aws.ec2-instance\n")).Spec
	cases := []struct {
		binding string
		spec    contract.ManifestSpec
		snap    contract.TypeSnapshot
		wants   []string
		note    string
	}{
		{"requires.aws.ec2-instance.outputs.publicIp", spec, snap, nil, ""},
		{"requires.aws.ec2-instance.outputs.cloud.region", spec, snap, nil, ""},
		{"requires.github.repository.outputs.repo", spec, snap, nil, ""},
		{"requires.host.outputs.hostId", spec, snap, nil, ""},
		{"requires.host.outputs.cloud.region", spec, snap, nil, ""},
		{"requires.primary.outputs.roleArn", spec, snap, nil, ""},
		{"resources.deploy-role.outputs.roleArn", spec, snap, nil, ""},
		{"resources.aws.route53-record.outputs.appUrl", spec, snap, nil, ""},
		{"resources.aws.ec2-instance.outputs.publicIp", spec, snap, nil, ""},
		{"resources.app-container.outputs.url", spec, snap, nil, ""},
		{"requires.aws.ec2-instance.outputs.dbName", spec, snap, []string{"aws.ec2-instance", "dbName"}, ""},
		{"requires.host.outputs.publicIp", spec, snap, []string{"hetzner.server", "publicIp"}, ""},
		{"requires.github.repository.outputs.registryHost", spec, snap, []string{"github.repository", "registryHost"}, ""},
		{"resources.deploy-role.outputs.publicIp", spec, snap, []string{"aws.iam-role", "publicIp"}, ""},
		{"resources.aws.vpc.outputs.vpcId", spec, snap, []string{"aws.vpc", "vpcId"}, ""},
		{"requires.missing.outputs.repo", spec, snap, []string{"missing"}, ""},
		{"requires.github.repositry.outputs.repo", spec, snap, []string{"github.repositry"}, ""},
		{"requires.hetzner.server.outputs.hostId", spec, snap, []string{"hetzner.server"}, ""},
		{"resources.missing.outputs.publicIp", spec, snap, []string{"missing"}, ""},
		{"resources.aws.iam-role.outputs.roleArn", spec, snap, []string{"aws.iam-role"}, ""},
		{"requires.aws.ec2-instance.outputs.publicIp", typed, small, []string{"aws.ec2-instance", "publicIp"}, " through a snapshot without it"},
		{"resources.state.outputs.bucketName", typed, small, nil, " through a snapshot declaring it"},
	}
	for _, tc := range cases {
		t.Run(tc.binding+tc.note, func(t *testing.T) {
			err := contract.CheckBinding(tc.spec, tc.snap, tc.binding)

			if tc.wants == nil {
				if err != nil {
					t.Errorf("CheckBinding(%s): %v", tc.binding, err)
				}
				return
			}
			requireErrorNaming(t, "CheckBinding("+tc.binding+")", err, tc.wants...)
		})
	}
}

// plainRequires is the plain golden's requires line, with a host at spec.requires[0] and a database at spec.requires[1].
const plainRequires = "  requires: [aws.ec2-instance, aws.ec2-postgres-database]\n"

func TestParseManifest_V1BetaTellsRequirementsOfOneTypeApartByReference(t *testing.T) {
	cases := []struct {
		label, requires string
		wants           []string
	}{
		{"two unnamed requirements of one type are refused",
			"  requires: [aws.ec2-instance, aws.ec2-postgres-database, aws.ec2-postgres-database]\n",
			[]string{"spec.requires[1]", "spec.requires[2]", `"aws.ec2-postgres-database"`, "appears once"}},
		{"a requirement named for the type of an unnamed one is refused",
			"  requires:\n    - aws.ec2-instance\n    - aws.ec2-postgres-database\n    - { name: aws.ec2-instance, type: aws.ec2-postgres-database }\n",
			[]string{"spec.requires[0]", "spec.requires[2]", `"aws.ec2-instance"`, "appears once"}},
		{"two requirements of one name are refused",
			"  requires:\n    - aws.ec2-instance\n    - { name: tracker-db, type: aws.ec2-postgres-database }\n    - { name: tracker-db, type: aws.ec2-postgres-database }\n",
			[]string{"spec.requires[1]", "spec.requires[2]", `"tracker-db"`, "appears once"}},
		{"two named requirements are each bound through their own reference",
			"  requires:\n    - aws.ec2-instance\n    - { name: tracker-db, type: aws.ec2-postgres-database }\n    - { name: audit-db, type: aws.ec2-postgres-database }\n", nil},
		{"a named and an unnamed requirement are each bound through their own reference",
			"  requires:\n    - aws.ec2-instance\n    - aws.ec2-postgres-database\n    - { name: audit-db, type: aws.ec2-postgres-database }\n", nil},
	}
	snap := platformSnapshot(t)
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			raw := editGolden(t, plainGolden, plainRequires, tc.requires)

			if tc.wants != nil {
				requireRefusedNaming(t, raw, tc.wants...)
				return
			}
			b := parseBeta(t, raw)
			if len(b.Spec.Requires) != 3 {
				t.Fatalf("spec.requires = %+v, want the host and both database requirements", b.Spec.Requires)
			}
			if err := contract.CheckBinding(b.Spec, snap, "requires.audit-db.outputs.dbName"); err != nil {
				t.Errorf("CheckBinding(requires.audit-db.outputs.dbName): %v", err)
			}
		})
	}
}
