// A region-scoped block tears down exactly what each subscribe provisioned, read from the request's instance config.
package handler_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/kittest"
)

// provisioned replays the bookkeeping a region-scoped block keeps: each Ensure adds the region its request names and each Delete removes it.
func provisioned(r *resource) []string {
	regions := map[string]bool{}
	for _, d := range r.ensured {
		if region := instanceRegion(d.Instances); region != "" {
			regions[region] = true
		}
	}
	for _, d := range r.deleted {
		delete(regions, instanceRegion(d.Instances))
	}
	out := []string{}
	for region := range regions {
		out = append(out, region)
	}
	slices.Sort(out)
	return out
}

func tenantRequest(op, org, sub, region string) map[string]any {
	return map[string]any{"operation": op, "orgId": org, "subscriptionId": sub, "instances": awsInstances(region)}
}

func TestServe_UnsubscribeTearsDownOnlyTheRegionItsOwnSubscribeProvisioned(t *testing.T) {
	r := fake(&kittest.FakeResource{ObserveState: stateOf(contract.Unknown)})
	x := newHarness(t, r)
	steps := []struct {
		fields map[string]any
		want   []string
	}{
		{tenantRequest("subscribe", "org-a", "sub-a", "eu-west-1"), []string{"eu-west-1"}},
		{tenantRequest("subscribe", "org-b", "sub-b", "us-east-1"), []string{"eu-west-1", "us-east-1"}},
		{tenantRequest("unsubscribe", "org-a", "sub-a", "eu-west-1"), []string{"us-east-1"}},
		{tenantRequest("unsubscribe", "org-b", "sub-b", "us-east-1"), []string{}},
	}

	for _, step := range steps {
		status, raw := x.post(step.fields)

		if status != http.StatusOK {
			t.Fatalf("%v: status = %d, want 200, body: %s", step.fields, status, raw)
		}
		assertConforms(t, raw)
		if got := provisioned(r); !slices.Equal(got, step.want) {
			t.Errorf("after the %s by %s, provisioned regions = %v, want %v: one org's teardown must not reach another's", step.fields["operation"], step.fields["orgId"], got, step.want)
		}
	}
	for i, want := range [][2]string{{"org-a", "eu-west-1"}, {"org-b", "us-east-1"}} {
		if len(r.deleted) != 2 || r.deleted[i].OrgID != want[0] || instanceRegion(r.deleted[i].Instances) != want[1] {
			t.Fatalf("Delete calls = %+v, want call %d from %s in %s", r.deleted, i+1, want[0], want[1])
		}
	}
}

// The fake reports absent whatever Delete did, so the answer alone cannot tell a real teardown from a false success; the region bookkeeping can.
func TestServe_UnsubscribeReportsAbsentOnlyWithTheResourceActuallyGone(t *testing.T) {
	r := fake(&kittest.FakeResource{ObserveState: stateOf(contract.Absent)})
	x := newHarness(t, r)
	if status, _ := x.post(tenantRequest("subscribe", "org-1", "sub-1", "eu-central-1")); status != http.StatusOK || len(provisioned(r)) != 1 {
		t.Fatalf("subscribe: status %d, provisioned %v; want 200 and exactly one region", status, provisioned(r))
	}

	status, resp := x.answer(tenantRequest("unsubscribe", "org-1", "sub-1", "eu-central-1"))

	if status != http.StatusOK || resp.Error != "" {
		t.Fatalf("unsubscribe: status, error = %d, %q; want 200 and none", status, resp.Error)
	}
	wantAbsent(t, resp, true)
	if got := provisioned(r); len(got) != 0 {
		t.Errorf("absent:true answered while regions %v are still provisioned", got)
	}
}
