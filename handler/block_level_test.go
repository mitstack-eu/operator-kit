package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

const blockLevelManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: block-level
  name: Block Level
spec:
  version: 1.0.0
  resources:
    - aws.vpc
    - aws.ec2-instance
    - { name: a, type: aws.iam-role }
    - { name: b, type: aws.iam-role }
  provides: [aws.ec2-instance, a]
  lifecycle:
    endpoint: https://block-level.example/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`

var declaredTypes = map[string]string{
	"aws.vpc":          "aws.vpc",
	"aws.ec2-instance": "aws.ec2-instance",
	"a":                "aws.iam-role",
	"b":                "aws.iam-role",
}

var blockOutputs = map[string]map[string]string{
	"aws.vpc":          {"vpcId": "vpc-0abc"},
	"aws.ec2-instance": {"publicIp": "203.0.113.7"},
	"a":                {"aOut": "from-a"},
	"b":                {"bOut": "from-b"},
}

type fakeCloud struct {
	mu                sync.Mutex
	present           map[string]bool
	neverPresent      map[string]bool
	standsAfterDelete map[string]bool
}

func newFakeCloud() *fakeCloud {
	return &fakeCloud{present: map[string]bool{}, neverPresent: map[string]bool{}, standsAfterDelete: map[string]bool{}}
}

func (c *fakeCloud) createAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name := range declaredTypes {
		if !c.neverPresent[name] {
			c.present[name] = true
		}
	}
}

func (c *fakeCloud) deleteAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name := range declaredTypes {
		if !c.standsAfterDelete[name] {
			delete(c.present, name)
		}
	}
}

func (c *fakeCloud) stands(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.present[name]
}

// blockResource returns deleteOutputs from each Delete and keeps the request each Delete is handed.
type blockResource struct {
	cloud                                  *fakeCloud
	ensureCalls, observeCalls, deleteCalls int
	deleteOutputs                          map[string]string
	deleted                                []contract.Desired
}

func (b *blockResource) Ensure(ctx context.Context, d contract.Desired) error {
	b.ensureCalls++
	b.cloud.createAll()
	return nil
}

func (b *blockResource) Observe(ctx context.Context, d contract.Desired) (contract.Observation, error) {
	b.observeCalls++
	return contract.Observation{State: contract.Unknown}, nil
}

func (b *blockResource) Delete(ctx context.Context, d contract.Desired) (map[string]string, error) {
	b.deleteCalls++
	b.deleted = append(b.deleted, d)
	b.cloud.deleteAll()
	return b.deleteOutputs, nil
}

type reportingBlock struct {
	*blockResource
	report []contract.CreatedResource
}

func (r reportingBlock) EnsureResources(ctx context.Context, d contract.Desired) ([]contract.CreatedResource, error) {
	if err := r.Ensure(ctx, d); err != nil {
		return nil, err
	}
	return r.report, nil
}

type planningBlock struct {
	*blockResource
	replaces []string
}

func (p planningBlock) PlanReplacements(ctx context.Context, d contract.Desired) ([]string, error) {
	return p.replaces, nil
}

var (
	_ handler.ResourceReporter   = reportingBlock{}
	_ handler.ReplacementPlanner = planningBlock{}
	_ handler.Observer           = (*fakeObserver)(nil)
)

type fakeObserver struct {
	name  string
	cloud *fakeCloud
	mu    sync.Mutex
	refs  []contract.ResourceRef
}

func (o *fakeObserver) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	o.mu.Lock()
	o.refs = append(o.refs, r)
	o.mu.Unlock()
	if !o.cloud.stands(o.name) {
		return contract.Observation{State: contract.Absent}, nil
	}
	return contract.Observation{State: contract.Present, Outputs: blockOutputs[o.name]}, nil
}

func (o *fakeObserver) calls() []contract.ResourceRef {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]contract.ResourceRef(nil), o.refs...)
}

func blockObservers(cloud *fakeCloud) map[string]*fakeObserver {
	out := map[string]*fakeObserver{}
	for name := range declaredTypes {
		out[name] = &fakeObserver{name: name, cloud: cloud}
	}
	return out
}

func asObservers(obs map[string]*fakeObserver) map[string]handler.Observer {
	out := make(map[string]handler.Observer, len(obs))
	for name, o := range obs {
		out[name] = o
	}
	return out
}

func blockLevelConfig(t *testing.T, s *kittest.Signer, r handler.Resource, obs map[string]handler.Observer) handler.Config {
	t.Helper()
	cfg := baseConfig(t, r, s)
	cfg.Manifest = []byte(blockLevelManifest)
	cfg.Observers = obs
	return cfg
}

func serveBlockLevel(t *testing.T, r handler.Resource, obs map[string]*fakeObserver, operation string, extra map[string]any, mutate ...func(*handler.Config)) (int, contract.Response) {
	t.Helper()
	s := kittest.NewSigner(t, "kid-block-level", 17)
	cfg := blockLevelConfig(t, s, r, asObservers(obs))
	for _, m := range mutate {
		m(&cfg)
	}
	h, err := handler.New(cfg)
	if err != nil {
		t.Fatalf("handler.New: %v", err)
	}
	fields := map[string]any{"operation": operation}
	for k, v := range extra {
		fields[k] = v
	}
	status, raw := postHook(t, h, s, envelope(t, s, "nonce-block-level-"+operation, fields))
	assertConforms(t, raw)
	return status, decodeResponse(t, raw)
}

func recordedAll() map[string]any {
	return map[string]any{
		"aws.vpc":          map[string]any{"type": "aws.vpc", "identity": map[string]any{"vpcId": "vpc-0abc"}},
		"aws.ec2-instance": map[string]any{"type": "aws.ec2-instance", "identity": map[string]any{"instanceId": "i-0abc"}},
		"a":                map[string]any{"type": "aws.iam-role", "identity": map[string]any{"roleName": "a-role"}},
		"b":                map[string]any{"type": "aws.iam-role", "identity": map[string]any{"roleName": "b-role"}},
	}
}

func wantEveryResourceObserved(t *testing.T, resp contract.Response, state contract.State) {
	t.Helper()
	got := make([]string, 0, len(resp.Resources))
	for name := range resp.Resources {
		got = append(got, name)
	}
	slices.Sort(got)
	if want := []string{"a", "aws.ec2-instance", "aws.vpc", "b"}; !slices.Equal(got, want) {
		t.Errorf("resources = %q, want one entry per declared resource %q", got, want)
	}
	for name, typ := range declaredTypes {
		o, ok := resp.Resources[name]
		if !ok || o.Observe == nil {
			t.Errorf("resource %q has no observe outcome; resources: %+v", name, resp.Resources)
			continue
		}
		if o.Observe.State != state || o.Observe.Type != typ || o.Observe.Error != "" {
			t.Errorf("resource %q observe = %+v, want state %s, type %s and no error", name, *o.Observe, state, typ)
		}
	}
}

// A capability that implements no issue handler is served as a block is.
func TestBlockLevel_SubscribeAndUpgradeRunOneEnsureThenObserveEachResourceByName(t *testing.T) {
	for _, tc := range []struct{ name, manifest, op string }{
		{"block subscribe", blockLevelManifest, "subscribe"}, {"block upgrade", blockLevelManifest, "upgrade"},
		{"capability subscribe", capabilityManifest, "subscribe"}, {"capability upgrade", capabilityManifest, "upgrade"},
	} {
		op := tc.op
		t.Run(tc.name, func(t *testing.T) {
			cloud := newFakeCloud()
			block := &blockResource{cloud: cloud}
			obs := blockObservers(cloud)

			status, resp := serveBlockLevel(t, block, obs, op, nil, withManifest(tc.manifest))
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200; response %+v", status, resp)
			}
			if block.ensureCalls != 1 || block.deleteCalls != 0 {
				t.Errorf("block Ensure, Delete calls = %d, %d; want one Ensure for the whole block and no Delete", block.ensureCalls, block.deleteCalls)
			}
			if resp.Pending {
				t.Errorf("pending = true (%s), want false: every resource stands", resp.StatusDetail)
			}
			wantEveryResourceObserved(t, resp, contract.Present)
			for name, o := range obs {
				refs := o.calls()
				if len(refs) == 0 {
					t.Errorf("resource %q was never observed through its own observer", name)
					continue
				}
				if r := refs[0]; r.Name != name || r.Type != declaredTypes[name] || r.Desired.SubscriptionID != "sub-1" {
					t.Errorf("resource %q observer handed name %q, type %q, subscription %q; want %q, %q, sub-1",
						name, r.Name, r.Type, r.Desired.SubscriptionID, name, declaredTypes[name])
				}
			}
			want := map[string]string{"publicIp": "203.0.113.7", "aOut": "from-a"}
			if !reflect.DeepEqual(resp.Outputs, want) {
				t.Errorf("outputs = %v, want %v: the union of the provided resources' outputs alone", resp.Outputs, want)
			}
		})
	}
}

