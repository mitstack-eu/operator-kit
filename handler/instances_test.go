package handler_test

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

func awsInstances(region string) map[string]any {
	return map[string]any{
		"aws": map[string]any{
			"name":     "Consumer AWS",
			"endpoint": "https://aws.example.com",
			"config":   map[string]string{"region": region},
		},
	}
}

// instanceRegion answers "" for a nil map or a missing entry or key, so a block that provisioned nothing reads an empty region rather than panicking.
func instanceRegion(instances map[string]contract.InstanceConfig) string {
	return instances["aws"].Config["region"]
}

// regionScopedResource's Delete returns nil without deleting for an empty region, since a Delete that refused would break unsubscribe for every subscription that legitimately provisioned nothing.
type regionScopedResource struct {
	mu sync.Mutex

	observeState contract.State

	provisioned map[string]bool

	ensureDesired     []contract.Desired
	observeIdentities []contract.Identity
	deleteIdentities  []contract.Identity
}

func (r *regionScopedResource) Ensure(_ context.Context, d contract.Desired) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureDesired = append(r.ensureDesired, d)

	region := instanceRegion(d.Instances)
	if region == "" {
		return nil
	}
	if r.provisioned == nil {
		r.provisioned = map[string]bool{}
	}
	r.provisioned[region] = true
	return nil
}

func (r *regionScopedResource) Observe(_ context.Context, d contract.Desired) (contract.Observation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observeIdentities = append(r.observeIdentities, d.Identity)

	state := r.observeState
	if state == "" {
		state = contract.Unknown
	}
	return contract.Observation{State: state}, nil
}

func (r *regionScopedResource) Delete(_ context.Context, d contract.Desired) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleteIdentities = append(r.deleteIdentities, d.Identity)

	region := instanceRegion(d.Instances)
	if region == "" {
		return nil, nil
	}
	delete(r.provisioned, region)
	return nil, nil
}

func (r *regionScopedResource) regions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.provisioned))
	for region := range r.provisioned {
		out = append(out, region)
	}
	sort.Strings(out)
	return out
}

func (r *regionScopedResource) observed() []contract.Identity {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]contract.Identity(nil), r.observeIdentities...)
}

func (r *regionScopedResource) deleted() []contract.Identity {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]contract.Identity(nil), r.deleteIdentities...)
}

func (r *regionScopedResource) ensured() []contract.Desired {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]contract.Desired(nil), r.ensureDesired...)
}

