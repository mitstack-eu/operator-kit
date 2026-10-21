package kittest

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/handler"
)

// BrokenSetup is what a block supplies to prove its teardown after a subscribe broken at each side effect.
type BrokenSetup struct {
	Manifest []byte
	Types    contract.TypeSnapshot
	// Desired needs its SubscriptionID, BlockID and OrgID; the unsubscribe is sent without its recorded resources and prior outputs.
	Desired contract.Desired
	// NewProvider returns an empty fake provider whose fakes call faults.SideEffect before each change they make to it.
	NewProvider func(faults *Faults) FaultProvider
}

// FaultProvider is a fake provider that keeps its state across the block processes it serves.
type FaultProvider interface {
	// Process returns the block as a newly started operator over this provider, holding none of an earlier process's own state.
	Process() (handler.Resource, map[string]handler.Observer)
	Holds(subscriptionID string) []string
}

// Faults counts the side effects a block's fakes make and breaks the one a run picks.
type Faults struct {
	mu     sync.Mutex
	made   []string
	breaks int
	broken string
}

// SideEffect records one side effect and returns an error when it is the one this run breaks; a nil Faults breaks nothing.
func (f *Faults) SideEffect(what string) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.made = append(f.made, what)
	if len(f.made) != f.breaks {
		return nil
	}
	f.broken = what
	return fmt.Errorf("kittest: side effect %d, %s, broken by the check", f.breaks, what)
}

func (f *Faults) disarm() (made []string, broken string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.breaks = 0
	return f.made, f.broken
}

// CheckTeardownAfterBrokenSetup breaks a subscribe at each side effect in turn, unsubscribes on a fresh process, and returns one problem per run that leaves anything in the provider.
func CheckTeardownAfterBrokenSetup(in BrokenSetup) []string {
	problems, _ := checkTeardownAfterBrokenSetup(in)
	return problems
}

// AssertTeardownAfterBrokenSetup runs CheckTeardownAfterBrokenSetup, logs the side effects it broke, and fails tb with one error per problem.
func AssertTeardownAfterBrokenSetup(tb testing.TB, in BrokenSetup) {
	tb.Helper()
	problems, effects := checkTeardownAfterBrokenSetup(in)
	tb.Logf("kittest.AssertTeardownAfterBrokenSetup broke each side effect in turn: %s", strings.Join(effects, ", "))
	for _, p := range problems {
		tb.Error(p)
	}
}

func checkTeardownAfterBrokenSetup(in BrokenSetup) ([]string, []string) {
	if in.NewProvider == nil {
		return []string{"NewProvider is required, so there is no provider to break"}, nil
	}
	if in.Desired.SubscriptionID == "" || in.Desired.BlockID == "" || in.Desired.OrgID == "" {
		return []string{"Desired needs a SubscriptionID, a BlockID and an OrgID, which every request the engine sends carries"}, nil
	}
	faults := &Faults{}
	status, resp, err := in.send(in.NewProvider(faults), "subscribe", in.Desired)
	effects, _ := faults.disarm()
	switch {
	case err != nil:
		return []string{err.Error()}, nil
	case status != http.StatusOK:
		return []string{fmt.Sprintf("the subscribe with nothing broken answered %d, so its side effects are unknown: %s", status, resp.Error)}, nil
	case len(effects) == 0:
		return []string{"the subscribe made no side effect through Faults.SideEffect, so no setup was broken; call it from each fake before it changes the provider"}, nil
	}
	var problems []string
	for n := 1; n <= len(effects); n++ {
		if p := in.breakAt(n, len(effects)); p != "" {
			problems = append(problems, p)
		}
	}
	return problems, effects
}

func (in BrokenSetup) breakAt(n, of int) string {
	faults := &Faults{breaks: n}
	provider := in.NewProvider(faults)
	if _, _, err := in.send(provider, "subscribe", in.Desired); err != nil {
		return err.Error()
	}
	_, broken := faults.disarm()
	if broken == "" {
		return fmt.Sprintf("side effect %d of %d was never reached when the check broke it, so the subscribe does not make the same side effects every time", n, of)
	}
	unrecorded := in.Desired
	unrecorded.RecordedResources, unrecorded.PriorOutputs = nil, nil
	status, _, err := in.send(provider, "unsubscribe", unrecorded)
	if err != nil {
		return err.Error()
	}
	left := provider.Holds(in.Desired.SubscriptionID)
	if len(left) == 0 {
		return ""
	}
	return fmt.Sprintf("after a subscribe broken at side effect %d of %d, %s, an unsubscribe on a fresh process answered %d and left %q in the provider",
		n, of, broken, status, left)
}

func (in BrokenSetup) send(provider FaultProvider, operation string, d contract.Desired) (int, contract.Response, error) {
	resource, observers := provider.Process()
	server, err := newBlockServer(Block{Manifest: in.Manifest, Types: in.Types, Handler: resource, Observers: observers})
	if err != nil {
		return 0, contract.Response{}, err
	}
	status, resp, err := server.send(operation, d)
	if err != nil {
		return status, resp, fmt.Errorf("%s could not be sent or its answer read: %v", operation, err)
	}
	return status, resp, nil
}