func TestBlockLevel_SubscribeAnswersPendingNamingAResourceNotPresentWithinTheBudget(t *testing.T) {
	cloud := newFakeCloud()
	cloud.neverPresent["b"] = true
	obs := blockObservers(cloud)

	status, resp := serveBlockLevel(t, &blockResource{cloud: cloud}, obs, "subscribe", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: pending is never an error", status)
	}
	if !resp.Pending || !strings.Contains(resp.StatusDetail, `"b"`) {
		t.Errorf("pending, statusDetail = %v, %q; want pending naming resource \"b\"", resp.Pending, resp.StatusDetail)
	}
	if got := len(obs["b"].calls()); got != 3 {
		t.Errorf("b observed %d times, want the verb budget of 3", got)
	}
}

// Each observe is handed the reported identity and type, else the recorded ones, else no identity and the entry's one type.
func TestBlockLevel_EachObserveIsHandedTheIdentityAndTypeOfItsResource(t *testing.T) {
	reported := []contract.CreatedResource{
		{Name: "aws.vpc", Type: "aws.vpc", Identity: map[string]string{"vpcId": "vpc-new"}},
		{Name: "aws.ec2-instance", Type: "aws.ec2-instance", Identity: map[string]string{"instanceId": "i-new"}},
		{Name: "a", Type: "aws.iam-role", Identity: map[string]string{"roleName": "a-new"}},
		{Name: "b", Type: "aws.iam-role", Identity: map[string]string{"roleName": "b-new"}},
	}
	cases := []struct {
		label     string
		block     func(cloud *fakeCloud) handler.Resource
		extra     map[string]any
		wantB     map[string]string
		wantEntry bool
	}{
		{"reported by the block", func(c *fakeCloud) handler.Resource {
			return reportingBlock{blockResource: &blockResource{cloud: c}, report: reported}
		}, map[string]any{"recordedResources": recordedAll()}, map[string]string{"roleName": "b-new"}, true},
		{"recorded by the engine", func(c *fakeCloud) handler.Resource {
			return &blockResource{cloud: c}
		}, map[string]any{"recordedResources": recordedAll()}, map[string]string{"roleName": "b-role"}, true},
		{"neither", func(c *fakeCloud) handler.Resource {
			return &blockResource{cloud: c}
		}, nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			cloud := newFakeCloud()
			obs := blockObservers(cloud)
			status, resp := serveBlockLevel(t, tc.block(cloud), obs, "upgrade", tc.extra)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200; response %+v", status, resp)
			}
			refs := obs["b"].calls()
			if len(refs) == 0 {
				t.Fatal("b was never observed")
			}
			if r := refs[0]; r.Type != "aws.iam-role" || !(reflect.DeepEqual(r.Identity, tc.wantB) || len(r.Identity)+len(tc.wantB) == 0) {
				t.Errorf("b observer handed type %q, identity %v; want aws.iam-role, %v", r.Type, r.Identity, tc.wantB)
			}
			if o := resp.Resources["b"].Observe; tc.wantEntry && (o == nil || !reflect.DeepEqual(o.Identity, tc.wantB)) {
				t.Errorf("b observe entry = %+v, want identity %v", o, tc.wantB)
			}
		})
	}
}

func TestBlockLevel_RefusesAReportedResourceItsEntryDoesNotDeclare(t *testing.T) {
	cases := []struct {
		label    string
		reported contract.CreatedResource
		wants    []string
	}{
		{"a name the manifest does not declare", contract.CreatedResource{Name: "c", Type: "aws.iam-role"}, []string{`"c"`}},
		{"a type its entry does not list", contract.CreatedResource{Name: "b", Type: "aws.vpc"}, []string{`"b"`, "aws.vpc"}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			cloud := newFakeCloud()
			block := reportingBlock{blockResource: &blockResource{cloud: cloud}, report: []contract.CreatedResource{tc.reported}}
			status, resp := serveBlockLevel(t, block, blockObservers(cloud), "subscribe", nil)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; response %+v", status, resp)
			}
			for _, want := range tc.wants {
				if !strings.Contains(resp.Error, want) {
					t.Errorf("error = %q, want it to name %s", resp.Error, want)
				}
			}
		})
	}
}

func TestBlockLevel_UnsubscribeRunsOneDeleteThenProvesEachResourceAbsentWithItsRecordedIdentity(t *testing.T) {
	cloud := newFakeCloud()
	cloud.createAll()
	block := &blockResource{cloud: cloud}
	obs := blockObservers(cloud)

	status, resp := serveBlockLevel(t, block, obs, "unsubscribe", map[string]any{"recordedResources": recordedAll()})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	if block.deleteCalls != 1 || block.ensureCalls != 0 {
		t.Errorf("block Delete, Ensure calls = %d, %d; want one Delete for the whole block and no Ensure", block.deleteCalls, block.ensureCalls)
	}
	if resp.Absent == nil || !*resp.Absent {
		t.Errorf("absent = %v, want true: every resource is gone", resp.Absent)
	}
	wantEveryResourceObserved(t, resp, contract.Absent)
	refs := obs["b"].calls()
	if len(refs) == 0 || !reflect.DeepEqual(refs[0].Identity, map[string]string{"roleName": "b-role"}) || refs[0].Type != "aws.iam-role" {
		t.Errorf("b observer handed %+v, want its recorded identity and type", refs)
	}
}

func TestBlockLevel_UnsubscribeAnswersAbsentFalseNamingAResourceStillStanding(t *testing.T) {
	cloud := newFakeCloud()
	cloud.createAll()
	cloud.standsAfterDelete["b"] = true

	status, resp := serveBlockLevel(t, &blockResource{cloud: cloud}, blockObservers(cloud), "unsubscribe",
		map[string]any{"recordedResources": recordedAll()})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if resp.Absent == nil || *resp.Absent {
		t.Errorf("absent = %v, want false: b still stands", resp.Absent)
	}
	if !strings.Contains(resp.StatusDetail, `"b"`) {
		t.Errorf("statusDetail = %q, want it to name resource \"b\"", resp.StatusDetail)
	}
}

func TestBlockLevel_ValidateObservesEachResourceOnceAndNamesTheReplacementsTheBlockPlans(t *testing.T) {
	cloud := newFakeCloud()
	cloud.createAll()
	block := planningBlock{blockResource: &blockResource{cloud: cloud}, replaces: []string{"b"}}
	obs := blockObservers(cloud)

	status, resp := serveBlockLevel(t, block, obs, "validate", map[string]any{"recordedResources": recordedAll()})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	for name, o := range obs {
		if got := len(o.calls()); got != 1 {
			t.Errorf("resource %q observed %d times, want once", name, got)
		}
	}
	if block.ensureCalls != 0 || block.deleteCalls != 0 {
		t.Errorf("validate ran Ensure %d and Delete %d times, want neither", block.ensureCalls, block.deleteCalls)
	}
	wantEveryResourceObserved(t, resp, contract.Present)
	if !slices.Equal(resp.Replaces, []string{"b"}) {
		t.Errorf("replaces = %q, want [b]", resp.Replaces)
	}
}

