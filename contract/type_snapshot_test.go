package contract_test

// type_snapshot_test.go pins contract.ParseTypeSnapshot, which reads the
// versioned snapshot of resource types the platform serves at
// GET /v1/resource-types/snapshots/{version}, the shape the fixture under
// testdata holds: a snapshot of the platform's 51 type files. A snapshot is
// refused when it is not JSON, holds no type, or holds one type id twice.

import (
	"slices"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/contract"
)

func TestParseTypeSnapshot_ReadsThePlatformsSnapshot(t *testing.T) {
	snap := platformSnapshot(t)
	if snap.Version != 1 {
		t.Errorf("Version = %d, want 1", snap.Version)
	}
	if len(snap.Types) != 51 {
		t.Errorf("Types holds %d types, want the platform's 51", len(snap.Types))
	}

	instance, ok := snap.Lookup("aws.ec2-instance")
	if !ok {
		t.Fatal(`Lookup("aws.ec2-instance") found nothing`)
	}
	if instance.ID != "aws.ec2-instance" || instance.Capability != "aws" || instance.Kind != "host" ||
		instance.Scope != "per-app" || instance.Stateful || instance.Version != 1 {
		t.Errorf("aws.ec2-instance = %+v, want id aws.ec2-instance, capability aws, kind host, scope per-app, not stateful, version 1", instance)
	}
	hostID, ok := instance.Output("hostId")
	if !ok || hostID != (contract.SnapshotOutput{Name: "hostId", Required: true}) {
		t.Errorf(`aws.ec2-instance Output("hostId") = %+v, %v; want a required plain output`, hostID, ok)
	}
	if region, ok := instance.Output("cloud.region"); !ok || region.Required || region.Secret {
		t.Errorf(`aws.ec2-instance Output("cloud.region") = %+v, %v; want an optional plain output`, region, ok)
	}
	if _, ok := instance.Output("dbName"); ok {
		t.Error(`aws.ec2-instance Output("dbName") found an output the type does not declare`)
	}

	role, _ := snap.Lookup("aws.iam-role")
	if arn, ok := role.Output("roleArn"); !ok || !arn.Secret {
		t.Errorf(`aws.iam-role Output("roleArn") = %+v, %v; want a secret output`, arn, ok)
	}
	volume, _ := snap.Lookup("aws.ebs-volume")
	if !volume.Stateful || volume.Category != "storage" || volume.Pricing.Priced {
		t.Errorf("aws.ebs-volume = %+v, want stateful, category storage, not priced", volume)
	}
	if volume.TimeBudgets["observe"] != "30s" || len(volume.Failures) == 0 || volume.Failures[0].Class != "transient" {
		t.Errorf("aws.ebs-volume time budgets %v, failures %+v; want observe 30s and its failure list", volume.TimeBudgets, volume.Failures)
	}
	if !strings.Contains(volume.Observe, "DescribeVolumes") || !strings.Contains(volume.Absence, "NotFound") {
		t.Errorf("aws.ebs-volume observe %q, absence %q; want the type's prose", volume.Observe, volume.Absence)
	}
	cluster, _ := snap.Lookup("hetzner.k3s-cluster")
	if cluster.Hosts != "kubernetes" {
		t.Errorf("hetzner.k3s-cluster hosts = %q, want kubernetes", cluster.Hosts)
	}
	runner, _ := snap.Lookup("ubicloud.runner")
	if !slices.Equal(runner.ReliesOn, []string{"github"}) {
		t.Errorf("ubicloud.runner reliesOn = %q, want [github]", runner.ReliesOn)
	}

	if _, ok := snap.Lookup("host.container"); ok {
		t.Error(`Lookup("host.container") found a type ADR 0085 retires`)
	}
}

func TestParseTypeSnapshot_RefusesASnapshotItCannotUse(t *testing.T) {
	cases := []struct {
		label, raw string
		wants      []string
	}{
		{"not JSON", `{"version": 1, "types": [`, nil},
		{"no types", `{"version": 1, "types": []}`, []string{"type"}},
		{"no types key", `{"version": 1}`, []string{"type"}},
		{"one type id twice",
			`{"version": 1, "types": [{"id": "aws.vpc", "capability": "aws", "kind": "network", "scope": "per-app", "version": 1},` +
				` {"id": "aws.vpc", "capability": "aws", "kind": "network", "scope": "per-app", "version": 2}]}`,
			[]string{"aws.vpc"}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			snap, err := contract.ParseTypeSnapshot([]byte(tc.raw))
			if err == nil {
				t.Fatalf("ParseTypeSnapshot accepted %s as %+v, want it refused", tc.raw, snap)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("ParseTypeSnapshot error = %q, want it to name %q", err, want)
				}
			}
		})
	}
}

func TestParseTypeSnapshot_ReadsATypesEndsWithSubscriptionMark(t *testing.T) {
	cases := []struct {
		label, field string
		want         bool
	}{
		{"a type without the field reads as unmarked", ``, false},
		{"a type marked false reads as unmarked", `, "endsWithSubscription": false`, false},
		{"a type marked true reads as marked", `, "endsWithSubscription": true`, true},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			raw := `{"version": 2, "types": [{"id": "github.scaffold-contribution", "capability": "github",` +
				` "kind": "scaffold-contribution", "scope": "per-app", "version": 1` + tc.field + `}]}`

			snap, err := contract.ParseTypeSnapshot([]byte(raw))

			if err != nil {
				t.Fatalf("ParseTypeSnapshot(%s): %v", raw, err)
			}
			typ, _ := snap.Lookup("github.scaffold-contribution")
			if typ.EndsWithSubscription != tc.want {
				t.Errorf("ParseTypeSnapshot(%s) EndsWithSubscription = %v; want %v", raw, typ.EndsWithSubscription, tc.want)
			}
		})
	}
}

func TestParseTypeSnapshot_ThePlatformsSnapshotWithoutTheFieldMarksNoType(t *testing.T) {
	snap := platformSnapshot(t)

	var marked []string
	for _, typ := range snap.Types {
		if typ.EndsWithSubscription {
			marked = append(marked, typ.ID)
		}
	}

	if len(marked) != 0 {
		t.Errorf("types marked endsWithSubscription = %q; want none, since the fixture carries no such field", marked)
	}
}
