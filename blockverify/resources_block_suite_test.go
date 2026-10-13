package main

// The resources suite runs a block-level block's kittest.AssertBlock and fails a block that breaks any guard, its detail naming the resource.
// Each fixture is a compiling module in t.TempDir() with the kit module replaced by this checkout.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const blockFixtureManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: blockverify-block-fixture
  name: blockverify block fixture
  owner: blockverify-tests
spec:
  version: 1.0.0
  description: A fixture block that creates an instance and its data volume.
  resources:
    - aws.ec2-instance
    - aws.ebs-volume
  provides: [aws.ec2-instance]
  lifecycle:
    endpoint: https://api.mitstack.dev/operators/blockverify-block-fixture/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`

const listFixtureManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: blockverify-list-fixture
  name: blockverify list fixture
  owner: blockverify-tests
spec:
  version: 1.0.0
  description: A fixture block that runs a container on either cloud's host.
  requires:
    - { name: host, type: [aws.ec2-instance, hetzner.server] }
  resources:
    - { name: app-container, type: [aws.ec2-container, hetzner.server-container] }
  provides: [app-container]
  lifecycle:
    endpoint: https://api.mitstack.dev/operators/blockverify-list-fixture/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`

// blockFixtureCommonGo is the fixture's provider, block-level handler and
// observers. The block creates one object per declared resource, tagged
// with the subscription, and replaces its volume when volumeSize changes;
// each field of misbehave breaks one guard.
const blockFixtureCommonGo = `package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/observe"
)

type misbehave struct {
	extraTagged     bool
	leaveOnDelete   string
	hideReplacement bool
	staleObserve    string
	failOnHostType  string
	dropOutput      string
}

type provider struct {
	mu    sync.Mutex
	items map[string]observe.Tagged
	tags  map[string]string
}

func (p *provider) put(id, typ, sub string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.items[id] = observe.Tagged{Type: typ, ID: id}
	p.tags[id] = sub
}

func (p *provider) remove(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.items, id)
	delete(p.tags, id)
}

func (p *provider) holds(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.items[id]
	return ok
}

func (p *provider) ListTagged(_ context.Context, sub string) ([]observe.Tagged, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []observe.Tagged
	for id, item := range p.items {
		if p.tags[id] == sub {
			out = append(out, item)
		}
	}
	return out, nil
}

func (p *provider) Lose(_ context.Context, r contract.CreatedResource) error {
	p.remove(r.Identity["id"])
	return nil
}

type block struct {
	p         *provider
	m         misbehave
	container bool
	mu        sync.Mutex
	seq       int
	size      string
	created   map[string]contract.CreatedResource
}

func (b *block) wanted(d contract.Desired) map[string]string {
	if !b.container {
		return map[string]string{"aws.ec2-instance": "aws.ec2-instance", "aws.ebs-volume": "aws.ebs-volume"}
	}
	if d.RequirementTypes["host"] == "hetzner.server" {
		return map[string]string{"app-container": "hetzner.server-container"}
	}
	return map[string]string{"app-container": "aws.ec2-container"}
}

func (b *block) EnsureResources(ctx context.Context, d contract.Desired) ([]contract.CreatedResource, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.container && d.Bindings["host"]["hostId"] == "" {
		return nil, fmt.Errorf("no hostId bound for requires.host")
	}
	if b.created == nil {
		b.created = map[string]contract.CreatedResource{}
	}
	var out []contract.CreatedResource
	for name, typ := range b.wanted(d) {
		prior, had := b.created[name]
		keep := had && b.p.holds(prior.Identity["id"])
		if name == "aws.ebs-volume" && had && b.size != d.Params["volumeSize"] {
			b.p.remove(prior.Identity["id"])
			keep = false
		}
		r := prior
		if !keep {
			b.seq++
			r = contract.CreatedResource{Name: name, Type: typ, Identity: map[string]string{"id": fmt.Sprintf("%s-%d", typ, b.seq)}}
			b.p.put(r.Identity["id"], typ, d.SubscriptionID)
		}
		b.created[name] = r
		out = append(out, r)
	}
	b.size = d.Params["volumeSize"]
	if b.m.extraTagged {
		b.p.put("state-bucket", "aws.s3-bucket", d.SubscriptionID)
	}
	return out, nil
}

func (b *block) Ensure(ctx context.Context, d contract.Desired) error {
	_, err := b.EnsureResources(ctx, d)
	return err
}

func (b *block) PlanReplacements(ctx context.Context, d contract.Desired) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, had := b.created["aws.ebs-volume"]; had && !b.m.hideReplacement && b.size != d.Params["volumeSize"] {
		return []string{"aws.ebs-volume"}, nil
	}
	return nil, nil
}

func (b *block) Observe(ctx context.Context, d contract.Desired) (contract.Observation, error) {
	return contract.Observation{State: contract.Unknown}, nil
}

func (b *block) Delete(ctx context.Context, d contract.Desired) (map[string]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for name, r := range b.created {
		if name == b.m.leaveOnDelete || (b.m.failOnHostType != "" && d.RequirementTypes["host"] == b.m.failOnHostType) {
			continue
		}
		b.p.remove(r.Identity["id"])
		delete(b.created, name)
	}
	b.p.remove("state-bucket")
	return nil, nil
}

type providerObserver struct {
	p    *provider
	drop string
}

func (o providerObserver) ObserveResource(_ context.Context, r contract.ResourceRef) (contract.Observation, error) {
	switch id := r.Identity["id"]; {
	case id == "":
		return contract.Observation{State: contract.Unknown}, nil
	case o.p.holds(id):
		var outputs map[string]string
		if r.Type == "aws.ec2-instance" {
			outputs = map[string]string{"hostId": id, "execKind": "ssm", "execTarget": id}
			delete(outputs, o.drop)
		}
		return contract.Observation{State: contract.Present, Outputs: outputs}, nil
	}
	return contract.Observation{State: contract.Absent}, nil
}

type rememberingObserver struct {
	b    *block
	name string
}

func (o rememberingObserver) ObserveResource(_ context.Context, r contract.ResourceRef) (contract.Observation, error) {
	o.b.mu.Lock()
	defer o.b.mu.Unlock()
	if _, ok := o.b.created[o.name]; ok {
		return contract.Observation{State: contract.Present}, nil
	}
	return contract.Observation{State: contract.Absent}, nil
}

func assertBlock(t *testing.T, m misbehave, container bool) {
	manifest, err := os.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("type-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := contract.ParseTypeSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	p := &provider{items: map[string]observe.Tagged{}, tags: map[string]string{}}
	b := &block{p: p, m: m, container: container}
	in := kittest.Block{
		Manifest: manifest,
		Types:    snap,
		Handler:  b,
		Provider: p,
		Desired: contract.Desired{
			Identity: contract.Identity{SubscriptionID: "sub-fixture", BlockID: "blockverify-block-fixture", OrgID: "org-fixture"},
			Params:   map[string]string{"volumeSize": "10"},
		},
	}
	if container {
		in.Observers = map[string]handler.Observer{"app-container": providerObserver{p: p}}
		in.Requirements = map[string]map[string]map[string]string{"host": {
			"aws.ec2-instance": {"hostId": "i-0host", "execKind": "ssm", "execTarget": "i-0host"},
			"hetzner.server":   {"hostId": "4711", "execKind": "ssh", "execTarget": "198.51.100.9"},
		}}
	} else {
		in.Observers = map[string]handler.Observer{"aws.ec2-instance": providerObserver{p: p, drop: m.dropOutput}, "aws.ebs-volume": providerObserver{p: p}}
		if m.staleObserve != "" {
			in.Observers[m.staleObserve] = rememberingObserver{b: b, name: m.staleObserve}
		}
		upgrade := in.Desired
		upgrade.Params = map[string]string{"volumeSize": "20"}
		in.Upgrade = &upgrade
	}
	kittest.AssertBlock(t, in)
}
`