func TestBlockLevel_ValidateRefusesAPlannedReplacementTheManifestDoesNotDeclare(t *testing.T) {
	cloud := newFakeCloud()
	cloud.createAll()
	block := planningBlock{blockResource: &blockResource{cloud: cloud}, replaces: []string{"c"}}

	status, resp := serveBlockLevel(t, block, blockObservers(cloud), "validate", nil)
	if status != http.StatusBadRequest || !strings.Contains(resp.Error, `"c"`) {
		t.Errorf("status, error = %d, %q; want 400 naming \"c\"", status, resp.Error)
	}
}

func TestBlockLevel_ValidateNamingOneResourceObservesItAloneWithTheRequestsIdentityAndType(t *testing.T) {
	cloud := newFakeCloud()
	cloud.createAll()
	block := &blockResource{cloud: cloud}
	obs := blockObservers(cloud)
	identity := map[string]string{"roleName": "b-role"}

	status, resp := serveBlockLevel(t, block, obs, "validate", map[string]any{
		"resource":         "b",
		"resourceIdentity": map[string]any{"roleName": "b-role"},
		"resourceType":     "aws.iam-role",
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	refs := obs["b"].calls()
	if len(refs) != 1 {
		t.Fatalf("b observed %d times, want once", len(refs))
	}
	if r := refs[0]; r.Name != "b" || r.Type != "aws.iam-role" || !reflect.DeepEqual(r.Identity, identity) {
		t.Errorf("b observer handed name %q, type %q, identity %v; want b, aws.iam-role, %v", r.Name, r.Type, r.Identity, identity)
	}
	for name, o := range obs {
		if name != "b" && len(o.calls()) != 0 {
			t.Errorf("resource %q was observed, but the request names b alone", name)
		}
	}
	if block.ensureCalls+block.observeCalls+block.deleteCalls != 0 {
		t.Errorf("the block-level handler ran Ensure %d, Observe %d, Delete %d times; want none for an observe of one resource",
			block.ensureCalls, block.observeCalls, block.deleteCalls)
	}
	if len(resp.Resources) != 1 {
		t.Errorf("resources = %+v, want b's entry alone", resp.Resources)
	}
	o := resp.Resources["b"].Observe
	if o == nil || o.State != contract.Present || o.Type != "aws.iam-role" || !reflect.DeepEqual(o.Identity, identity) {
		t.Errorf("b observe entry = %+v, want present as aws.iam-role with identity %v", o, identity)
	}
}

// The engine observes by the type it recorded, which an older manifest may list and the current one not, so the requested type reaches the observer rather than a refusal.
func TestBlockLevel_ValidateNamingOneResourceHandsTheObserverARequestedTypeItsEntryDoesNotList(t *testing.T) {
	cloud := newFakeCloud()
	cloud.createAll()
	obs := blockObservers(cloud)
	status, resp := serveBlockLevel(t, &blockResource{cloud: cloud}, obs, "validate", map[string]any{
		"resource":     "b",
		"resourceType": "aws.vpc",
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: an observe by recorded type is served; response %+v", status, resp)
	}
	refs := obs["b"].calls()
	if len(refs) != 1 || refs[0].Type != "aws.vpc" {
		t.Errorf("b observer handed %+v, want one observe as aws.vpc", refs)
	}
	if o := resp.Resources["b"].Observe; o == nil || o.Type != "aws.vpc" {
		t.Errorf("b observe entry = %+v, want type aws.vpc", o)
	}
}

func TestBlockLevel_UnsubscribeHandsTheObserverARecordedTypeItsEntryDoesNotList(t *testing.T) {
	cloud := newFakeCloud()
	cloud.createAll()
	obs := blockObservers(cloud)
	recorded := recordedAll()
	recorded["b"] = map[string]any{"type": "aws.vpc", "identity": map[string]any{"vpcId": "vpc-old"}}
	status, resp := serveBlockLevel(t, &blockResource{cloud: cloud}, obs, "unsubscribe", map[string]any{"recordedResources": recorded})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	refs := obs["b"].calls()
	if len(refs) == 0 || refs[0].Type != "aws.vpc" {
		t.Errorf("b observer handed %+v, want its recorded type aws.vpc", refs)
	}
}

type tickingClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *tickingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *tickingClock) advance() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Second)
	return c.now
}

type clockedBlock struct {
	*blockResource
	clock                *tickingClock
	ensuredAt, deletedAt time.Time
}

func (b *clockedBlock) Ensure(ctx context.Context, d contract.Desired) error {
	err := b.blockResource.Ensure(ctx, d)
	b.ensuredAt = b.clock.advance()
	return err
}

func (b *clockedBlock) Delete(ctx context.Context, d contract.Desired) (map[string]string, error) {
	outputs, err := b.blockResource.Delete(ctx, d)
	b.deletedAt = b.clock.advance()
	return outputs, err
}

// Each observe is handed the clock's reading once the block-level call returned, since it settles that verb.
func TestBlockLevel_ObservesAreHandedTheTimeTheBlockLevelCallReturned(t *testing.T) {
	for _, op := range []string{"subscribe", "upgrade", "unsubscribe"} {
		t.Run(op, func(t *testing.T) {
			cloud := newFakeCloud()
			if op == "unsubscribe" {
				cloud.createAll()
			}
			clock := &tickingClock{now: time.Now()}
			block := &clockedBlock{blockResource: &blockResource{cloud: cloud}, clock: clock}
			obs := blockObservers(cloud)
			status, resp := serveBlockLevel(t, block, obs, op, map[string]any{"recordedResources": recordedAll()},
				func(c *handler.Config) { c.Now = clock.Now })
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200; response %+v", status, resp)
			}
			want := block.ensuredAt
			if op == "unsubscribe" {
				want = block.deletedAt
			}
			if want.IsZero() {
				t.Fatalf("the block-level call for %s never ran", op)
			}
			for name, o := range obs {
				refs := o.calls()
				if len(refs) == 0 || !refs[0].Desired.VerbAt.Equal(want) {
					t.Errorf("resource %q observer handed %+v, want VerbAt %v, the clock once the block-level call returned", name, refs, want)
				}
			}
		})
	}
}

func TestBlockLevel_ValidateHandsTheObserverTheVerbItSettles(t *testing.T) {
	cloud := newFakeCloud()
	cloud.createAll()
	obs := blockObservers(cloud)
	settles := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	status, resp := serveBlockLevel(t, &blockResource{cloud: cloud}, obs, "validate", map[string]any{
		"recordedResources": recordedAll(),
		"settlesAt":         settles.Format(time.RFC3339),
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	for name, o := range obs {
		refs := o.calls()
		if len(refs) == 0 || !refs[0].Desired.VerbAt.Equal(settles) {
			t.Errorf("resource %q observer handed %+v, want VerbAt %v from settlesAt", name, refs, settles)
		}
	}
}

func TestBlockLevel_RefusesANamedSubscribeUpgradeOrUnsubscribe(t *testing.T) {
	for _, op := range []string{"subscribe", "upgrade", "unsubscribe"} {
		t.Run(op, func(t *testing.T) {
			cloud := newFakeCloud()
			block := &blockResource{cloud: cloud}
			status, resp := serveBlockLevel(t, block, blockObservers(cloud), op, map[string]any{"resource": "b"})
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; response %+v", status, resp)
			}
			if !strings.Contains(resp.Error, "block-level handler") {
				t.Errorf("error = %q, want it to say the block creates and deletes its resources through its block-level handler", resp.Error)
			}
			if !strings.Contains(resp.Error, `"b"`) {
				t.Errorf("error = %q, want it to name the resource \"b\"", resp.Error)
			}
			if block.ensureCalls+block.deleteCalls != 0 {
				t.Errorf("a refused %s ran Ensure %d and Delete %d times", op, block.ensureCalls, block.deleteCalls)
			}
		})
	}
}

func TestBlockLevel_RefusesAnUndeclaredNameAsUndeclaredResource(t *testing.T) {
	cloud := newFakeCloud()
	status, resp := serveBlockLevel(t, &blockResource{cloud: cloud}, blockObservers(cloud), "validate", map[string]any{"resource": "c"})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if got := handler.RefusedClass(resp.StatusDetail); got != "undeclared-resource" {
		t.Errorf("refusal class = %q, want undeclared-resource", got)
	}
}

const onlyAManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: only-a
  name: Only A
spec:
  version: 1.0.0
  resources:
    - { name: a, type: aws.iam-role }
  provides: [a]
  lifecycle:
    endpoint: https://only-a.example/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`

type typeObserver struct {
	state contract.State
	mu    sync.Mutex
	refs  []contract.ResourceRef
}

func (o *typeObserver) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	o.mu.Lock()
	o.refs = append(o.refs, r)
	o.mu.Unlock()
	return contract.Observation{State: o.state}, nil
}

