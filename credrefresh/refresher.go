// Package credrefresh, an optional helper, caches a short-lived credential of any type and renews it ahead of expiry, failing closed rather than returning a stale one.
package credrefresh

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DefaultRenewMargin is how long before expiry a Refresher renews, unless overridden.
const DefaultRenewMargin = 10 * time.Minute

// Resolve mints a fresh credential; a zero expiry means unknown and is never cached.
type Resolve[T any] func(context.Context) (T, time.Time, error)

// Refresher holds a credential for work that outlives a single resolve, and renews it ahead of expiry.
type Refresher[T any] struct {
	// RenewMargin overrides DefaultRenewMargin when non-zero; set it before the first Get, since writing it concurrently with a Get is a data race.
	RenewMargin time.Duration
	Now         func() time.Time

	mu          sync.Mutex
	held        T
	heldExpires time.Time
	hasHeld     bool
	resolve     Resolve[T]
}

// New returns a Refresher that mints credentials through resolve.
func New[T any](resolve Resolve[T]) *Refresher[T] {
	return &Refresher[T]{resolve: resolve}
}

// Get returns a usable credential, renewing inside the renewal margin or when expiry is unknown, and never returns an expired one.
func (r *Refresher[T]) Get(ctx context.Context) (T, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	margin := r.RenewMargin
	if margin <= 0 {
		margin = DefaultRenewMargin
	}

	// A zero expiry means unknown, and now.Before(heldExpires) is checked directly so a negative margin cannot serve an expired credential.
	if r.hasHeld && !r.heldExpires.IsZero() && now.Before(r.heldExpires) && now.Before(r.heldExpires.Add(-margin)) {
		return r.held, nil
	}

	fresh, expires, err := r.resolve(ctx)
	if err != nil {
		r.hasHeld = false
		var zero T
		r.held = zero
		return zero, fmt.Errorf("credrefresh: resolving credential: %w", err)
	}

	if !expires.IsZero() && !now.Before(expires) {
		r.hasHeld = false
		var zero T
		r.held = zero
		return zero, fmt.Errorf("credrefresh: refreshed credential is already expired (expires %s, now %s) — failing closed",
			expires.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}

	r.held = fresh
	r.heldExpires = expires
	r.hasHeld = true
	return fresh, nil
}
