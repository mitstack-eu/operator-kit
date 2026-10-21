// The idempotency cache replays only a settled answer under the same subscription and key, and stores no secret.
package handler_test

import (
	"bytes"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/handler"
	"github.com/mitstack-eu/operator-kit/v3/kittest"
)

// delivery is one webhook delivery of a row: between runs first, replays names the earlier delivery whose exact answer it must get, or -1, and calls are the Resource's totals after it.
type delivery struct {
	fields  map[string]any
	between func(f *kittest.FakeResource)
	replays int
	differs bool
	status  int
	calls   [3]int
	check   func(t *testing.T, resp contract.Response)
}

func wantPending(want bool) func(*testing.T, contract.Response) {
	return func(t *testing.T, resp contract.Response) {
		if resp.Pending != want {
			t.Errorf("pending = %v, want %v; statusDetail %q", resp.Pending, want, resp.StatusDetail)
		}
	}
}

func wantAbsentAnswer(want bool) func(*testing.T, contract.Response) {
	return func(t *testing.T, resp contract.Response) { wantAbsent(t, resp, want) }
}

func TestServe_IdempotencyCache(t *testing.T) {
	key := func(op, k, sub string) map[string]any {
		return map[string]any{"operation": op, "idempotencyKey": k, "subscriptionId": sub, "secrets": guardSecrets()}
	}
	inOrg := func(fields map[string]any, org string) map[string]any { fields["orgId"] = org; return fields }
	settled := func(t *testing.T, resp contract.Response) {
		if resp.Pending || resp.Outputs["endpoint"] != "https://ready" {
			t.Errorf("pending, outputs = %v, %v; want the settled observation's outputs", resp.Pending, resp.Outputs)
		}
	}
	cases := []struct {
		name       string
		f          *kittest.FakeResource
		config     []func(*handler.Config)
		deliveries []delivery
	}{
		{name: "the same key replays the first answer without calling the Resource", f: &kittest.FakeResource{},
			deliveries: []delivery{
				{fields: key("subscribe", "idem-1", "sub-1"), replays: -1, status: http.StatusOK, calls: [3]int{1, 1, 0}},
				{fields: key("subscribe", "idem-1", "sub-1"), replays: 0, status: http.StatusOK, calls: [3]int{1, 1, 0}},
			}},
		{name: "a different key is a distinct request", f: &kittest.FakeResource{},
			deliveries: []delivery{
				{fields: key("subscribe", "idem-A", "sub-1"), replays: -1, status: http.StatusOK, calls: [3]int{1, 1, 0}},
				{fields: key("subscribe", "idem-B", "sub-1"), replays: -1, status: http.StatusOK, calls: [3]int{2, 2, 0}},
			}},
		{name: "an empty key is never cached", f: &kittest.FakeResource{},
			deliveries: []delivery{
				{fields: key("subscribe", "", "sub-1"), replays: -1, status: http.StatusOK, calls: [3]int{1, 1, 0}},
				{fields: key("subscribe", "", "sub-1"), replays: -1, status: http.StatusOK, calls: [3]int{2, 2, 0}},
			}},
		{name: "a key is cached per subscription across tenants, and each subscription still replays its own", f: &kittest.FakeResource{},
			deliveries: []delivery{
				{fields: inOrg(key("subscribe", "idem-shared", "sub-A"), "org-A"), replays: -1, status: http.StatusOK, calls: [3]int{1, 1, 0}},
				{fields: inOrg(key("subscribe", "idem-shared", "sub-B"), "org-B"), replays: -1, status: http.StatusOK, calls: [3]int{2, 2, 0}},
				{fields: inOrg(key("subscribe", "idem-shared", "sub-A"), "org-A"), replays: 0, status: http.StatusOK, calls: [3]int{2, 2, 0}},
			}},
		{name: "a 500 is never cached, so the retry reaches the Resource and answers its own success", f: &kittest.FakeResource{EnsureErr: errors.New("transient backend error")},
			deliveries: []delivery{
				{fields: key("subscribe", "idem-500", "sub-1"), replays: -1, status: http.StatusInternalServerError, calls: [3]int{1, 0, 0}},
				{fields: key("subscribe", "idem-500", "sub-1"), between: func(f *kittest.FakeResource) { f.EnsureErr = nil }, replays: -1, differs: true,
					status: http.StatusOK, calls: [3]int{2, 1, 0}, check: func(t *testing.T, resp contract.Response) {
						if resp.Error != "" {
							t.Errorf("retry error = %q, want the second attempt's own success", resp.Error)
						}
					}},
			}},
		{name: "a settled subscribe is replayed", f: &kittest.FakeResource{Outputs: map[string]string{"endpoint": "https://ready"}},
			deliveries: []delivery{
				{fields: key("subscribe", "idem-settled", "sub-1"), replays: -1, status: http.StatusOK, calls: [3]int{1, 1, 0}, check: settled},
				{fields: key("subscribe", "idem-settled", "sub-1"), replays: 0, status: http.StatusOK, calls: [3]int{1, 1, 0}},
			}},
		{name: "a refusal is deterministic, so it is replayed", f: &kittest.FakeResource{EnsureErr: handler.Refuse("quota-exceeded", "org quota exhausted")},
			deliveries: []delivery{
				{fields: key("subscribe", "idem-refused", "sub-1"), replays: -1, status: http.StatusBadRequest, calls: [3]int{1, 0, 0}},
				{fields: key("subscribe", "idem-refused", "sub-1"), replays: 0, status: http.StatusBadRequest, calls: [3]int{1, 0, 0}},
			}},
		{name: "a validate is never cached, since a probe must observe the current state", f: &kittest.FakeResource{Count: 1},
			deliveries: []delivery{
				{fields: key("validate", "idem-validate", "sub-1"), replays: -1, status: http.StatusOK, calls: [3]int{0, 1, 0}, check: wantAbsentAnswer(false)},
				{fields: key("validate", "idem-validate", "sub-1"), between: func(f *kittest.FakeResource) { f.Count = 0 }, replays: -1,
					status: http.StatusOK, calls: [3]int{0, 2, 0}, check: wantAbsentAnswer(true)},
			}},
		{name: "a pending subscribe is never cached, so the subscribe re-driven once validate reports the work finished runs Ensure once and answers the converged resource",
			f: &kittest.FakeResource{ObserveState: stateOf(contract.Absent), Outputs: map[string]string{"endpoint": "https://ready"}},
			deliveries: []delivery{
				{fields: key("subscribe", "idem-pending-subscribe", "sub-1"), replays: -1, status: http.StatusOK, calls: [3]int{1, 3, 0}, check: wantPending(true)},
				{fields: key("validate", "idem-settle-validate", "sub-1"), between: func(f *kittest.FakeResource) { f.ObserveState, f.Redrive = stateOf(contract.Unknown), true },
					replays: -1, status: http.StatusOK, calls: [3]int{1, 4, 0}, check: wantPending(true)},
				{fields: key("subscribe", "idem-pending-subscribe", "sub-1"), between: func(f *kittest.FakeResource) { f.ObserveState = nil }, replays: -1, differs: true,
					status: http.StatusOK, calls: [3]int{2, 5, 0}, check: settled},
			}},
		{name: "a pending upgrade is never cached, so the re-delivery answers the converged resource",
			f: &kittest.FakeResource{ObserveState: stateOf(contract.Absent), Outputs: map[string]string{"endpoint": "https://ready"}},
			deliveries: []delivery{
				{fields: key("upgrade", "idem-pending-upgrade", "sub-1"), replays: -1, status: http.StatusOK, calls: [3]int{1, 3, 0}, check: wantPending(true)},
				{fields: key("upgrade", "idem-pending-upgrade", "sub-1"), between: func(f *kittest.FakeResource) { f.ObserveState = nil }, replays: -1, differs: true,
					status: http.StatusOK, calls: [3]int{2, 4, 0}, check: settled},
			}},
		{name: "an unsubscribe answered absent false is never cached, so the re-delivery observes again", f: &kittest.FakeResource{Count: 1, ObserveState: stateOf(contract.Present)},
			config: []func(*handler.Config){withBudget(2)},
			deliveries: []delivery{
				{fields: key("unsubscribe", "idem-unsub-present", "sub-1"), replays: -1, status: http.StatusOK, calls: [3]int{0, 2, 1}, check: wantAbsentAnswer(false)},
				{fields: key("unsubscribe", "idem-unsub-present", "sub-1"), replays: -1, status: http.StatusOK, calls: [3]int{0, 4, 2}, check: wantAbsentAnswer(false)},
			}},
		{name: "an unsubscribe settled absent is replayed without observing", f: &kittest.FakeResource{Count: 1},
			deliveries: []delivery{
				{fields: key("unsubscribe", "idem-unsub-absent", "sub-1"), replays: -1, status: http.StatusOK, calls: [3]int{0, 1, 1}, check: wantAbsentAnswer(true)},
				{fields: key("unsubscribe", "idem-unsub-absent", "sub-1"), replays: 0, status: http.StatusOK, calls: [3]int{0, 1, 1}},
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logged := captureLog(t)
			x := newHarness(t, tc.f, tc.config...)
			var bodies [][]byte

			for i, d := range tc.deliveries {
				if d.between != nil {
					d.between(tc.f)
				}

				status, raw := x.post(d.fields)

				if status != d.status {
					t.Fatalf("delivery %d: status = %d, want %d, body: %s", i, status, d.status, raw)
				}
				assertConforms(t, raw)
				assertCalls(t, tc.f, d.calls)
				if d.replays >= 0 && !bytes.Equal(raw, bodies[d.replays]) {
					t.Errorf("delivery %d body = %s, want delivery %d's exact body %s", i, raw, d.replays, bodies[d.replays])
				}
				for j, earlier := range bodies {
					if d.differs && bytes.Equal(raw, earlier) {
						t.Errorf("delivery %d body is byte-identical to delivery %d's %s, so it was replayed", i, j, earlier)
					}
				}
				if d.check != nil {
					d.check(t, decodeResponse(t, raw))
				}
				kittest.AssertNoSecret(t, "a response body", string(raw), guardSecret)
				bodies = append(bodies, raw)
			}
			kittest.AssertNoSecret(t, "log output", logged.String(), guardSecret)
		})
	}
}