func (o *typeObserver) calls() []contract.ResourceRef {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]contract.ResourceRef(nil), o.refs...)
}

func serveTypeObserved(t *testing.T, block *blockResource, vpc *typeObserver, operation string, extra map[string]any, mutate ...func(*handler.Config)) (int, contract.Response) {
	t.Helper()
	cloud := newFakeCloud()
	aObs := &fakeObserver{name: "a", cloud: cloud}
	return serveBlockLevel(t, block, map[string]*fakeObserver{"a": aObs}, operation, extra, append([]func(*handler.Config){func(cfg *handler.Config) {
		cfg.Manifest = []byte(onlyAManifest)
		if vpc != nil {
			cfg.TypeObservers = map[string]handler.Observer{"aws.vpc": vpc}
		}
	}}, mutate...)...)
}

func TestTypeObservers_ValidateNamingAResourceTheManifestNoLongerDeclaresObservesItByRecordedType(t *testing.T) {
	identity := map[string]string{"vpcId": "vpc-0abc"}
	for _, state := range []contract.State{contract.Absent, contract.Present} {
		t.Run(string(state), func(t *testing.T) {
			block := &blockResource{cloud: newFakeCloud()}
			vpc := &typeObserver{state: state}

			status, resp := serveTypeObserved(t, block, vpc, "validate", map[string]any{
				"resource":         "b",
				"resourceType":     "aws.vpc",
				"resourceIdentity": map[string]any{"vpcId": "vpc-0abc"},
			})

			if status != http.StatusOK {
				t.Fatalf("validate naming b of type aws.vpc, observer reporting %s: status = %d, want 200; response %+v", state, status, resp)
			}
			refs := vpc.calls()
			if len(refs) != 1 {
				t.Fatalf("aws.vpc observer called %d times, want once", len(refs))
			}
			if r := refs[0]; r.Name != "b" || r.Type != "aws.vpc" || !reflect.DeepEqual(r.Identity, identity) {
				t.Errorf("observer handed name %q, type %q, identity %v; want b, aws.vpc, %v", r.Name, r.Type, r.Identity, identity)
			}
			o := resp.Resources["b"].Observe
			if o == nil || o.State != state {
				t.Errorf("b observe entry = %+v, want state %s", o, state)
			}
			if len(resp.Resources) != 1 {
				t.Errorf("resources = %+v, want b's entry alone", resp.Resources)
			}
			if block.ensureCalls+block.observeCalls+block.deleteCalls != 0 {
				t.Errorf("block-level handler ran Ensure %d, Observe %d, Delete %d times; want none",
					block.ensureCalls, block.observeCalls, block.deleteCalls)
			}
		})
	}
}

func TestTypeObservers_RefuseSubscribeUpgradeAndUnsubscribeNamingAnUndeclaredResource(t *testing.T) {
	for _, op := range []string{"subscribe", "upgrade", "unsubscribe"} {
		t.Run(op, func(t *testing.T) {
			block := &blockResource{cloud: newFakeCloud()}
			vpc := &typeObserver{state: contract.Present}

			status, resp := serveTypeObserved(t, block, vpc, op, map[string]any{"resource": "b", "resourceType": "aws.vpc"})

			if status != http.StatusBadRequest {
				t.Fatalf("%s naming b of type aws.vpc: status = %d, want 400; response %+v", op, status, resp)
			}
			if got := handler.RefusedClass(resp.StatusDetail); got != "undeclared-resource" {
				t.Errorf("refusal class = %q, want undeclared-resource", got)
			}
			if n := len(vpc.calls()); n != 0 {
				t.Errorf("aws.vpc observer called %d times, want none", n)
			}
			if block.ensureCalls+block.observeCalls+block.deleteCalls != 0 {
				t.Errorf("block-level handler ran Ensure %d, Observe %d, Delete %d times; want none",
					block.ensureCalls, block.observeCalls, block.deleteCalls)
			}
		})
	}
}

func TestTypeObservers_ValidateNamingAnUndeclaredResourceWithNoHeldTypeIsRefusedNamingResourceAndType(t *testing.T) {
	cases := []struct {
		label string
		extra map[string]any
		wants []string
	}{
		{"no type", map[string]any{"resource": "b"}, []string{`"b"`}},
		{"a type no observer holds", map[string]any{"resource": "b", "resourceType": "aws.subnet"}, []string{`"b"`, "aws.subnet"}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			vpc := &typeObserver{state: contract.Present}

			status, resp := serveTypeObserved(t, &blockResource{cloud: newFakeCloud()}, vpc, "validate", tc.extra)

			if status != http.StatusBadRequest {
				t.Fatalf("validate with %v: status = %d, want 400; response %+v", tc.extra, status, resp)
			}
			if got := handler.RefusedClass(resp.StatusDetail); got != "undeclared-resource" {
				t.Errorf("refusal class = %q, want undeclared-resource", got)
			}
			for _, want := range tc.wants {
				if !strings.Contains(resp.Error, want) {
					t.Errorf("error = %q, want it to name %s", resp.Error, want)
				}
			}
			if n := len(vpc.calls()); n != 0 {
				t.Errorf("aws.vpc observer called %d times, want none", n)
			}
		})
	}
}

func TestTypeObservers_ADeclaredNameStillGoesThroughItsDeclaredObserver(t *testing.T) {
	cloud := newFakeCloud()
	cloud.createAll()
	aObs := &fakeObserver{name: "a", cloud: cloud}
	vpc := &typeObserver{state: contract.Present}

	status, resp := serveBlockLevel(t, &blockResource{cloud: cloud}, map[string]*fakeObserver{"a": aObs}, "validate",
		map[string]any{"resource": "a", "resourceType": "aws.iam-role"},
		func(cfg *handler.Config) {
			cfg.Manifest = []byte(onlyAManifest)
			cfg.TypeObservers = map[string]handler.Observer{"aws.vpc": vpc}
		})

	if status != http.StatusOK {
		t.Fatalf("validate naming declared a: status = %d, want 200; response %+v", status, resp)
	}
	if len(aObs.calls()) != 1 {
		t.Errorf("declared observer for a called %d times, want once", len(aObs.calls()))
	}
	if n := len(vpc.calls()); n != 0 {
		t.Errorf("type observer called %d times for a declared name, want none", n)
	}
}

