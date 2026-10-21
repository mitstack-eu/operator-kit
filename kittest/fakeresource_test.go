// FakeResource matches handler.Resource structurally, so kittest stays importable from a handler test without a cycle.
package kittest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/kittest"
)

func testIdentity() contract.Identity {
	return contract.Identity{SubscriptionID: "sub-1", BlockID: "block-1", OrgID: "org-1"}
}

func testDesired() contract.Desired {
	return contract.Desired{Identity: testIdentity()}
}

// call is one verb on a FakeResource: op is ensure, observe or delete, state the observed state it must give when set, and err the error it must return.
type call struct {
	op    string
	state contract.State
	err   error
}

func TestFakeResource(t *testing.T) {
	unknown := contract.Unknown
	ensureErr, observeErr, deleteErr := errors.New("ensure failed"), errors.New("observe failed"), errors.New("delete failed")
	anyErr := errors.New("any error")
	cases := []struct {
		name  string
		fake  *kittest.FakeResource
		calls []call
		// check sees the fake, every observation and the Count after each call.
		check func(t *testing.T, f *kittest.FakeResource, obs []contract.Observation, counts []int)
	}{
		{"Observe reflects Count by default, absent until Ensure and again after Delete", &kittest.FakeResource{},
			[]call{{"observe", contract.Absent, nil}, {"ensure", "", nil}, {"observe", contract.Present, nil}, {"delete", "", nil}, {"observe", contract.Absent, nil}}, nil},
		{"ObserveState overrides Count", &kittest.FakeResource{Count: 1, ObserveState: &unknown}, []call{{"observe", contract.Unknown, nil}}, nil},
		{"DeadReportedPending answers Unknown with no error even with ObserveErr set",
			&kittest.FakeResource{DeadReportedPending: true, ObserveErr: errors.New("this would normally be a permanent failure")},
			[]call{{"observe", contract.Unknown, nil}}, nil},
		{"Observe reports the fake's outputs and status detail",
			&kittest.FakeResource{Outputs: map[string]string{"url": "https://example.test"}, StatusDetail: "all good"}, []call{{"observe", "", nil}},
			func(t *testing.T, _ *kittest.FakeResource, obs []contract.Observation, _ []int) {
				if obs[0].Outputs["url"] != "https://example.test" || obs[0].StatusDetail != "all good" {
					t.Errorf("Observe = %+v, want the fake's own url output and detail", obs[0])
				}
			}},
		{"Ensure, Observe and Delete return their configured errors", &kittest.FakeResource{EnsureErr: ensureErr, ObserveErr: observeErr, DeleteErr: deleteErr},
			[]call{{"ensure", "", ensureErr}, {"observe", "", observeErr}, {"delete", "", deleteErr}}, nil},
		{"DuplicateOnEnsure adds a resource on every call", &kittest.FakeResource{DuplicateOnEnsure: true}, []call{{"ensure", "", nil}, {"ensure", "", nil}},
			func(t *testing.T, _ *kittest.FakeResource, _ []contract.Observation, counts []int) {
				if counts[1] <= counts[0] {
					t.Errorf("Count after a retried Ensure = %d, want it grown past %d", counts[1], counts[0])
				}
			}},
		{"LeakOnDelete leaves a resource behind", &kittest.FakeResource{LeakOnDelete: true}, []call{{"ensure", "", nil}, {"delete", "", nil}},
			func(t *testing.T, f *kittest.FakeResource, _ []contract.Observation, _ []int) {
				if f.Count == 0 {
					t.Error("Count == 0 after Delete, want at least one resource left behind")
				}
			}},
		{"ErrorWhenAlreadyAbsent fails a Delete of nothing provisioned", &kittest.FakeResource{ErrorWhenAlreadyAbsent: true}, []call{{"delete", "", anyErr}}, nil},
		{"ClobberOnUpgrade drops prior outputs on a second Ensure only",
			&kittest.FakeResource{Outputs: map[string]string{"url": "https://example.test"}, ClobberOnUpgrade: true},
			[]call{{"ensure", "", nil}, {"observe", "", nil}, {"ensure", "", nil}, {"observe", "", nil}},
			func(t *testing.T, _ *kittest.FakeResource, obs []contract.Observation, _ []int) {
				if obs[0].Outputs["url"] == "" || obs[1].Outputs["url"] != "" {
					t.Errorf("outputs after the first and second Ensure = %v, %v; want kept, then dropped by the destructive upgrade", obs[0].Outputs, obs[1].Outputs)
				}
			}},
		{"the call counters record every call, an erroring one included", &kittest.FakeResource{EnsureErr: errors.New("boom")},
			[]call{{"ensure", "", anyErr}, {"ensure", "", anyErr}, {"observe", "", nil}, {"observe", "", nil}, {"observe", "", nil}, {"delete", "", nil}},
			func(t *testing.T, f *kittest.FakeResource, _ []contract.Observation, _ []int) {
				if got := [3]int{f.EnsureCalls, f.ObserveCalls, f.DeleteCalls}; got != [3]int{2, 3, 1} {
					t.Errorf("Ensure, Observe, Delete calls = %v, want [2 3 1]", got)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var obs []contract.Observation
			var counts []int

			for i, c := range tc.calls {
				var err error
				switch c.op {
				case "ensure":
					err = tc.fake.Ensure(ctx, testDesired())
				case "observe":
					var o contract.Observation
					o, err = tc.fake.Observe(ctx, testDesired())
					obs = append(obs, o)
					if c.state != "" && o.State != c.state {
						t.Errorf("call %d Observe state = %v, want %v", i+1, o.State, c.state)
					}
				case "delete":
					_, err = tc.fake.Delete(ctx, testDesired())
				}
				counts = append(counts, tc.fake.Count)
				if (c.err == anyErr && err == nil) || (c.err != anyErr && !errors.Is(err, c.err)) {
					t.Errorf("call %d %s error = %v, want %v", i+1, c.op, err, c.err)
				}
			}

			if tc.check != nil {
				tc.check(t, tc.fake, obs, counts)
			}
		})
	}
}