func TestServe_UnsubscribeDeletesTheRegionScopedResourceSubscribeCreated(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	r := &regionScopedResource{}
	h := newHandler(t, r, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-inst-sub", map[string]any{
		"operation": "subscribe",
		"instances": awsInstances("eu-central-1"),
	}))
	if status != http.StatusOK {
		t.Fatalf("subscribe status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)
	if got := r.regions(); len(got) != 1 || got[0] != "eu-central-1" {
		t.Fatalf("provisioned regions after subscribe = %v, want [eu-central-1] — "+
			"Ensure must read the region off the request's instance config", got)
	}

	status, raw = postHook(t, h, s, envelope(t, s, "nonce-inst-unsub", map[string]any{
		"operation": "unsubscribe",
		"instances": awsInstances("eu-central-1"),
	}))
	if status != http.StatusOK {
		t.Fatalf("unsubscribe status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	if got := r.regions(); len(got) != 0 {
		t.Errorf("provisioned regions after unsubscribe = %v, want none — the resource survived teardown", got)
	}
	deleted := r.deleted()
	if len(deleted) != 1 {
		t.Fatalf("Delete called %d times, want 1", len(deleted))
	}
	if got := instanceRegion(deleted[0].Instances); got != "eu-central-1" {
		t.Errorf("Delete saw Identity.Instances[aws].Config[region] = %q, want %q — "+
			"unsubscribe must carry the request's instance config to Delete", got, "eu-central-1")
	}
}

// The fake's Observe simply reports Absent, so the response alone cannot tell a real teardown from a false success; the test also asserts the resource is gone.
func TestServe_UnsubscribeReportsAbsentOnlyWithTheResourceActuallyGone(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	r := &regionScopedResource{observeState: contract.Absent}
	h := newHandler(t, r, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-false-success-sub", map[string]any{
		"operation": "subscribe",
		"instances": awsInstances("eu-central-1"),
	}))
	if status != http.StatusOK {
		t.Fatalf("subscribe status = %d, want 200, body: %s", status, raw)
	}
	if got := r.regions(); len(got) != 1 {
		t.Fatalf("provisioned regions after subscribe = %v, want exactly one", got)
	}

	status, raw = postHook(t, h, s, envelope(t, s, "nonce-false-success-unsub", map[string]any{
		"operation": "unsubscribe",
		"instances": awsInstances("eu-central-1"),
	}))
	if status != http.StatusOK {
		t.Fatalf("unsubscribe status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Absent == nil || *resp.Absent != true {
		t.Errorf("Absent = %v, want a non-nil true — the response half of the report", resp.Absent)
	}
	if resp.Error != "" {
		t.Errorf("Error = %q, want empty", resp.Error)
	}
	if got := r.regions(); len(got) != 0 {
		t.Errorf("the response reported absent:true while regions %v are still provisioned — "+
			"teardown reported success without tearing anything down", got)
	}
}

// priorOutputsResource reads its region from PriorOutputs and never looks at an instance config; a test seeds provisioned directly since a real subscribe carries no priorOutputs.
type priorOutputsResource struct {
	mu               sync.Mutex
	provisioned      map[string]bool
	deleteIdentities []contract.Identity
}

func (r *priorOutputsResource) Ensure(_ context.Context, d contract.Desired) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	region := d.PriorOutputs["region"]
	if region == "" {
		return nil
	}
	if r.provisioned == nil {
		r.provisioned = map[string]bool{}
	}
	r.provisioned[region] = true
	return nil
}

func (r *priorOutputsResource) Observe(_ context.Context, _ contract.Desired) (contract.Observation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.provisioned) == 0 {
		return contract.Observation{State: contract.Absent}, nil
	}
	return contract.Observation{State: contract.Present}, nil
}

func (r *priorOutputsResource) Delete(_ context.Context, d contract.Desired) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleteIdentities = append(r.deleteIdentities, d.Identity)

	region := d.PriorOutputs["region"]
	if region == "" {
		return nil, nil
	}
	delete(r.provisioned, region)
	return nil, nil
}

func (r *priorOutputsResource) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.provisioned)
}

func (r *priorOutputsResource) deleted() []contract.Identity {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]contract.Identity(nil), r.deleteIdentities...)
}

func TestServe_UnsubscribeStillTearsDownABlockReadingRegionFromPriorOutputs(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	r := &priorOutputsResource{provisioned: map[string]bool{"eu-west-1": true}}
	h := newHandler(t, r, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-prior-outputs-unsub", map[string]any{
		"operation":    "unsubscribe",
		"priorOutputs": map[string]string{"region": "eu-west-1"},
	}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Absent == nil || *resp.Absent != true {
		t.Errorf("Absent = %v, want a non-nil true", resp.Absent)
	}
	if n := r.count(); n != 0 {
		t.Errorf("%d resources still provisioned, want none — a PriorOutputs-driven Delete must still work", n)
	}
	deleted := r.deleted()
	if len(deleted) != 1 {
		t.Fatalf("Delete called %d times, want 1", len(deleted))
	}
	if got := deleted[0].PriorOutputs["region"]; got != "eu-west-1" {
		t.Errorf("Delete saw Identity.PriorOutputs[region] = %q, want %q", got, "eu-west-1")
	}
	if deleted[0].Instances != nil {
		t.Errorf("Delete saw Identity.Instances = %v, want nil for a request carrying no instances", deleted[0].Instances)
	}
}