func TestNew_RefusesATypeObserversConfigTheBlockCannotBack(t *testing.T) {
	cases := []struct {
		label  string
		mutate func(cfg *handler.Config)
		wants  []string
	}{
		{"a key that is not a resource type id", func(cfg *handler.Config) {
			cfg.TypeObservers["Not A Type"] = &typeObserver{}
		}, []string{`"Not A Type"`, "Config.TypeObservers"}},
		{"a nil observer under a resource type id", func(cfg *handler.Config) {
			cfg.TypeObservers["aws.vpc"] = nil
		}, []string{`"aws.vpc"`, "Config.TypeObservers"}},
		{"type observers with no manifest", func(cfg *handler.Config) { cfg.Observers = nil; cfg.Manifest = nil }, []string{"Config.TypeObservers", "Config.Manifest"}},
		{"type observers under a v1alpha manifest", func(cfg *handler.Config) { cfg.Observers = nil; cfg.Manifest = []byte(manifestWithNoHooksKey) }, []string{"Config.TypeObservers", "Config.Manifest"}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			cloud := newFakeCloud()
			cfg := blockLevelConfig(t, s, &blockResource{cloud: cloud}, map[string]handler.Observer{"a": &fakeObserver{name: "a", cloud: cloud}})
			cfg.Manifest = []byte(onlyAManifest)
			cfg.TypeObservers = map[string]handler.Observer{"aws.vpc": &typeObserver{}}
			tc.mutate(&cfg)

			_, err := handler.New(cfg)

			if err == nil {
				t.Fatalf("handler.New accepted the config (%s), want it refused", tc.label)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to name %s", err, want)
				}
			}
		})
	}
}

func TestNew_AcceptsATypeObserverForAParsableTypeOnABlockLevelBlock(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cloud := newFakeCloud()
	cfg := blockLevelConfig(t, s, &blockResource{cloud: cloud}, map[string]handler.Observer{"a": &fakeObserver{name: "a", cloud: cloud}})
	cfg.Manifest = []byte(onlyAManifest)
	cfg.TypeObservers = map[string]handler.Observer{"aws.vpc": &typeObserver{}}
	if _, err := handler.New(cfg); err != nil {
		t.Fatalf("handler.New: %v", err)
	}
}

func TestNew_AcceptsAnObserverForEveryDeclaredResource(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cloud := newFakeCloud()
	if _, err := handler.New(blockLevelConfig(t, s, &blockResource{cloud: cloud}, asObservers(blockObservers(cloud)))); err != nil {
		t.Fatalf("handler.New: %v", err)
	}
}

func TestNew_RefusesADeclaredResourceWithNoObserverNamingIt(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	cloud := newFakeCloud()
	obs := asObservers(blockObservers(cloud))
	delete(obs, "b")

	_, err := handler.New(blockLevelConfig(t, s, &blockResource{cloud: cloud}, obs))
	if err == nil {
		t.Fatal("handler.New accepted a block whose resource b has no observe")
	}
	for _, want := range []string{`"b"`, "observe"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %s", err, want)
		}
	}
}

func TestNew_RefusesAnObserverConfigTheManifestCannotBack(t *testing.T) {
	cloud := newFakeCloud()
	cases := []struct {
		label  string
		mutate func(cfg *handler.Config)
		wants  []string
	}{
		{"an observer for an undeclared resource", func(cfg *handler.Config) {
			cfg.Observers["c"] = &fakeObserver{name: "c", cloud: cloud}
		}, []string{`"c"`}},
		{"observers with no manifest", func(cfg *handler.Config) { cfg.Manifest = nil }, nil},
		{"observers under a v1alpha manifest", func(cfg *handler.Config) { cfg.Manifest = []byte(manifestWithNoHooksKey) }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			cfg := blockLevelConfig(t, s, &blockResource{cloud: cloud}, asObservers(blockObservers(cloud)))
			tc.mutate(&cfg)
			_, err := handler.New(cfg)
			if err == nil {
				t.Fatal("handler.New accepted the config, want it refused")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to name %s", err, want)
				}
			}
		})
	}
}

type errObserver struct{}

func (errObserver) ObserveResource(context.Context, contract.ResourceRef) (contract.Observation, error) {
	return contract.Observation{}, errors.New("describe role: throttled")
}

func TestBlockLevel_AnObserveErrorIsReportedUnderItsResource(t *testing.T) {
	cloud := newFakeCloud()
	s := kittest.NewSigner(t, "kid-block-level", 17)
	obs := asObservers(blockObservers(cloud))
	obs["b"] = errObserver{}
	h, err := handler.New(blockLevelConfig(t, s, &blockResource{cloud: cloud}, obs))
	if err != nil {
		t.Fatalf("handler.New: %v", err)
	}
	status, raw := postHook(t, h, s, envelope(t, s, "nonce-block-level-observe-error", map[string]any{"operation": "subscribe"}))
	resp := decodeResponse(t, raw)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for an unclassified observe error", status)
	}
	if o := resp.Resources["b"].Observe; o == nil || !strings.Contains(o.Error, "throttled") {
		t.Errorf("b observe entry = %+v, want the observe error", o)
	}
}

const roleBlockManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: role-block
  name: Role Block
spec:
  version: 1.0.0
  resources:
    - { name: deploy-role, type: aws.iam-role }
    - { name: ecr-push-role, type: aws.iam-role }
    - { name: audit-role, type: aws.iam-role }
  provides: [deploy-role, ecr-push-role]
  lifecycle:
    endpoint: https://role-block.example/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`

type secretObserver struct{ secrets map[string]string }

func (o secretObserver) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	return contract.Observation{State: contract.Present, SecretOutputs: o.secrets}, nil
}

func serveRoleBlock(t *testing.T, operation string, arns map[string]map[string]string) (int, contract.Response) {
	t.Helper()
	observers := map[string]handler.Observer{}
	for name, secrets := range arns {
		observers[name] = secretObserver{secrets}
	}
	return serveRoleBlockObservers(t, context.Background(), operation, observers)
}

func serveRoleBlockObservers(t *testing.T, ctx context.Context, operation string, observers map[string]handler.Observer) (int, contract.Response) {
	t.Helper()
	s := kittest.NewSigner(t, "kid-role-block", 19)
	cfg := baseConfig(t, &blockResource{cloud: newFakeCloud()}, s)
	cfg.Manifest = []byte(roleBlockManifest)
	cfg.Observers = observers
	h, err := handler.New(cfg)
	if err != nil {
		t.Fatalf("handler.New: %v", err)
	}
	status, raw := postHookWithContext(t, h, s, envelope(t, s, "nonce-role-block-"+operation, map[string]any{"operation": operation}), ctx)
	assertConforms(t, raw)
	return status, decodeResponse(t, raw)
}

func roleArns() map[string]map[string]string {
	return map[string]map[string]string{
		"deploy-role":   {"roleArn": "arn:aws:iam::1:role/deploy"},
		"ecr-push-role": {"roleArn": "arn:aws:iam::1:role/ecr-push"},
		"audit-role":    {"roleArn": "arn:aws:iam::1:role/audit"},
	}
}

func TestBlockLevel_AnswerKeysEachProvidedResourcesSecretOutputsByResourceName(t *testing.T) {
	for _, op := range []string{"subscribe", "upgrade", "validate"} {
		t.Run(op, func(t *testing.T) {
			status, resp := serveRoleBlock(t, op, roleArns())

			want := map[string]map[string]string{
				"deploy-role":   {"roleArn": "arn:aws:iam::1:role/deploy"},
				"ecr-push-role": {"roleArn": "arn:aws:iam::1:role/ecr-push"},
			}
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200; response %+v", status, resp)
			}
			if !reflect.DeepEqual(resp.ResourceSecretOutputs, want) {
				t.Errorf("ResourceSecretOutputs = %v, want %v: provided resources only, none for audit-role", resp.ResourceSecretOutputs, want)
			}
			if got := resp.SecretOutputs["roleArn"]; got != "arn:aws:iam::1:role/ecr-push" {
				t.Errorf(`merged SecretOutputs["roleArn"] = %q, want the later provided resource's value, as before`, got)
			}
		})
	}
}

func TestBlockLevel_NoSecretOutputValueAppearsInTheRecordedResourceOutcomes(t *testing.T) {
	for _, op := range []string{"subscribe", "validate"} {
		t.Run(op, func(t *testing.T) {
			_, resp := serveRoleBlock(t, op, roleArns())

			encoded, err := json.Marshal(resp.Resources)
			if err != nil {
				t.Fatalf("marshal Resources: %v", err)
			}
			if len(resp.Resources) != 3 {
				t.Errorf("Resources has %d entries, want one per declared resource: %s", len(resp.Resources), encoded)
			}
			if strings.Contains(string(encoded), "arn:aws:iam") {
				t.Errorf("Resources = %s, want no secret output value in the recorded outcome", encoded)
			}
		})
	}
}