// blockFixtureTestGo is a TestDeclaredResources calling the fixture's
// assertBlock with misbehave and container as Go literals.
func blockFixtureTestGo(misbehave, container string) string {
	return `package main

import "testing"

func TestDeclaredResources(t *testing.T) {
	assertBlock(t, misbehave{` + misbehave + `}, ` + container + `)
}
`
}

// blockFixture writes a module holding manifest, the fixture's block and
// testGo, and the platform's type snapshot, and returns its directory.
func blockFixture(t *testing.T, manifest, testGo string) string {
	t.Helper()
	snapshot, err := os.ReadFile(filepath.Join(kitModuleDir(t), "contract", "testdata", "type-snapshot.json"))
	if err != nil {
		t.Fatalf("reading the platform's type snapshot fixture: %v", err)
	}
	goMod := "module fixture\n\ngo 1.25\n\n" +
		"require github.com/mitstack-eu/operator-kit/v2 v2.0.0\n\n" +
		"replace github.com/mitstack-eu/operator-kit/v2 => " + kitModuleDir(t) + "\n"
	return newFixtureFrom(t, map[string]string{
		"go.mod":             goMod,
		"main.go":            resourcesFixtureMainGo,
		"manifest.yaml":      manifest,
		"type-snapshot.json": string(snapshot),
		"block_test.go":      blockFixtureCommonGo,
		"resources_test.go":  testGo,
	})
}