func TestServe_ObserveReceivesTheRequestsInstanceConfig(t *testing.T) {
	cases := []struct {
		name      string
		operation string
		nonce     string
	}{
		{"validate", "validate", "nonce-observe-inst-validate"},
		{"subscribe settle", "subscribe", "nonce-observe-inst-subscribe"},
		{"unsubscribe settle", "unsubscribe", "nonce-observe-inst-unsubscribe"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			r := &regionScopedResource{}
			h := newHandler(t, r, s)

			status, raw := postHook(t, h, s, envelope(t, s, tc.nonce, map[string]any{
				"operation": tc.operation,
				"instances": awsInstances("eu-central-1"),
			}))
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200, body: %s", status, raw)
			}

			observed := r.observed()
			if len(observed) == 0 {
				t.Fatalf("Observe was never called for %s", tc.operation)
			}
			for i, id := range observed {
				if got := instanceRegion(id.Instances); got != "eu-central-1" {
					t.Errorf("Observe call %d saw Identity.Instances[aws].Config[region] = %q, want %q",
						i+1, got, "eu-central-1")
				}
				if got := id.Instances["aws"].Name; got != "Consumer AWS" {
					t.Errorf("Observe call %d saw Identity.Instances[aws].Name = %q, want %q",
						i+1, got, "Consumer AWS")
				}
				if got := id.Instances["aws"].Endpoint; got != "https://aws.example.com" {
					t.Errorf("Observe call %d saw Identity.Instances[aws].Endpoint = %q, want %q",
						i+1, got, "https://aws.example.com")
				}
			}
		})
	}
}

func TestServe_EnsureStillReceivesTheRequestsInstanceConfig(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	r := &regionScopedResource{}
	h := newHandler(t, r, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-ensure-inst", map[string]any{
		"operation": "subscribe",
		"instances": awsInstances("eu-central-1"),
	}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}

	ensured := r.ensured()
	if len(ensured) != 1 {
		t.Fatalf("Ensure called %d times, want 1", len(ensured))
	}
	if got := instanceRegion(ensured[0].Instances); got != "eu-central-1" {
		t.Errorf("Ensure saw Desired.Instances[aws].Config[region] = %q, want %q", got, "eu-central-1")
	}
	if got := instanceRegion(ensured[0].Identity.Instances); got != "eu-central-1" {
		t.Errorf("Ensure saw Desired.Identity.Instances[aws].Config[region] = %q, want %q — "+
			"Desired must carry the instance config through its embedded Identity", got, "eu-central-1")
	}
}

func TestServe_DeleteHooksReceiveTheRequestsInstanceConfig(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	r := &regionScopedResource{observeState: contract.Absent}

	seen := map[string]contract.Desired{}
	capture := func(name string) handler.Hook {
		return func(_ context.Context, d contract.Desired) error {
			seen[name] = d
			return nil
		}
	}
	h := newHandler(t, r, s, func(c *handler.Config) {
		c.Manifest = manifestDeclaring(handler.HookBeforeDelete, handler.HookAfterDelete)
		c.Hooks = map[string]handler.Hook{
			handler.HookBeforeDelete: capture(handler.HookBeforeDelete),
			handler.HookAfterDelete:  capture(handler.HookAfterDelete),
		}
	})

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-hook-inst", map[string]any{
		"operation": "unsubscribe",
		"instances": awsInstances("eu-central-1"),
	}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}

	for _, name := range []string{handler.HookBeforeDelete, handler.HookAfterDelete} {
		d, ok := seen[name]
		if !ok {
			t.Errorf("hook %q was never called", name)
			continue
		}
		if got := instanceRegion(d.Instances); got != "eu-central-1" {
			t.Errorf("hook %q saw Desired.Instances[aws].Config[region] = %q, want %q", name, got, "eu-central-1")
		}
	}
}