func TestBlockLevel_AProvidedResourceReportingNoSecretOutputHasNoEntryInTheAnswer(t *testing.T) {
	arns := roleArns()
	arns["ecr-push-role"] = nil

	_, resp := serveRoleBlock(t, "subscribe", arns)

	want := map[string]map[string]string{"deploy-role": {"roleArn": "arn:aws:iam::1:role/deploy"}}
	if !reflect.DeepEqual(resp.ResourceSecretOutputs, want) {
		t.Errorf("ResourceSecretOutputs = %v, want %v", resp.ResourceSecretOutputs, want)
	}
}

func TestBlockLevel_APendingAnswerCarriesTheSecretOutputsOfEachProvidedResourceAlreadyObservedPresent(t *testing.T) {
	const deployArn = "arn:aws:iam::1:role/deploy"
	cases := []struct {
		name      string
		operation string
		ended     bool
	}{
		{"subscribe with a later resource unknown", "subscribe", false},
		{"validate with a later resource unknown", "validate", false},
		{"subscribe ended before the later resource", "subscribe", true},
		{"validate ended before the later resource", "validate", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var deploy handler.Observer = secretObserver{map[string]string{"roleArn": deployArn}}
			if tc.ended {
				deploy = cancellingObserver{Observer: deploy, settledOn: contract.Present, cancel: cancel}
			}
			observers := map[string]handler.Observer{
				"deploy-role":   deploy,
				"ecr-push-role": fixedObserver{contract.Observation{State: contract.Unknown}},
				"audit-role":    secretObserver{map[string]string{"roleArn": "arn:aws:iam::1:role/audit"}},
			}

			status, resp := serveRoleBlockObservers(t, ctx, tc.operation, observers)

			if status != http.StatusOK || !resp.Pending {
				t.Fatalf("status, pending = %d, %v; want 200 and pending; response %+v", status, resp.Pending, resp)
			}
			wantByResource := map[string]map[string]string{"deploy-role": {"roleArn": deployArn}}
			if !reflect.DeepEqual(resp.ResourceSecretOutputs, wantByResource) {
				t.Errorf("ResourceSecretOutputs = %v, want %v: deploy-role was observed present before the answer went pending", resp.ResourceSecretOutputs, wantByResource)
			}
			wantMerged := map[string]string{"roleArn": deployArn}
			if !reflect.DeepEqual(resp.SecretOutputs, wantMerged) {
				t.Errorf("SecretOutputs = %v, want %v: the merged map the platform judges and stores by name", resp.SecretOutputs, wantMerged)
			}
		})
	}
}

const contributionType = "github.scaffold-contribution"

func contributionManifest(resources ...string) string {
	return `apiVersion: mitstack/v1beta
type: block
metadata:
  id: contribution-block
  name: Contribution Block
spec:
  version: 1.0.0
  resources:
    - ` + strings.Join(resources, "\n    - ") + `
  lifecycle:
    endpoint: https://contribution-block.example/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`
}

// contributionSnapshot marks the scaffold contribution and leaves aws.ec2-instance unmarked.
func contributionSnapshot(t *testing.T) contract.TypeSnapshot {
	t.Helper()
	snap, err := contract.ParseTypeSnapshot([]byte(`{"version": 2, "types": [
  {"id": "github.scaffold-contribution", "capability": "github", "kind": "scaffold-contribution", "scope": "per-app", "endsWithSubscription": true, "version": 1},
  {"id": "aws.ec2-instance", "capability": "aws", "kind": "host", "scope": "per-app", "version": 1}]}`))
	if err != nil {
		t.Fatalf("ParseTypeSnapshot: %v", err)
	}
	return snap
}

func recordedContribution() map[string]any {
	return map[string]any{"recordedResources": map[string]any{
		contributionType:   map[string]any{"type": contributionType, "identity": map[string]any{"repository": "acme/weather"}},
		"aws.ec2-instance": map[string]any{"type": "aws.ec2-instance", "identity": map[string]any{"instanceId": "i-0abc"}},
	}}
}

// standingObserver answers present on every call, as files left in a repository the platform keeps do.
type standingObserver struct {
	mu    sync.Mutex
	calls int
}

func (o *standingObserver) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++
	return contract.Observation{State: contract.Present}, nil
}

func (o *standingObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
}

type failingDeleteBlock struct{ *blockResource }

func (b failingDeleteBlock) Delete(ctx context.Context, d contract.Desired) (map[string]string, error) {
	b.deleteCalls++
	return nil, errors.New("the GitHub API answered 502")
}

func contributionConfig(manifest string, obs map[string]handler.Observer, types contract.TypeSnapshot) func(*handler.Config) {
	return func(c *handler.Config) {
		c.Manifest = []byte(manifest)
		c.Observers = obs
		c.Types = types
	}
}

func TestBlockLevel_UnsubscribeCountsAResourceOfAMarkedTypeAbsentOnceTheBlocksDeleteSucceeds(t *testing.T) {
	block := &blockResource{cloud: newFakeCloud()}
	contribution := &standingObserver{}
	obs := map[string]handler.Observer{contributionType: contribution}

	status, resp := serveBlockLevel(t, block, nil, "unsubscribe", recordedContribution(),
		contributionConfig(contributionManifest(contributionType), obs, contributionSnapshot(t)))

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	if block.deleteCalls != 1 {
		t.Errorf("block Delete calls = %d, want 1", block.deleteCalls)
	}
	if resp.Absent == nil || !*resp.Absent {
		t.Errorf("absent = %v, want true: the contribution's type ends with its subscription; detail %q", resp.Absent, resp.StatusDetail)
	}
	o := resp.Resources[contributionType]
	if o.Observe != nil {
		t.Errorf("resource %q observe = %+v, want none: its absence is not observed", contributionType, *o.Observe)
	}
	if o.Delete == nil || o.Delete.State != contract.Absent || o.Delete.Type != contributionType || o.Delete.Error != "" {
		t.Errorf("resource %q delete = %+v, want state absent, type %s and no error", contributionType, o.Delete, contributionType)
	}
	if n := contribution.count(); n != 0 {
		t.Errorf("contribution observer called %d times on unsubscribe, want 0", n)
	}
}

func TestBlockLevel_UnsubscribeStillAnswersAbsentFalseNamingAnUnmarkedResourceObservedPresent(t *testing.T) {
	obs := map[string]handler.Observer{contributionType: &standingObserver{}, "aws.ec2-instance": &standingObserver{}}

	status, resp := serveBlockLevel(t, &blockResource{cloud: newFakeCloud()}, nil, "unsubscribe", recordedContribution(),
		contributionConfig(contributionManifest("aws.ec2-instance", contributionType), obs, contributionSnapshot(t)))

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	if resp.Absent == nil || *resp.Absent {
		t.Errorf("absent = %v, want false: aws.ec2-instance is unmarked and still observed present", resp.Absent)
	}
	if !strings.Contains(resp.StatusDetail, `"aws.ec2-instance"`) {
		t.Errorf("statusDetail = %q, want it to name resource \"aws.ec2-instance\"", resp.StatusDetail)
	}
	if o := resp.Resources["aws.ec2-instance"].Observe; o == nil || o.State != contract.Present {
		t.Errorf("resource \"aws.ec2-instance\" observe = %+v, want state present", o)
	}
	if d := resp.Resources[contributionType].Delete; d == nil || d.State != contract.Absent {
		t.Errorf("resource %q delete = %+v, want state absent", contributionType, d)
	}
}

