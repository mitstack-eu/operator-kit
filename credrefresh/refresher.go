// Package credrefresh caches a short-lived credential and renews it ahead of
// expiry, for any credential type at all.
//
// It exists because MIT-889 built this mechanism once, inside a single
// provider-specific package, for that provider's assumed-role credentials —
// and then a second provider needed the exact same reconcile-loop-outlives-
// the-token shape with a different credential type. A generic package is what
// keeps the renewal logic in one place instead of near-identical copies
// drifting apart. It deliberately is NOT a provider client: it holds whatever
// a Resolve function hands it and knows nothing about how that value was
// minted, or which provider minted it.
//
// It fails closed. When the held credential has expired, or a fresh one comes
// back already expired, or the resolver itself errors, Get returns the zero
// value and an error — never a stale credential. That property came out of two
// real incidents (MIT-412, MIT-889): a reconcile loop that outlives the
// credential it started with does not merely error when handed a dead one, it
// can take a wrong or destructive action against a live account. "Return the
// last known good value" is exactly how that happens, so this package never
// does it.
package credrefresh

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DefaultRenewMargin is how long before expiry a Refresher renews, unless
// overridden. It is generous because the failure it avoids — a credential
// that dies partway through a run — is far more expensive than an early
// renewal.
const DefaultRenewMargin = 10 * time.Minute

// Resolve mints a fresh credential. A zero time.Time expiry means unknown,
// and is treated as unusable for caching — never as "never expires".
type Resolve[T any] func(context.Context) (T, time.Time, error)

// Refresher holds a credential for work that outlives a single resolve, and
// renews it ahead of expiry.
type Refresher[T any] struct {
	// RenewMargin overrides DefaultRenewMargin when non-zero. Set it before
	// the first Get call: it is read under the mutex inside Get but written
	// without one, so mutating it concurrently with an in-flight Get is a
	// data race.
	RenewMargin time.Duration
	// Now is injectable so expiry behaviour is testable without sleeping. It
	// is read fresh on every Get call, and defaults to time.Now when nil. Set
	// it before the first Get call, for the same reason as RenewMargin.
	Now func() time.Time

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

// Get returns a usable credential, renewing when none is held, the held one
// is inside the renewal margin, or its expiry is unknown. It never returns an
// expired credential.
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

	// A zero expiry means UNKNOWN. Treating unknown as "never expires" is the
	// fail-open reading, and it is the one that lets a dead credential through.
	// now.Before(heldExpires) is checked directly, not only through the
	// margin-shifted deadline, so a caller-supplied margin can never itself
	// become the reason an expired credential is served.
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

	// Failing closed means the caller never receives something unusable — which
	// includes a credential the RESOLVER handed back already expired. A cached
	// response from the credential issuer, or clock skew between the caller and
	// the issuer, both produce exactly this.
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
