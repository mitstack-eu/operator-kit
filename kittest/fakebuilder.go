package kittest

import (
	"context"
	"sync"

	"github.com/mitstack-eu/operator-kit/v3/contract"
)

// FakeBuilder is a Resource and an Observer whose first Ensure starts a builder that runs until FinishBuild, and whose next Ensure turns the finished build into the image; the zero value conforms and each bool turns on one misbehaviour.
type FakeBuilder struct {
	mu               sync.Mutex
	finished, imaged bool

	// Builds counts the builders Ensure started.
	Builds int

	// RebuildOnRedrive makes Ensure start a new builder over a finished one rather than take the next step.
	RebuildOnRedrive bool
	// HideFinish makes Observe never report the finished step.
	HideFinish bool
}

func (f *FakeBuilder) FinishBuild() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished = true
}

func (f *FakeBuilder) Ensure(context.Context, contract.Desired) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.imaged:
	case f.Builds == 0, f.finished && f.RebuildOnRedrive:
		f.Builds++
		f.finished = false
	case f.finished:
		f.imaged = true
	}
	return nil
}

func (f *FakeBuilder) Observe(context.Context, contract.Desired) (contract.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.imaged:
		return contract.Observation{State: contract.Present}, nil
	case f.Builds == 0:
		return contract.Observation{State: contract.Absent}, nil
	}
	return contract.Observation{State: contract.Unknown, Redrive: f.finished && !f.HideFinish}, nil
}

func (f *FakeBuilder) ObserveResource(ctx context.Context, _ contract.ResourceRef) (contract.Observation, error) {
	return f.Observe(ctx, contract.Desired{})
}

func (f *FakeBuilder) Delete(context.Context, contract.Desired) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Builds, f.finished, f.imaged = 0, false, false
	return nil, nil
}
