package credrefresh_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/credrefresh"
)

// cred is a minimal stand-in credential. It deliberately carries no provider
// shape, because the Refresher must hold any credential type, not only AWS
// credentials.
type cred struct {
	id string
}

// --- Reuse and renewal -------------------------------------------------------

func TestRefresher_ReusesCredentialUntilItNearsExpiry(t *testing.T) {
	var calls int
	clock := time.Now()
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
		calls++
		return cred{id: "A"}, clock.Add(time.Hour), nil
	})
	r.Now = func() time.Time { return clock }

	for range 3 {
		if _, err := r.Get(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("resolved %d times, want 1 — a valid credential well outside the renewal margin must not be re-minted per call", calls)
	}
}

func TestRefresher_RenewsInsideTheRenewalMarginButNotYetExpired(t *testing.T) {
	var calls int
	clock := time.Now()
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
		calls++
		return cred{id: "A"}, clock.Add(time.Hour), nil
	})
	r.Now = func() time.Time { return clock }

	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Inside the renewal margin but not yet expired: renewing here, rather than
	// handing out a credential that will die mid-operation, is the point of the
	// margin at all.
	r.Now = func() time.Time { return clock.Add(time.Hour - credrefresh.DefaultRenewMargin/2) }
	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("resolved %d times, want 2 — a credential inside the renewal margin must be renewed", calls)
	}
}

func TestRefresher_ZeroValuedCredentialWithAGoodExpiryIsStillHeldAndReused(t *testing.T) {
	// A generic Refresher cannot probe presence off a field of T the way
	// cloudid's AWS-specific version probes AccessKeyID — there is no universal
	// "empty" sentinel for an arbitrary credential type. So a resolver minting
	// the zero value of T, alongside a good expiry, must still count as a held,
	// reusable credential rather than as "nothing held yet".
	var calls int
	clock := time.Now()
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
		calls++
		return cred{}, clock.Add(time.Hour), nil
	})
	r.Now = func() time.Time { return clock }

	for range 3 {
		if _, err := r.Get(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("resolved %d times, want 1 — a zero-valued credential with a good expiry is still a held credential", calls)
	}
}

// --- Fail closed --------------------------------------------------------------

func TestRefresher_FailsClosedWhenRenewalFails(t *testing.T) {
	// The headline guarantee: a reconcile loop acting on a dead credential does
	// not merely error, it can take a wrong or destructive action. So a
	// credential past expiry that cannot be renewed yields an error and the
	// zero value, never the stale one.
	clock := time.Now()
	var fail bool
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
		if fail {
			return cred{}, time.Time{}, errors.New("network down")
		}
		return cred{id: "A"}, clock.Add(time.Hour), nil
	})
	r.Now = func() time.Time { return clock }

	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fail = true
	clock = clock.Add(2 * time.Hour) // past the held credential's expiry

	got, err := r.Get(context.Background())
	if err == nil {
		t.Fatal("a credential past expiry that cannot be renewed MUST fail, never fall back to the stale one")
	}
	var zero cred
	if got != zero {
		t.Errorf("Get returned %+v alongside the error — the caller must get nothing usable, only the zero value", got)
	}

	// The held credential must be cleared, not merely masked behind the error:
	// a later successful resolve must re-mint rather than silently reusing the
	// credential that just failed to renew.
	fail = false
	got, err = r.Get(context.Background())
	if err != nil {
		t.Fatalf("unexpected error on recovery: %v", err)
	}
	if got.id != "A" {
		t.Errorf("Get = %+v after recovery, want the freshly minted credential", got)
	}
}

func TestRefresher_FailsClosedOnAnAlreadyExpiredFreshCredential(t *testing.T) {
	// Not hypothetical: a cached response from the credential issuer, or clock
	// skew between the operator and the issuer, both hand back a credential
	// that is already dead on arrival. Without this check it sails straight
	// through to the caller.
	clock := time.Now()
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
		return cred{id: "A"}, clock.Add(-time.Minute), nil
	})
	r.Now = func() time.Time { return clock }

	got, err := r.Get(context.Background())
	if err == nil {
		t.Fatal("a resolver handing back a credential already expired must fail closed, not pass it through")
	}
	var zero cred
	if got != zero {
		t.Errorf("Get returned %+v alongside the error — the caller must get nothing usable", got)
	}
}

func TestRefresher_FailsClosedWhenFreshCredentialExpiresExactlyNow(t *testing.T) {
	clock := time.Now()
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
		return cred{id: "A"}, clock, nil
	})
	r.Now = func() time.Time { return clock }

	if _, err := r.Get(context.Background()); err == nil {
		t.Fatal("a credential expiring exactly at now has no usable life left and must fail closed, not be treated as valid")
	}
}

func TestRefresher_RenewalPastExpiryYieldsAUsableCredential(t *testing.T) {
	// The ordinary case: past expiry, the resolver mints against the current
	// clock, and Get hands back something with life left in it.
	clock := time.Now()
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
		return cred{id: "A"}, clock.Add(3 * time.Hour), nil
	})
	r.Now = func() time.Time { return clock }
	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clock = clock.Add(2 * time.Hour)
	got, err := r.Get(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.id != "A" {
		t.Errorf("Get returned %+v, want the renewed credential", got)
	}
}

