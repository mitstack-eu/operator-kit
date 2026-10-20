package health_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/health"
)

func TestStatus_ZeroValueIsConverging(t *testing.T) {
	var s health.Status

	if s != health.Converging {
		t.Errorf("the zero Status = %v, want Converging", s)
	}
}

func TestHealer_Run(t *testing.T) {
	probe := func(s health.Status, err error) health.Check {
		return func(context.Context) (health.Status, error) { return s, err }
	}
	cases := []struct {
		name      string
		check     health.Check
		max, runs int
		wantSt    health.Status
		wantDid   bool
		wantErr   bool
		wantCalls int
		lastErrIs error
	}{
		{"a healthy resource is not remediated", probe(health.Healthy, nil), 0, 1, health.Healthy, false, false, 0, nil},
		{"a converging resource is never remediated", probe(health.Converging, nil), 0, 1, health.Converging, false, false, 0, nil},
		{"a dead resource is remediated and reported as observed before remediation", probe(health.Dead, nil), 2, 1, health.Dead, true, false, 1, nil},
		{"remediation stops at MaxAttempts and then says why", probe(health.Dead, nil), 2, 6, health.Dead, true, false, 2, health.ErrRemediationExhausted},
		{"zero MaxAttempts means one attempt, not unlimited", probe(health.Dead, nil), 0, 4, health.Dead, true, false, 1, nil},
		{"a probe failure surfaces as converging, never as dead", probe(health.Dead, errors.New("api timeout")), 0, 1, health.Converging, false, true, 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := &health.Healer{Check: tc.check, MaxAttempts: tc.max, Remediate: func(context.Context) error {
				calls++
				return nil
			}}

			st, did, err := h.Run(context.Background())
			var lastErr error
			for range tc.runs - 1 {
				_, _, lastErr = h.Run(context.Background())
			}

			if st != tc.wantSt || did != tc.wantDid || (err != nil) != tc.wantErr {
				t.Errorf("first Run = %v, %v, %v; want %v, %v, error %v", st, did, err, tc.wantSt, tc.wantDid, tc.wantErr)
			}
			if calls != tc.wantCalls {
				t.Errorf("remediated %d times over %d runs (MaxAttempts %d), want %d", calls, tc.runs, tc.max, tc.wantCalls)
			}
			if tc.lastErrIs != nil && !errors.Is(lastErr, tc.lastErrIs) {
				t.Errorf("last Run error = %v, want %v so the caller can report why it stopped", lastErr, tc.lastErrIs)
			}
		})
	}
}

func TestPromote(t *testing.T) {
	placeholder := health.Content{Ref: "placeholder", IsPlaceholder: true}
	cases := []struct {
		name                         string
		current, lastGood, candidate health.Content
		wantRef                      string
		wantErr                      error
	}{
		{"a placeholder never replaces serving content", health.Content{Ref: "sha-real", Serving: true}, health.Content{}, placeholder, "", health.ErrWouldReplaceServingContent},
		{"a recreated resource comes back at last-good", health.Content{}, health.Content{Ref: "sha-lastgood"}, placeholder, "sha-lastgood", nil},
		{"the placeholder is fine on a genuinely new resource", health.Content{}, health.Content{}, placeholder, "placeholder", nil},
		{"real content always wins", health.Content{Ref: "sha-old", Serving: true}, health.Content{Ref: "sha-lastgood"}, health.Content{Ref: "sha-new"}, "sha-new", nil},
		{"a placeholder may replace a placeholder", health.Content{Ref: "placeholder", IsPlaceholder: true, Serving: true}, health.Content{},
			health.Content{Ref: "placeholder-v2", IsPlaceholder: true}, "placeholder-v2", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := health.Promote(tc.current, tc.lastGood, tc.candidate)

			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && got.Ref != tc.wantRef) {
				t.Errorf("Promote(%+v, %+v, %+v) = %+v, %v; want ref %q, error %v", tc.current, tc.lastGood, tc.candidate, got, err, tc.wantRef, tc.wantErr)
			}
		})
	}
}
