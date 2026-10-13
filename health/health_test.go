package health_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/health"
)

func check(s health.Status) health.Check {
	return func(context.Context) (health.Status, error) { return s, nil }
}

func TestStatus_ZeroValueIsConverging(t *testing.T) {
	var s health.Status
	if s != health.Converging {
		t.Errorf("the zero Status = %v, want Converging", s)
	}
}

func TestRun_HealthyIsNotRemediated(t *testing.T) {
	var remediated bool
	h := &health.Healer{Check: check(health.Healthy), Remediate: func(context.Context) error {
		remediated = true
		return nil
	}}

	st, did, err := h.Run(context.Background())
	if err != nil || st != health.Healthy || did || remediated {
		t.Errorf("st=%v did=%v remediated=%v err=%v", st, did, remediated, err)
	}
}

func TestRun_ConvergingIsNotRemediated(t *testing.T) {
	var remediated bool
	h := &health.Healer{Check: check(health.Converging), Remediate: func(context.Context) error {
		remediated = true
		return nil
	}}

	_, did, err := h.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if did || remediated {
		t.Error("a converging resource must never be remediated")
	}
}

func TestRun_DeadIsRemediated(t *testing.T) {
	var remediated bool
	h := &health.Healer{Check: check(health.Dead), MaxAttempts: 2, Remediate: func(context.Context) error {
		remediated = true
		return nil
	}}

	st, did, err := h.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st != health.Dead {
		t.Errorf("Run must report the status observed BEFORE remediation, got %v", st)
	}
	if !did || !remediated {
		t.Error("a dead resource should be remediated")
	}
}

func TestRun_RemediationIsBounded(t *testing.T) {
	var calls int
	h := &health.Healer{Check: check(health.Dead), MaxAttempts: 2, Remediate: func(context.Context) error {
		calls++
		return nil
	}}

	for range 5 {
		_, _, _ = h.Run(context.Background())
	}

	if calls != 2 {
		t.Errorf("remediated %d times, want the 2-attempt cap honoured", calls)
	}
	if _, _, err := h.Run(context.Background()); !errors.Is(err, health.ErrRemediationExhausted) {
		t.Errorf("err = %v, want ErrRemediationExhausted so the caller can report why it stopped", err)
	}
}

func TestRun_ZeroMaxAttemptsMeansOneNotUnlimited(t *testing.T) {
	var calls int
	h := &health.Healer{Check: check(health.Dead), Remediate: func(context.Context) error {
		calls++
		return nil
	}}

	for range 4 {
		_, _, _ = h.Run(context.Background())
	}
	if calls != 1 {
		t.Errorf("remediated %d times with MaxAttempts=0, want exactly 1", calls)
	}
}

func TestRun_AProbeFailureIsConvergingNotDead(t *testing.T) {
	// Not determining health must not be concluded as death, which triggers a destructive action.
	h := &health.Healer{
		Check:     func(context.Context) (health.Status, error) { return health.Dead, errors.New("api timeout") },
		Remediate: func(context.Context) error { t.Fatal("must not remediate on an unreadable probe"); return nil },
	}

	st, did, err := h.Run(context.Background())
	if err == nil {
		t.Fatal("a probe failure must surface")
	}
	if st != health.Converging || did {
		t.Errorf("st=%v did=%v — an unreadable probe must not be treated as dead", st, did)
	}
}

func TestPromote_APlaceholderNeverReplacesServingContent(t *testing.T) {
	current := health.Content{Ref: "sha-real", Serving: true}
	candidate := health.Content{Ref: "placeholder", IsPlaceholder: true}

	_, err := health.Promote(current, health.Content{}, candidate)
	if !errors.Is(err, health.ErrWouldReplaceServingContent) {
		t.Fatalf("err = %v, want ErrWouldReplaceServingContent", err)
	}
}

func TestPromote_ARecreatedResourceComesBackAtLastGood(t *testing.T) {
	current := health.Content{} // freshly recreated, serving nothing
	lastGood := health.Content{Ref: "sha-lastgood"}
	candidate := health.Content{Ref: "placeholder", IsPlaceholder: true}

	got, err := health.Promote(current, lastGood, candidate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Ref != "sha-lastgood" {
		t.Errorf("promoted %q, want the last-good content", got.Ref)
	}
}

func TestPromote_ThePlaceholderIsFineOnAGenuinelyNewResource(t *testing.T) {
	got, err := health.Promote(health.Content{}, health.Content{}, health.Content{Ref: "placeholder", IsPlaceholder: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Ref != "placeholder" {
		t.Errorf("promoted %q, want the placeholder on a genuinely new resource", got.Ref)
	}
}

func TestPromote_RealContentAlwaysWins(t *testing.T) {
	current := health.Content{Ref: "sha-old", Serving: true}
	candidate := health.Content{Ref: "sha-new"}

	got, err := health.Promote(current, health.Content{Ref: "sha-lastgood"}, candidate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Ref != "sha-new" {
		t.Errorf("promoted %q, want the real new content", got.Ref)
	}
}

func TestPromote_APlaceholderMayReplaceAPlaceholder(t *testing.T) {
	current := health.Content{Ref: "placeholder", IsPlaceholder: true, Serving: true}
	got, err := health.Promote(current, health.Content{}, health.Content{Ref: "placeholder-v2", IsPlaceholder: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Ref != "placeholder-v2" {
		t.Errorf("promoted %q", got.Ref)
	}
}
