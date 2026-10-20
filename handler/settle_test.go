// The settle loops stop when the caller has gone, since defaultSleep returns at once on a done context and a cancelled request would fire its whole remaining budget unpaced.
package handler_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// cancelAfterNObserves never reports the target state, so with a budget far above cancelAfter a low call count can only mean the loop honoured cancellation.
type cancelAfterNObserves struct {
	mu          sync.Mutex
	calls       int
	cancelAfter int
	cancel      context.CancelFunc
	state       contract.State
}

func (c *cancelAfterNObserves) Ensure(context.Context, contract.Desired) error { return nil }

func (c *cancelAfterNObserves) Delete(context.Context, contract.Desired) (map[string]string, error) {
	return nil, nil
}

func (c *cancelAfterNObserves) Observe(context.Context, contract.Desired) (contract.Observation, error) {
	c.mu.Lock()
	c.calls++
	n := c.calls
	c.mu.Unlock()
	if n == c.cancelAfter {
		c.cancel()
	}
	return contract.Observation{State: c.state}, nil
}

func (c *cancelAfterNObserves) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestServe_SettleStopsEarlyWhenTheContextIsCancelled(t *testing.T) {
	const budget = 1000
	for op, never := range map[string]contract.State{"subscribe": contract.Absent, "unsubscribe": contract.Present} {
		t.Run(op, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			r := &cancelAfterNObserves{cancelAfter: 2, cancel: cancel, state: never}
			x := newHarness(t, r, withBudget(budget))
			req := x.request(map[string]any{"operation": op}).WithContext(ctx)
			done := make(chan struct{})

			go func() {
				x.serve(req)
				close(done)
			}()

			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Serve did not return after its context was cancelled: it kept spending the settle budget")
			}
			if calls := r.count(); calls >= budget {
				t.Errorf("Observe called %d times, want strictly under the %d budget", calls, budget)
			}
		})
	}
}
