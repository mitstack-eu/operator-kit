package kittest

import (
	"context"
	"errors"
	"sync"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// FakeResource is a Resource whose behaviour a test dictates, where the zero value conforms and each other field turns on one misbehaviour.
type FakeResource struct {
	mu sync.Mutex

	Count int
	// ObserveState overrides the state Observe reports, which defaults to Present when Count > 0 and Absent otherwise.
	ObserveState *contract.State
	Outputs      map[string]string
	StatusDetail string

	EnsureErr, ObserveErr, DeleteErr error

	DuplicateOnEnsure bool
	// LeakOnDelete leaves one resource behind on Delete.
	LeakOnDelete bool
	// ErrorWhenAlreadyAbsent makes Delete fail when nothing is provisioned.
	ErrorWhenAlreadyAbsent bool
	// ClobberOnUpgrade drops the pre-existing outputs on a second Ensure.
	ClobberOnUpgrade bool
	// DeadReportedPending makes Observe answer Unknown with no error, modelling an operator that never expresses deadness and lets a subscription wait forever.
	DeadReportedPending bool

	// EnsureCalls, ObserveCalls and DeleteCalls count calls whether or not each succeeded.
	EnsureCalls, ObserveCalls, DeleteCalls int
}

// Ensure converges Count to a provisioned resource, or adds one per call under DuplicateOnEnsure.
func (f *FakeResource) Ensure(ctx context.Context, d contract.Desired) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.EnsureCalls++

	if f.EnsureErr != nil {
		return f.EnsureErr
	}

	if f.ClobberOnUpgrade && f.EnsureCalls > 1 {
		f.Outputs = nil
	}

	if f.DuplicateOnEnsure {
		f.Count++
	} else if f.Count == 0 {
		f.Count = 1
	}
	return nil
}

// Observe checks DeadReportedPending ahead of ObserveErr and every other field.
func (f *FakeResource) Observe(ctx context.Context, d contract.Desired) (contract.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ObserveCalls++

	if f.DeadReportedPending {
		return contract.Observation{State: contract.Unknown, Outputs: f.Outputs, StatusDetail: f.StatusDetail}, nil
	}
	if f.ObserveErr != nil {
		return contract.Observation{}, f.ObserveErr
	}

	state := contract.Absent
	if f.Count > 0 {
		state = contract.Present
	}
	if f.ObserveState != nil {
		state = *f.ObserveState
	}
	return contract.Observation{State: state, Outputs: f.Outputs, StatusDetail: f.StatusDetail}, nil
}

// Delete treats absence as success unless ErrorWhenAlreadyAbsent is set.
func (f *FakeResource) Delete(ctx context.Context, d contract.Desired) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DeleteCalls++

	if f.DeleteErr != nil {
		return nil, f.DeleteErr
	}

	if f.Count == 0 {
		if f.ErrorWhenAlreadyAbsent {
			return nil, errors.New("kittest: FakeResource.Delete called with nothing provisioned (ErrorWhenAlreadyAbsent)")
		}
		return nil, nil
	}

	if f.LeakOnDelete {
		if f.Count > 1 {
			f.Count--
		}
		return nil, nil
	}

	f.Count = 0
	return nil, nil
}
