package kittest_test

// A teardown run on a fresh process must remove what a subscribe broken at any side effect left behind.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

const containerOnlyManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: container-only
  name: Container Only
spec:
  version: 1.0.0
  resources: [aws.ec2-container]
  lifecycle:
    endpoint: https://container-only.example/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`

// heldProvider holds each item it created under the subscription that created it.
type heldProvider struct {
	mu     sync.Mutex
	held   map[string]string
	faults *kittest.Faults
	// findsOnTeardown makes Delete remove everything the subscription holds instead of what its own process created.
	findsOnTeardown bool
	// ignoresFaults makes every side effect skip the Faults it was handed.
	ignoresFaults bool
	refusesWrites bool
}

func (p *heldProvider) create(item, subscriptionID, what string) error {
	if p.refusesWrites {
		return errors.New("the provider refuses every write")
	}
	if !p.ignoresFaults {
		if err := p.faults.SideEffect(what); err != nil {
			return err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.held[item] = subscriptionID
	return nil
}

func (p *heldProvider) remove(item string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.held, item)
}

func (p *heldProvider) Holds(subscriptionID string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for item, sub := range p.held {
		if sub == subscriptionID {
			out = append(out, item)
		}
	}
	slices.Sort(out)
	return out
}

func (p *heldProvider) Process() (handler.Resource, map[string]handler.Observer) {
	return &credentialThenContainer{p: p}, map[string]handler.Observer{"aws.ec2-container": heldObserver{p}}
}

// credentialThenContainer writes a credential, then starts a container, and remembers both only once both succeed.
type credentialThenContainer struct {
	p       *heldProvider
	mu      sync.Mutex
	created []string
}

func (b *credentialThenContainer) EnsureResources(_ context.Context, d contract.Desired) ([]contract.CreatedResource, error) {
	credential, container := "credential-"+d.SubscriptionID, "container-"+d.SubscriptionID
	if err := b.p.create(credential, d.SubscriptionID, "write the credential"); err != nil {
		return nil, err
	}
	if err := b.p.create(container, d.SubscriptionID, "start the container"); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.created = []string{credential, container}
	return []contract.CreatedResource{{Name: "aws.ec2-container", Type: "aws.ec2-container", Identity: map[string]string{"id": container}}}, nil
}

func (b *credentialThenContainer) Ensure(ctx context.Context, d contract.Desired) error {
	_, err := b.EnsureResources(ctx, d)
	return err
}

func (b *credentialThenContainer) Observe(context.Context, contract.Desired) (contract.Observation, error) {
	return contract.Observation{State: contract.Unknown}, nil
}

func (b *credentialThenContainer) Delete(_ context.Context, d contract.Desired) (map[string]string, error) {
	b.mu.Lock()
	items := b.created
	b.mu.Unlock()
	if b.p.findsOnTeardown {
		items = b.p.Holds(d.SubscriptionID)
	}
	for _, item := range items {
		b.p.remove(item)
	}
	return nil, nil
}

type heldObserver struct{ p *heldProvider }

func (o heldObserver) ObserveResource(_ context.Context, r contract.ResourceRef) (contract.Observation, error) {
	if slices.Contains(o.p.Holds(r.Desired.SubscriptionID), "container-"+r.Desired.SubscriptionID) {
		return contract.Observation{State: contract.Present}, nil
	}
	return contract.Observation{State: contract.Absent}, nil
}

func brokenSetup(t *testing.T, configure func(*heldProvider)) kittest.BrokenSetup {
	t.Helper()
	return kittest.BrokenSetup{
		Manifest: []byte(containerOnlyManifest),
		Types:    typeSnapshot(t),
		Desired: contract.Desired{
			Identity: contract.Identity{SubscriptionID: "sub-broken", BlockID: "container-only", OrgID: "org-1"},
		},
		NewProvider: func(faults *kittest.Faults) kittest.FaultProvider {
			p := &heldProvider{held: map[string]string{}, faults: faults}
			configure(p)
			return p
		},
	}
}

func TestTeardownAfterBrokenSetup_FailsATeardownThatLeavesTheFirstSideEffect(t *testing.T) {
	problems := kittest.CheckTeardownAfterBrokenSetup(brokenSetup(t, func(*heldProvider) {}))

	if len(problems) != 1 {
		t.Fatalf("want one problem, for the run broken at the container, got %q", problems)
	}
	for _, want := range []string{"side effect 2 of 2", "start the container", `"credential-sub-broken"`} {
		if !strings.Contains(problems[0], want) {
			t.Errorf("problem %q does not name %s", problems[0], want)
		}
	}
}

func TestTeardownAfterBrokenSetup_PassesATeardownThatFindsEverything(t *testing.T) {
	kittest.AssertTeardownAfterBrokenSetup(t, brokenSetup(t, func(p *heldProvider) { p.findsOnTeardown = true }))
}

func TestTeardownAfterBrokenSetup_RefusesASetupThatCannotBreakAnything(t *testing.T) {
	noSideEffect := brokenSetup(t, func(p *heldProvider) { p.ignoresFaults = true })
	noProvider := brokenSetup(t, func(*heldProvider) {})
	noProvider.NewProvider = nil
	noSubscription := brokenSetup(t, func(*heldProvider) {})
	noSubscription.Desired.SubscriptionID = ""
	unservable := brokenSetup(t, func(*heldProvider) {})
	unservable.Manifest = []byte("apiVersion: mitstack/v1beta\n")
	refused := brokenSetup(t, func(p *heldProvider) { p.refusesWrites = true })
	unreached := brokenSetup(t, func(*heldProvider) {})
	runs := 0
	unreached.NewProvider = func(f *kittest.Faults) kittest.FaultProvider {
		runs++
		return &heldProvider{held: map[string]string{}, faults: f, ignoresFaults: runs > 1}
	}

	for want, in := range map[string]kittest.BrokenSetup{
		"no side effect": noSideEffect, "NewProvider is required": noProvider, "SubscriptionID": noSubscription,
		"cannot be served": unservable, "with nothing broken answered": refused, "never reached": unreached,
	} {
		t.Run(want, func(t *testing.T) {
			problems := kittest.CheckTeardownAfterBrokenSetup(in)

			if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), want) {
				t.Errorf("want a problem naming %q, got %q", want, problems)
			}
		})
	}
}