type served struct {
	status int
	body   []byte
}

// startConcurrent delivers fields twice to one handler, the second once the first holds the Resource, and returns the answers and whether the second reached the Resource within wait.
func startConcurrent(t *testing.T, r *blockingResource, fields map[string]any, wait time.Duration) (first, second served, reached bool) {
	t.Helper()
	x := newHarness(t, r)
	reqA, reqB := x.request(fields), x.request(fields)
	results := make(chan served, 2)
	deliver := func(req *http.Request) {
		status, raw := x.serve(req)
		results <- served{status, raw}
	}

	go deliver(reqA)
	select {
	case <-r.entered:
	case <-time.After(5 * time.Second):
		close(r.release)
		t.Fatal("the first delivery never reached the Resource")
	}
	go deliver(reqB)
	select {
	case <-r.entered:
		reached = true
	case <-time.After(wait):
	}
	close(r.release)
	return <-results, <-results, reached
}

// Meaningful under -race: every field blockingResource shares across goroutines is a channel or mutex-guarded, so a race can only come from the cache.
func TestServe_TwoConcurrentDeliveriesOfOneKeyShareOneEnsureAndItsAnswer(t *testing.T) {
	r := newBlockingResource()

	first, second, reached := startConcurrent(t, r, map[string]any{"idempotencyKey": "idem-sf", "subscriptionId": "sub-1"}, 150*time.Millisecond)

	if reached {
		t.Error("a second concurrent delivery of the same key reached Ensure before the first one returned")
	}
	if ensures, _ := r.counts(); ensures != 1 {
		t.Errorf("Ensure called %d times, want exactly 1 for two concurrent deliveries of the same key", ensures)
	}
	if first.status != second.status || !bytes.Equal(first.body, second.body) {
		t.Errorf("concurrent deliveries got %d %s and %d %s, want one answer", first.status, first.body, second.status, second.body)
	}
}

func TestServe_AConcurrentValidateNeverJoinsAnInFlightCall(t *testing.T) {
	r := newBlockingResource()

	_, _, reached := startConcurrent(t, r, map[string]any{"operation": "validate", "idempotencyKey": "idem-validate-sf", "subscriptionId": "sub-1"}, 2*time.Second)

	if !reached {
		t.Error("a second validate of the same key did not reach Observe while the first was in flight")
	}
	if _, observes := r.counts(); observes != 2 {
		t.Errorf("Observe called %d times, want 2: each validate observes the resource itself", observes)
	}
}