func TestRefresher_ZeroExpiryIsNeverCached(t *testing.T) {
	// An unknown expiry must not be read as "never expires" — that is the
	// fail-open reading, and it is the one that lets a dead credential through.
	var calls int
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
		calls++
		return cred{id: "A"}, time.Time{}, nil
	})
	for range 3 {
		if _, err := r.Get(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if calls != 3 {
		t.Errorf("resolved %d times, want 3 — a credential with an unknown (zero) expiry must never be served from cache", calls)
	}
}

// --- RenewMargin ---------------------------------------------------------------

func TestRefresher_DefaultRenewMarginAppliesWhenUnset(t *testing.T) {
	var calls int
	clock := time.Now()
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
		calls++
		return cred{id: "A"}, clock.Add(time.Hour), nil
	})
	r.Now = func() time.Time { return clock }
	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	r.Now = func() time.Time { return clock.Add(time.Hour - credrefresh.DefaultRenewMargin/2) }
	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("resolved %d times, want 2 — RenewMargin left zero must fall back to DefaultRenewMargin", calls)
	}
}

func TestRefresher_NonZeroRenewMarginOverridesTheDefault(t *testing.T) {
	var calls int
	clock := time.Now()
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
		calls++
		return cred{id: "A"}, clock.Add(time.Hour), nil
	})
	r.Now = func() time.Time { return clock }
	r.RenewMargin = time.Minute
	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Well outside a one-minute margin; DefaultRenewMargin's ten minutes would
	// have forced a renewal here, so this proves the override is honoured
	// rather than merely widening the default.
	r.Now = func() time.Time { return clock.Add(time.Hour - 5*time.Minute) }
	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("resolved %d times, want 1 — a non-zero RenewMargin must override DefaultRenewMargin", calls)
	}
}

func TestRefresher_NegativeRenewMarginNeverServesAnExpiredCredential(t *testing.T) {
	// RenewMargin is an unvalidated exported field, and the cache branch is
	// now.Before(heldExpires.Add(-margin)) with no direct expiry check
	// alongside it. A negative margin pushes that "still fresh" boundary
	// PAST the real expiry — here by thirty minutes — so a credential ten
	// minutes past its actual expiry would still be served from cache. The
	// fix is unspecified here on purpose (rejecting a negative margin,
	// clamping it, or adding a direct expiry check all satisfy this): the
	// only thing pinned is the resulting behaviour.
	clock := time.Now()
	expiry := clock.Add(time.Hour)
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
		return cred{id: "A"}, expiry, nil
	})
	r.Now = func() time.Time { return clock }
	r.RenewMargin = -30 * time.Minute

	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error priming the cache: %v", err)
	}

	// Ten minutes past the credential's real expiry, but still inside the
	// window a -30m margin would flip into looking "fresh".
	clock = clock.Add(70 * time.Minute)
	got, err := r.Get(context.Background())
	if err == nil {
		t.Errorf("Get returned %+v with no error for a credential ten minutes past its real expiry under a negative RenewMargin — a negative margin must never serve an expired credential", got)
	}
}

func TestRefresher_ZeroValuedCredentialAtExactExpiryBoundaryFailsClosed(t *testing.T) {
	// The zero-value-credential caching path (see
	// TestRefresher_ZeroValuedCredentialWithAGoodExpiryIsStillHeldAndReused
	// above) gets no exemption from the exact-expiry rule either: serving
	// cred{} past its expiry is exactly as unsafe as serving any other value
	// past its expiry.
	clock := time.Now()
	expiry := clock.Add(time.Hour)
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
		return cred{}, expiry, nil
	})
	r.Now = func() time.Time { return clock }
	r.RenewMargin = time.Nanosecond // effectively no renewal margin

	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error priming the cache: %v", err)
	}

	clock = clock.Add(time.Hour) // exactly at expiry
	got, err := r.Get(context.Background())
	if err == nil {
		t.Errorf("Get returned %+v with no error for a zero-valued credential exactly at its expiry, want a fail-closed error", got)
	}
}

// --- Error wrapping --------------------------------------------------------

func TestRefresher_ErrorWrapsTheResolverError(t *testing.T) {
	sentinel := errors.New("network down")
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
		return cred{}, time.Time{}, sentinel
	})

	_, err := r.Get(context.Background())
	if err == nil {
		t.Fatal("expected an error from a resolver that always fails")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("Get error %v does not wrap the resolver's sentinel error — errors.Is must reach it", err)
	}
	if !strings.Contains(err.Error(), sentinel.Error()) {
		t.Errorf("Get error %q does not carry the resolver's message text, which a caller's classifier still matches on", err.Error())
	}
}

// --- Concurrency -------------------------------------------------------------

func TestRefresher_SerialisesConcurrentGetCalls(t *testing.T) {
	var calls int64
	clock := time.Now()
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
		atomic.AddInt64(&calls, 1)
		return cred{id: "A"}, clock.Add(time.Hour), nil
	})
	r.Now = func() time.Time { return clock }

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Get(context.Background()); err != nil {
				t.Errorf("unexpected error from a concurrent Get: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Errorf("resolved %d times across 50 concurrent Get calls, want 1 — Get must serialise access to the held credential", got)
	}
}