func TestBlockLevel_UnsubscribeObservesAContributionWhoseSnapshotDoesNotMarkItsType(t *testing.T) {
	cases := []struct {
		label string
		types contract.TypeSnapshot
	}{
		{"no snapshot", contract.TypeSnapshot{}},
		{"a snapshot leaving the type unmarked", contract.TypeSnapshot{Version: 1, Types: []contract.SnapshotType{{ID: contributionType}}}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			contribution := &standingObserver{}
			obs := map[string]handler.Observer{contributionType: contribution}

			_, resp := serveBlockLevel(t, &blockResource{cloud: newFakeCloud()}, nil, "unsubscribe", recordedContribution(),
				contributionConfig(contributionManifest(contributionType), obs, tc.types))

			if resp.Absent == nil || *resp.Absent {
				t.Errorf("absent = %v, want false: nothing marks %s, so its absence must be observed", resp.Absent, contributionType)
			}
			if contribution.count() == 0 {
				t.Errorf("contribution observer never called, want its absence observed")
			}
		})
	}
}

func TestBlockLevel_UnsubscribeCountsNoMarkedResourceAbsentWhenTheBlocksDeleteFails(t *testing.T) {
	block := failingDeleteBlock{&blockResource{cloud: newFakeCloud()}}
	obs := map[string]handler.Observer{contributionType: &standingObserver{}}

	status, resp := serveBlockLevel(t, block, nil, "unsubscribe", recordedContribution(),
		contributionConfig(contributionManifest(contributionType), obs, contributionSnapshot(t)))

	if status == http.StatusOK {
		t.Errorf("status = 200, want the delete's failure answered as an error; response %+v", resp)
	}
	if resp.Absent != nil && *resp.Absent {
		t.Errorf("absent = true, want no absence claimed when the block's delete failed")
	}
	if d := resp.Resources[contributionType].Delete; d != nil && d.State == contract.Absent {
		t.Errorf("resource %q delete = %+v, want it not counted absent", contributionType, *d)
	}
}

func TestBlockLevel_UnsubscribeHandsTheBlocksDeleteTheRequestAndMergesItsOutputsIntoTheAbsentAnswer(t *testing.T) {
	cloud := newFakeCloud()
	cloud.createAll()
	block := &blockResource{cloud: cloud, deleteOutputs: map[string]string{"dataSnapshotId": "snap-0abc"}}

	status, resp := serveBlockLevel(t, block, blockObservers(cloud), "unsubscribe", map[string]any{
		"recordedResources": recordedAll(),
		"params":            map[string]string{"dataHandover": "snapshot"},
	})

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	if resp.Absent == nil || !*resp.Absent {
		t.Fatalf("absent = %v, want true: every resource is gone", resp.Absent)
	}
	if got := resp.Outputs["dataSnapshotId"]; got != "snap-0abc" {
		t.Errorf(`outputs["dataSnapshotId"] = %q, want the %q the block's Delete returned; outputs %v`, got, "snap-0abc", resp.Outputs)
	}
	if len(block.deleted) != 1 || block.deleted[0].Params["dataHandover"] != "snapshot" {
		t.Errorf("Delete was handed %+v, want one call carrying the request's params", block.deleted)
	}
}

func TestBlockLevel_AnUnsubscribeLeavingAResourceStandingAnswersNoDeleteOutputs(t *testing.T) {
	cloud := newFakeCloud()
	cloud.createAll()
	cloud.standsAfterDelete["b"] = true
	block := &blockResource{cloud: cloud, deleteOutputs: map[string]string{"dataSnapshotId": "snap-0abc"}}

	status, resp := serveBlockLevel(t, block, blockObservers(cloud), "unsubscribe", map[string]any{"recordedResources": recordedAll()})

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	if resp.Absent == nil || *resp.Absent {
		t.Fatalf("absent = %v, want false: b still stands", resp.Absent)
	}
	if len(resp.Outputs) != 0 {
		t.Errorf("outputs = %v, want none until the block settles absent", resp.Outputs)
	}
}

func TestBlockLevel_RefusesADeleteOutputCarryingARequestSecret(t *testing.T) {
	const token = "ghp_delete-output-leak-8c41d2e9"
	cloud := newFakeCloud()
	cloud.createAll()
	block := &blockResource{cloud: cloud, deleteOutputs: map[string]string{"leaked": token}}

	status, resp := serveBlockLevel(t, block, blockObservers(cloud), "unsubscribe", map[string]any{
		"recordedResources": recordedAll(),
		"secrets":           map[string]any{"github": map[string]any{"token": token}},
	})

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; response %+v", status, resp)
	}
	if got := handler.RefusedClass(resp.StatusDetail); got != "secret-in-output" {
		t.Errorf("refusal class = %q, want secret-in-output", got)
	}
	if strings.Contains(resp.Error+resp.StatusDetail, token) {
		t.Error("the refusal carries the secret value")
	}
	if resp.Absent != nil && *resp.Absent {
		t.Error("absent = true, want no absence claimed beside a refused output")
	}
}

// fixedObserver answers one observation for every call.
type fixedObserver struct{ obs contract.Observation }

func (o fixedObserver) ObserveResource(context.Context, contract.ResourceRef) (contract.Observation, error) {
	return o.obs, nil
}

// serveBlockLevelObservers serves one verb over r with obs under the block-level manifest, on ctx.
func serveBlockLevelObservers(t *testing.T, ctx context.Context, r handler.Resource, obs map[string]handler.Observer, operation string, extra map[string]any) (int, contract.Response) {
	t.Helper()
	s := kittest.NewSigner(t, "kid-block-level-observers", 19)
	h, err := handler.New(blockLevelConfig(t, s, r, obs))
	if err != nil {
		t.Fatalf("handler.New: %v", err)
	}
	fields := map[string]any{"operation": operation}
	for k, v := range extra {
		fields[k] = v
	}
	status, raw := postHookWithContext(t, h, s, envelope(t, s, "nonce-block-level-observers-"+operation, fields), ctx)
	assertConforms(t, raw)
	return status, decodeResponse(t, raw)
}

func presentObservers(cloud *fakeCloud) map[string]handler.Observer {
	cloud.createAll()
	return asObservers(blockObservers(cloud))
}

func TestBlockLevel_ALaterProvidedResourceWinsAnOutputBothProduce(t *testing.T) {
	cloud := newFakeCloud()
	obs := presentObservers(cloud)
	obs["aws.ec2-instance"] = fixedObserver{contract.Observation{State: contract.Present, Outputs: map[string]string{"shared": "from-instance"}}}
	obs["a"] = fixedObserver{contract.Observation{State: contract.Present, Outputs: map[string]string{"shared": "from-a"}}}

	status, resp := serveBlockLevelObservers(t, context.Background(), &blockResource{cloud: cloud}, obs, "subscribe", nil)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	if got := resp.Outputs["shared"]; got != "from-a" {
		t.Errorf(`outputs["shared"] = %q, want %q: a is declared after aws.ec2-instance`, got, "from-a")
	}
}

func TestBlockLevel_ScrubsRequestSecretsFromEveryResourceEntry(t *testing.T) {
	const token = "ghp_status-detail-leak-5b72fa10"
	cloud := newFakeCloud()
	obs := presentObservers(cloud)
	obs["a"] = fixedObserver{contract.Observation{State: contract.Present, StatusDetail: "role assumed with " + token}}

	status, resp := serveBlockLevelObservers(t, context.Background(), &blockResource{cloud: cloud}, obs, "validate", map[string]any{
		"secrets": map[string]any{"github": map[string]any{"token": token}},
	})

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	o := resp.Resources["a"].Observe
	if o == nil || strings.Contains(o.StatusDetail, token) || !strings.Contains(o.StatusDetail, "[REDACTED]") {
		t.Errorf("a observe = %+v, want its statusDetail scrubbed of the request secret", o)
	}
}

