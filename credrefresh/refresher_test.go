package credrefresh_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/credrefresh"
	"github.com/mitstack-eu/operator-kit/v3/kittest"
)

const leakSecret = "PROVIDER-CREDENTIAL-MATERIAL-THAT-MUST-NEVER-SURFACE"

type cred struct {
	id, token string
}

type outcome int

const (
	usable outcome = iota
	refused
)

// get is one Refresher.Get at an offset from the start, with the outcome and credential id it must have.
type get struct {
	at   time.Duration
	want outcome
	id   string
}

// mint is a resolver for a credential expiring d after the moment it is minted.
func mint(c cred, d time.Duration) func(int, time.Time, time.Time) (cred, time.Time, error) {
	return func(_ int, _, now time.Time) (cred, time.Time, error) { return c, now.Add(d), nil }
}

// mintUntil is a resolver for a credential expiring d after the start, whenever it is minted.
func mintUntil(c cred, d time.Duration) func(int, time.Time, time.Time) (cred, time.Time, error) {
	return func(_ int, start, _ time.Time) (cred, time.Time, error) { return c, start.Add(d), nil }
}

func TestRefresher_Get(t *testing.T) {
	a := cred{id: "A"}
	cases := []struct {
		name      string
		margin    time.Duration
		resolve   func(call int, start, now time.Time) (cred, time.Time, error)
		gets      []get
		wantCalls int
		realClock bool
	}{
		{"a credential well outside the renewal margin is reused", 0, mint(a, time.Hour),
			[]get{{0, usable, "A"}, {0, usable, "A"}, {0, usable, "A"}}, 1, false},
		{"a credential inside the default renewal margin is renewed when RenewMargin is unset", 0, mintUntil(a, time.Hour),
			[]get{{0, usable, "A"}, {time.Hour - credrefresh.DefaultRenewMargin/2, usable, "A"}}, 2, false},
		{"a zero-valued credential with a good expiry is still held, since no type has a universal empty sentinel", 0, mint(cred{}, time.Hour),
			[]get{{0, usable, ""}, {0, usable, ""}, {0, usable, ""}}, 1, false},
		{"a renewal that fails past expiry fails closed and clears the held credential", 0, func(call int, _, now time.Time) (cred, time.Time, error) {
			if call == 2 {
				return cred{}, time.Time{}, errors.New("network down")
			}
			return a, now.Add(time.Hour), nil
		}, []get{{0, usable, "A"}, {2 * time.Hour, refused, ""}, {2 * time.Hour, usable, "A"}}, 3, false},
		{"a fresh credential already expired on arrival fails closed", 0, mint(a, -time.Minute), []get{{0, refused, ""}}, 1, false},
		{"a fresh credential expiring exactly now has no usable life left", 0, mint(a, 0), []get{{0, refused, ""}}, 1, false},
		{"a credential read hours later, still outside its renewal margin, is served usable", 0, mint(a, 3*time.Hour),
			[]get{{0, usable, "A"}, {2 * time.Hour, usable, "A"}}, 0, false},
		{"a credential of unknown expiry is never served from cache", 0, func(int, time.Time, time.Time) (cred, time.Time, error) {
			return a, time.Time{}, nil
		}, []get{{0, usable, "A"}, {0, usable, "A"}, {0, usable, "A"}}, 3, true},
		{"a non-zero RenewMargin overrides the default", time.Minute, mintUntil(a, time.Hour),
			[]get{{0, usable, "A"}, {time.Hour - 5*time.Minute, usable, "A"}}, 1, false},
		{"a negative RenewMargin never serves a credential past its real expiry", -30 * time.Minute, mintUntil(a, time.Hour),
			[]get{{0, usable, "A"}, {70 * time.Minute, refused, ""}}, 0, false},
		{"a zero-valued credential gets no exemption at its exact expiry", time.Nanosecond, mintUntil(cred{}, time.Hour),
			[]get{{0, usable, ""}, {time.Hour, refused, ""}}, 0, false},
		{"the fail-closed error carries no credential material", 0, mint(cred{token: leakSecret}, -time.Hour), []get{{0, refused, ""}}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			var now time.Time
			calls := 0
			r := credrefresh.New(func(context.Context) (cred, time.Time, error) {
				calls++
				return tc.resolve(calls, start, now)
			})
			r.RenewMargin = tc.margin

			for i, g := range tc.gets {
				now = start.Add(g.at)
				if !tc.realClock {
					r.Now = func() time.Time { return now }
				}

				got, err := r.Get(context.Background())

				switch {
				case g.want == usable && (err != nil || got.id != g.id):
					t.Fatalf("Get #%d at +%v = %+v, %v; want credential %q", i+1, g.at, got, err, g.id)
				case g.want == refused && err == nil:
					t.Fatalf("Get #%d at +%v = %+v; want a fail-closed error, never a stale credential", i+1, g.at, got)
				case g.want == refused:
					if got != (cred{}) {
						t.Errorf("Get #%d returned %+v alongside its error; want only the zero value", i+1, got)
					}
					kittest.AssertNoSecret(t, "the Refresher error", err.Error(), leakSecret)
				}
			}
			if tc.wantCalls != 0 && calls != tc.wantCalls {
				t.Errorf("resolved %d times, want %d", calls, tc.wantCalls)
			}
		})
	}
}

func TestRefresher_ErrorWrapsTheResolverError(t *testing.T) {
	sentinel := errors.New("network down")
	r := credrefresh.New(func(context.Context) (cred, time.Time, error) { return cred{}, time.Time{}, sentinel })

	_, err := r.Get(context.Background())

	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), sentinel.Error()) {
		t.Errorf("Get error = %v; want it to wrap %v and carry its text, which a caller's classifier matches on", err, sentinel)
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
				t.Errorf("concurrent Get: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Errorf("resolved %d times across 50 concurrent Get calls, want 1", got)
	}
}