func TestServe_UnsubscribeWithNoInstancesSucceedsWithANilInstanceMap(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	r := &regionScopedResource{observeState: contract.Absent}
	h := newHandler(t, r, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-no-inst-unsub", map[string]any{
		"operation": "unsubscribe",
	}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a subscription that provisioned nothing must still unsubscribe, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Error != "" {
		t.Errorf("Error = %q, want empty — an absent instance config is not an error", resp.Error)
	}
	if resp.Absent == nil || *resp.Absent != true {
		t.Errorf("Absent = %v, want a non-nil true", resp.Absent)
	}

	deleted := r.deleted()
	if len(deleted) != 1 {
		t.Fatalf("Delete called %d times, want 1", len(deleted))
	}
	if deleted[0].Instances != nil {
		t.Errorf("Delete saw Identity.Instances = %v, want nil for a request carrying no instances", deleted[0].Instances)
	}
	for i, id := range r.observed() {
		if id.Instances != nil {
			t.Errorf("Observe call %d saw Identity.Instances = %v, want nil", i+1, id.Instances)
		}
	}
}

func TestServe_UnsubscribeIsolatesEachOrgsRegion(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	r := &regionScopedResource{}
	h := newHandler(t, r, s)

	tenants := []struct {
		org, sub, region string
	}{
		{"org-a", "sub-a", "eu-west-1"},
		{"org-b", "sub-b", "us-east-1"},
	}

	for _, tn := range tenants {
		status, raw := postHook(t, h, s, envelope(t, s, "nonce-tenant-sub-"+tn.org, map[string]any{
			"operation":      "subscribe",
			"orgId":          tn.org,
			"subscriptionId": tn.sub,
			"instances":      awsInstances(tn.region),
		}))
		if status != http.StatusOK {
			t.Fatalf("%s subscribe status = %d, want 200, body: %s", tn.org, status, raw)
		}
	}
	if got := r.regions(); len(got) != 2 || got[0] != "eu-west-1" || got[1] != "us-east-1" {
		t.Fatalf("provisioned regions = %v, want [eu-west-1 us-east-1]", got)
	}

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-tenant-unsub-a", map[string]any{
		"operation":      "unsubscribe",
		"orgId":          tenants[0].org,
		"subscriptionId": tenants[0].sub,
		"instances":      awsInstances(tenants[0].region),
	}))
	if status != http.StatusOK {
		t.Fatalf("org-a unsubscribe status = %d, want 200, body: %s", status, raw)
	}
	if got := r.regions(); len(got) != 1 || got[0] != "us-east-1" {
		t.Errorf("provisioned regions after org-a unsubscribe = %v, want [us-east-1] — "+
			"one org's teardown must not reach another org's region", got)
	}

	status, raw = postHook(t, h, s, envelope(t, s, "nonce-tenant-unsub-b", map[string]any{
		"operation":      "unsubscribe",
		"orgId":          tenants[1].org,
		"subscriptionId": tenants[1].sub,
		"instances":      awsInstances(tenants[1].region),
	}))
	if status != http.StatusOK {
		t.Fatalf("org-b unsubscribe status = %d, want 200, body: %s", status, raw)
	}
	if got := r.regions(); len(got) != 0 {
		t.Errorf("provisioned regions after both unsubscribes = %v, want none", got)
	}

	deleted := r.deleted()
	if len(deleted) != 2 {
		t.Fatalf("Delete called %d times, want 2", len(deleted))
	}
	for i, tn := range tenants {
		if got := deleted[i].OrgID; got != tn.org {
			t.Errorf("Delete call %d saw OrgID = %q, want %q", i+1, got, tn.org)
		}
		if got := instanceRegion(deleted[i].Instances); got != tn.region {
			t.Errorf("Delete call %d (%s) saw region %q, want %q", i+1, tn.org, got, tn.region)
		}
	}
}