func TestBlockLevel_RefusesAnObservedOutputCarryingARequestSecret(t *testing.T) {
	const token = "ghp_observed-output-leak-3e9d7c21"
	cloud := newFakeCloud()
	obs := presentObservers(cloud)
	obs["a"] = fixedObserver{contract.Observation{State: contract.Present, Outputs: map[string]string{"leaked": token}}}

	status, resp := serveBlockLevelObservers(t, context.Background(), &blockResource{cloud: cloud}, obs, "validate", map[string]any{
		"secrets": map[string]any{"github": map[string]any{"token": token}},
	})

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; response %+v", status, resp)
	}
	if got := handler.RefusedClass(resp.StatusDetail); got != "secret-in-output" {
		t.Errorf("refusal class = %q, want secret-in-output", got)
	}
	if o := resp.Resources["a"].Observe; o == nil || o.Error == "" {
		t.Errorf("a observe = %+v, want the refusal recorded under it", o)
	}
	if strings.Contains(resp.Error+resp.StatusDetail, token) {
		t.Error("the refusal carries the secret value")
	}
}

// cancellingObserver cancels the request context once it reports settledOn, so the verb sees a done context only after this resource settled.
type cancellingObserver struct {
	handler.Observer
	settledOn contract.State
	cancel    context.CancelFunc
}

func (o cancellingObserver) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	obs, err := o.Observer.ObserveResource(ctx, r)
	if err == nil && obs.State == o.settledOn {
		o.cancel()
	}
	return obs, err
}

func TestBlockLevel_StopsBeforeTheNextResourceOnceTheContextIsDone(t *testing.T) {
	cases := []struct {
		operation  string
		cancelling string
		settledOn  contract.State
		notReached string
	}{
		{"subscribe", "aws.vpc", contract.Present, "aws.ec2-instance"},
		{"validate", "aws.vpc", contract.Present, "aws.ec2-instance"},
		{"unsubscribe", "b", contract.Absent, "a"},
	}
	for _, tc := range cases {
		t.Run(tc.operation, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cloud := newFakeCloud()
			fakes := blockObservers(cloud)
			if tc.operation != "subscribe" {
				cloud.createAll()
			}
			obs := asObservers(fakes)
			obs[tc.cancelling] = cancellingObserver{Observer: fakes[tc.cancelling], settledOn: tc.settledOn, cancel: cancel}

			status, resp := serveBlockLevelObservers(t, ctx, &blockResource{cloud: cloud}, obs, tc.operation, map[string]any{"recordedResources": recordedAll()})

			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200; response %+v", status, resp)
			}
			if !strings.Contains(resp.StatusDetail, `"`+tc.notReached+`"`) {
				t.Errorf("statusDetail = %q, want it to name %q, the first resource not reached", resp.StatusDetail, tc.notReached)
			}
			if got := len(resp.Resources); got != 1 || resp.Resources[tc.cancelling].Observe == nil {
				t.Errorf("resources = %+v, want only %q, the one reached", resp.Resources, tc.cancelling)
			}
			if calls := fakes[tc.notReached].calls(); len(calls) != 0 {
				t.Errorf("%q observed %d time(s), want none after the context was done", tc.notReached, len(calls))
			}
			if tc.operation == "unsubscribe" && (resp.Absent == nil || *resp.Absent) {
				t.Errorf("absent = %v, want false: %q was never proven absent", resp.Absent, tc.notReached)
			}
			if tc.operation != "unsubscribe" && !resp.Pending {
				t.Errorf("pending = false, want true: %q was never reached", tc.notReached)
			}
		})
	}
}

var capabilityManifest = strings.Replace(blockLevelManifest, "type: block\n", "type: capability\n", 1)

func withManifest(manifest string) func(*handler.Config) {
	return func(cfg *handler.Config) { cfg.Manifest = []byte(manifest) }
}

var issuedExpiry = time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)

// issuingBlock issues every named app the credential it holds, and keeps the apps it issued to.
type issuingBlock struct {
	*blockResource
	credential contract.Credential
	apps       []string
}

func (b *issuingBlock) IssueCredential(ctx context.Context, d contract.Desired, appID string) (contract.Credential, error) {
	b.apps = append(b.apps, appID)
	return b.credential, nil
}

func newIssuingBlock(values map[string]string, expiresAt time.Time) *issuingBlock {
	return &issuingBlock{blockResource: &blockResource{cloud: newFakeCloud()}, credential: contract.Credential{Values: values, ExpiresAt: expiresAt}}
}

// Each issue reaches the capability, since a credential replayed from the idempotency cache could be one that has expired.
func TestBlockLevel_IssueAnswersEachCallWithTheCredentialTheCapabilityIssuesTheNamedApp(t *testing.T) {
	block := newIssuingBlock(map[string]string{"accessToken": "tok-app-shop"}, issuedExpiry)
	s := kittest.NewSigner(t, "kid-issue", 18)
	cfg := blockLevelConfig(t, s, block, asObservers(blockObservers(block.cloud)))
	withManifest(capabilityManifest)(&cfg)
	h, err := handler.New(cfg)
	if err != nil {
		t.Fatalf("handler.New: %v", err)
	}

	for _, nonce := range []string{"nonce-issue-1", "nonce-issue-2"} {
		status, raw := postHook(t, h, s, envelope(t, s, nonce, map[string]any{"operation": "issue", "issueAppId": "app-shop", "idempotencyKey": "issue-1"}))

		assertConforms(t, raw)
		c := decodeResponse(t, raw).Credential
		if status != http.StatusOK || c == nil || !reflect.DeepEqual(c.Values, block.credential.Values) || !c.ExpiresAt.Equal(issuedExpiry) {
			t.Errorf("%s: status %d, credential %+v; want 200 and %+v", nonce, status, c, block.credential)
		}
	}
	if !slices.Equal(block.apps, []string{"app-shop", "app-shop"}) || block.ensureCalls+block.deleteCalls != 0 {
		t.Errorf("issued to %q with %d Ensure and %d Delete calls, want app-shop once per call and neither verb",
			block.apps, block.ensureCalls, block.deleteCalls)
	}
}

func TestBlockLevel_IssueIsRefusedUnlessACapabilityIssuesACredentialWithItsExpiry(t *testing.T) {
	token, app := map[string]string{"accessToken": "tok-app-shop"}, map[string]any{"issueAppId": "app-shop"}
	withSecret := map[string]any{"issueAppId": "app-shop", "secretInputs": map[string]any{"connectionKey": "conn-key-9f2"}}
	cases := []struct {
		name, manifest string
		resource       handler.Resource
		extra          map[string]any
		status         int
		want           string
	}{
		{"a block", blockLevelManifest, newIssuingBlock(token, issuedExpiry), app, http.StatusBadRequest, "refused:not-a-capability"},
		{"a capability with no issue handler", capabilityManifest, &blockResource{cloud: newFakeCloud()}, app, http.StatusBadRequest, "refused:issues-no-credential"},
		{"an issue naming no app", capabilityManifest, newIssuingBlock(token, issuedExpiry), nil, http.StatusBadRequest, `"issueAppId"`},
		{"a credential with no expiry", capabilityManifest, newIssuingBlock(token, time.Time{}), app, http.StatusInternalServerError, "expiry"},
		{"a credential with no value", capabilityManifest, newIssuingBlock(nil, issuedExpiry), app, http.StatusInternalServerError, "no value"},
		{"a credential repeating a secret the request carried", capabilityManifest,
			newIssuingBlock(map[string]string{"accessToken": "conn-key-9f2"}, issuedExpiry), withSecret, http.StatusInternalServerError, "accessToken"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, resp := serveBlockLevel(t, tc.resource, blockObservers(newFakeCloud()), "issue", tc.extra, withManifest(tc.manifest))

			if status != tc.status || resp.Credential != nil {
				t.Fatalf("status = %d, credential = %+v; want %d and none", status, resp.Credential, tc.status)
			}
			if got := resp.Error + " " + resp.StatusDetail; !strings.Contains(got, tc.want) || strings.Contains(got, "conn-key-9f2") {
				t.Errorf("error and statusDetail = %q, want them to name %s and carry no secret", got, tc.want)
			}
		})
	}
}
