package credrefresh_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/credrefresh"
)

type cred struct {
	id string
}

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
	r.Now = func() time.Time { return clock.Add(time.Hour - credrefresh.DefaultRenewMargin/2) }
	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("resolved %d times, want 2 — a credential inside the renewal margin must be renewed", calls)
	}
}

func TestRefresher_ZeroValuedCredentialWithAGoodExpiryIsStillHeldAndReused(t *testing.T) {
	// There is no universal "empty" sentinel for an arbitrary credential type, so a zero T with a good expiry is still held.
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

func TestRefresher_FailsClosedWhenRenewalFails(t *testing.T) {
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

	// The held credential must be cleared, so a later resolve re-mints rather than reusing the one that failed to renew.
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
	// A cached issuer response or clock skew can hand back a credential already dead on arrival.
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
	// An unknown expiry must not be read as "never expires", which is the fail-open reading.
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

	// DefaultRenewMargin's ten minutes would force a renewal here, so this proves the override is honoured.
	r.Now = func() time.Time { return clock.Add(time.Hour - 5*time.Minute) }
	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("resolved %d times, want 1 — a non-zero RenewMargin must override DefaultRenewMargin", calls)
	}
}

func TestRefresher_NegativeRenewMarginNeverServesAnExpiredCredential(t *testing.T) {
	// RenewMargin is unvalidated, and a negative one pushes the "still fresh" boundary past the real expiry.
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

	clock = clock.Add(70 * time.Minute)
	got, err := r.Get(context.Background())
	if err == nil {
		t.Errorf("Get returned %+v with no error for a credential ten minutes past its real expiry under a negative RenewMargin — a negative margin must never serve an expired credential", got)
	}
}

func TestRefresher_ZeroValuedCredentialAtExactExpiryBoundaryFailsClosed(t *testing.T) {
	// A zero-valued credential gets no exemption from the exact-expiry rule.
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
