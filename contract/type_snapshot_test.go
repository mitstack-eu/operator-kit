package contract_test

// contract.ParseTypeSnapshot reads the snapshot the platform serves at GET /v1/resource-types/snapshots/{version}, as the 56-type fixture under testdata holds it.

import (
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

func TestParseTypeSnapshot_ReadsThePlatformsSnapshot(t *testing.T) {
	lookup := func(t *testing.T, snap contract.TypeSnapshot, id string) contract.SnapshotType {
		t.Helper()
		typ, ok := snap.Lookup(id)
		if !ok {
			t.Fatalf("Lookup(%q) found nothing", id)
		}
		return typ
	}
	cases := []struct {
		label string
		check func(t *testing.T, snap contract.TypeSnapshot)
	}{
		{"its version and its 56 types", func(t *testing.T, snap contract.TypeSnapshot) {
			if snap.Version != 1 || len(snap.Types) != 56 {
				t.Errorf("Version, types = %d, %d; want 1 and the platform's 56", snap.Version, len(snap.Types))
			}
		}},
		{"aws.ec2-instance's fields", func(t *testing.T, snap contract.TypeSnapshot) {
			instance := lookup(t, snap, "aws.ec2-instance")
			if instance.ID != "aws.ec2-instance" || instance.Capability != "aws" || instance.Kind != "host" ||
				instance.Scope != "per-app" || instance.Stateful || instance.Version != 1 {
				t.Errorf("aws.ec2-instance = %+v, want id aws.ec2-instance, capability aws, kind host, scope per-app, not stateful, version 1", instance)
			}
		}},
		{"aws.ec2-instance's outputs, required, optional and undeclared", func(t *testing.T, snap contract.TypeSnapshot) {
			instance := lookup(t, snap, "aws.ec2-instance")
			if hostID, ok := instance.Output("hostId"); !ok || hostID != (contract.SnapshotOutput{Name: "hostId", Required: true}) {
				t.Errorf(`aws.ec2-instance Output("hostId") = %+v, %v; want a required plain output`, hostID, ok)
			}
			if region, ok := instance.Output("cloud.region"); !ok || region.Required || region.Secret {
				t.Errorf(`aws.ec2-instance Output("cloud.region") = %+v, %v; want an optional plain output`, region, ok)
			}
			if _, ok := instance.Output("dbName"); ok {
				t.Error(`aws.ec2-instance Output("dbName") found an output the type does not declare`)
			}
		}},
		{"aws.iam-role's secret output", func(t *testing.T, snap contract.TypeSnapshot) {
			if arn, ok := lookup(t, snap, "aws.iam-role").Output("roleArn"); !ok || !arn.Secret {
				t.Errorf(`aws.iam-role Output("roleArn") = %+v, %v; want a secret output`, arn, ok)
			}
		}},
		{"aws.ebs-volume's state, budgets, failures and prose", func(t *testing.T, snap contract.TypeSnapshot) {
			volume := lookup(t, snap, "aws.ebs-volume")
			if !volume.Stateful || volume.Category != "storage" || volume.Pricing.Priced {
				t.Errorf("aws.ebs-volume = %+v, want stateful, category storage, not priced", volume)
			}
			if volume.TimeBudgets["observe"] != "30s" || len(volume.Failures) == 0 || volume.Failures[0].Class != "transient" {
				t.Errorf("aws.ebs-volume time budgets %v, failures %+v; want observe 30s and its failure list", volume.TimeBudgets, volume.Failures)
			}
			if !strings.Contains(volume.Observe, "DescribeVolumes") || !strings.Contains(volume.Absence, "NotFound") {
				t.Errorf("aws.ebs-volume observe %q, absence %q; want the type's prose", volume.Observe, volume.Absence)
			}
		}},
		{"hetzner.k3s-cluster hosts kubernetes", func(t *testing.T, snap contract.TypeSnapshot) {
			wantField(t, "hetzner.k3s-cluster hosts", lookup(t, snap, "hetzner.k3s-cluster").Hosts, "kubernetes")
		}},
		{"ubicloud.runner relies on github", func(t *testing.T, snap contract.TypeSnapshot) {
			wantField(t, "ubicloud.runner reliesOn", lookup(t, snap, "ubicloud.runner").ReliesOn, []string{"github"})
		}},
		{"the retired host.container is not a type", func(t *testing.T, snap contract.TypeSnapshot) {
			if _, ok := snap.Lookup("host.container"); ok {
				t.Error(`Lookup("host.container") found a retired type`)
			}
		}},
		{"no type is marked endsWithSubscription, since the fixture carries no such field", func(t *testing.T, snap contract.TypeSnapshot) {
			for _, typ := range snap.Types {
				if typ.EndsWithSubscription {
					t.Errorf("type %s is marked endsWithSubscription; want none", typ.ID)
				}
			}
		}},
		{"aws.ec2-instance lists its parts", func(t *testing.T, snap contract.TypeSnapshot) {
			wantField(t, "aws.ec2-instance Parts", lookup(t, snap, "aws.ec2-instance").Parts, []contract.SnapshotPart{
				{Type: "aws.iam-role", Count: 2}, {Type: "aws.iam-instance-profile", Count: 1}, {Type: "aws.elastic-ip", Count: 1}})
		}},
		{"each part names a type the snapshot holds", func(t *testing.T, snap contract.TypeSnapshot) {
			for _, typ := range snap.Types {
				for _, part := range typ.Parts {
					if _, ok := snap.Lookup(part.Type); !ok {
						t.Errorf("type %s lists part %q, which the snapshot does not hold", typ.ID, part.Type)
					}
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			snap := platformSnapshot(t)

			tc.check(t, snap)
		})
	}
}

// iamRoleType is a snapshot type a part may name.
const iamRoleType = `{"id": "aws.iam-role", "capability": "aws", "kind": "iam-role", "scope": "per-app", "version": 1}`

const instanceHead = `{"id": "aws.ec2-instance", "capability": "aws", "kind": "host", "scope": "per-app", "version": 1,`

func TestParseTypeSnapshot_RefusesASnapshotItCannotUse(t *testing.T) {
	cases := []struct {
		label, raw string
		wants      []string
	}{
		{"not JSON", `{"version": 1, "types": [`, nil},
		{"no types", `{"version": 1, "types": []}`, []string{"type"}},
		{"no types key", `{"version": 1}`, []string{"type"}},
		{"one type id twice", `{"version": 1, "types": [{"id": "aws.vpc", "capability": "aws", "kind": "network", "scope": "per-app", "version": 1},` +
			` {"id": "aws.vpc", "capability": "aws", "kind": "network", "scope": "per-app", "version": 2}]}`, []string{"aws.vpc"}},
		{"a part whose count is below one", `{"version": 1, "types": [` + instanceHead + ` "parts": [{"type": "aws.iam-role", "count": 0}]}, ` +
			iamRoleType + `]}`, []string{"aws.ec2-instance", "aws.iam-role", "count"}},
		{"a part with no type", `{"version": 1, "types": [` + instanceHead + ` "parts": [{"count": 1}]}]}`, []string{"aws.ec2-instance", "type"}},
		{"a part keyed kind alone", `{"version": 1, "types": [` + instanceHead + ` "parts": [{"kind": "aws.iam-role", "count": 1}]}, ` +
			iamRoleType + `]}`, []string{"aws.ec2-instance", "type"}},
		{"a part whose type the snapshot does not hold", `{"version": 1, "types": [` + instanceHead +
			` "parts": [{"type": "aws.iam-instance-profile", "count": 1}]}, ` + iamRoleType + `]}`, []string{"aws.ec2-instance", "aws.iam-instance-profile"}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			_, err := contract.ParseTypeSnapshot([]byte(tc.raw))

			requireErrorNaming(t, "ParseTypeSnapshot("+tc.raw+")", err, tc.wants...)
		})
	}
}

func TestParseTypeSnapshot_ReadsEachTypesMarkAndParts(t *testing.T) {
	contribution := func(field string) string {
		return `{"version": 2, "types": [{"id": "github.scaffold-contribution", "capability": "github",` +
			` "kind": "scaffold-contribution", "scope": "per-app", "version": 1` + field + `}]}`
	}
	ends := func(snap contract.TypeSnapshot) any {
		typ, _ := snap.Lookup("github.scaffold-contribution")
		return typ.EndsWithSubscription
	}
	cases := []struct {
		label, raw string
		got        func(snap contract.TypeSnapshot) any
		want       any
	}{
		{"a type without endsWithSubscription reads as unmarked", contribution(``), ends, false},
		{"a type marked endsWithSubscription false reads as unmarked", contribution(`, "endsWithSubscription": false`), ends, false},
		{"a type marked endsWithSubscription true reads as marked", contribution(`, "endsWithSubscription": true`), ends, true},
		{"each part's type and count", `{"version": 3, "types": [{"id": "aws.ec2-instance", "capability": "aws", "kind": "host", "scope": "per-app", "version": 2,` +
			` "parts": [{"type": "aws.iam-role", "count": 2}, {"type": "aws.iam-instance-profile", "count": 1}, {"type": "aws.elastic-ip", "count": 1}]}, ` +
			iamRoleType + `, {"id": "aws.iam-instance-profile", "capability": "aws", "kind": "instance-profile", "scope": "per-app", "version": 1},` +
			` {"id": "aws.elastic-ip", "capability": "aws", "kind": "ip-address", "scope": "per-app", "version": 1}]}`,
			func(snap contract.TypeSnapshot) any {
				instance, _ := snap.Lookup("aws.ec2-instance")
				return instance.Parts
			}, []contract.SnapshotPart{{Type: "aws.iam-role", Count: 2}, {Type: "aws.iam-instance-profile", Count: 1}, {Type: "aws.elastic-ip", Count: 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			snap, err := contract.ParseTypeSnapshot([]byte(tc.raw))

			if err != nil {
				t.Fatalf("ParseTypeSnapshot(%s): %v", tc.raw, err)
			}
			wantField(t, "ParseTypeSnapshot("+tc.raw+")", tc.got(snap), tc.want)
		})
	}
}