// The pass detail says which guards ran. This fixture upgrades, so the
// replacement guard ran and the detail names it.
func TestResourcesSuite_PassesABlockLevelBlockThatHoldsEveryGuard(t *testing.T) {
	dir := blockFixture(t, blockFixtureManifest, blockFixtureTestGo("", "false"))
	got := resourcesSuite(dir)
	requireResourcesStatus(t, got, "pass")
	if !strings.Contains(got.Detail, "replace") {
		t.Errorf("resources detail = %q, want it to name the replacement guard, which ran", got.Detail)
	}
}

func TestResourcesSuite_FailsTheFourGuardsNamingTheResource(t *testing.T) {
	cases := []struct {
		label, misbehave string
		wants            []string
	}{
		{"an observe that still reports a resource the provider lost",
			`staleObserve: "aws.ec2-instance"`, []string{`resource "aws.ec2-instance"`}},
		{"a tagged resource the manifest does not declare",
			`extraTagged: true`, []string{`resource "state-bucket"`, "aws.s3-bucket"}},
		{"a declared resource left present after unsubscribe",
			`leaveOnDelete: "aws.ebs-volume"`, []string{`resource "aws.ebs-volume"`}},
		{"a validate hiding the replacement of a stateful resource",
			`hideReplacement: true`, []string{`resource "aws.ebs-volume"`, "stateful"}},
		{"a provided resource missing an output its type requires",
			`dropOutput: "hostId"`, []string{`resource "aws.ec2-instance"`, "hostId"}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got := resourcesSuite(blockFixture(t, blockFixtureManifest, blockFixtureTestGo(tc.misbehave, "false")))
			requireResourcesStatus(t, got, "fail")
			for _, want := range tc.wants {
				if !strings.Contains(got.Detail, want) {
					t.Errorf("resources detail = %q, want it to name %s", got.Detail, want)
				}
			}
		})
	}
}

// The container block declares no stateful type and runs no upgrade, so the
// replacement guard is skipped, and the pass detail must not claim it ran.
func TestResourcesSuite_RunsTheBlockOncePerListedTypeAndPasses(t *testing.T) {
	dir := blockFixture(t, listFixtureManifest, blockFixtureTestGo("", "true"))
	got := resourcesSuite(dir)
	requireResourcesStatus(t, got, "pass")
	if strings.Contains(got.Detail, "replace") {
		t.Errorf("resources detail = %q, want no claim of the replacement guard, which did not run", got.Detail)
	}
}

func TestResourcesSuite_FailsNamingTheListedTypeWhoseRunFails(t *testing.T) {
	got := resourcesSuite(blockFixture(t, listFixtureManifest, blockFixtureTestGo(`failOnHostType: "hetzner.server"`, "true")))
	requireResourcesFailNaming(t, got, `resource "app-container"`)
	if !strings.Contains(strings.ReplaceAll(got.Detail, "hetzner.server-container", ""), "hetzner.server") {
		t.Errorf("resources detail = %q, want it to name hetzner.server, the requirement type whose run failed", got.Detail)
	}
}
