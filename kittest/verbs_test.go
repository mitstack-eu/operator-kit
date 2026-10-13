package kittest_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// Each case breaks exactly one property, so the harness's verdict is asserted rather than trusted.

func conforming() kittest.VerbSeams {
	return kittest.VerbSeams{
		Subscribe:               func() (int, error) { return 3, nil },
		Unsubscribe:             func() (int, error) { return 0, nil },
		UnsubscribeWhenAbsent:   func() error { return nil },
		Upgrade:                 func() (bool, error) { return true, nil },
		ValidateWhileConverging: func() (bool, bool) { return true, false },
		ValidateWhenDead:        func() (bool, bool) { return false, true },
	}
}

// CheckVerbSemantics is a pure function, so this is a real assertion rather than a dance around *testing.T.
func broken(seams kittest.VerbSeams) bool {
	return len(kittest.CheckVerbSemantics(seams)) > 0
}

func TestHarness_PassesAConformingOperator(t *testing.T) {
	if broken(conforming()) {
		t.Error("a conforming operator must pass — otherwise the harness is unusable and will be disabled")
	}
}

func TestHarness_CatchesANonIdempotentSubscribe(t *testing.T) {
	s := conforming()
	calls := 0
	s.Subscribe = func() (int, error) {
		calls++
		return calls * 3, nil // duplicates on every retry
	}
	if !broken(s) {
		t.Error("a subscribe that duplicates on retry must fail the harness (T-501/T-585)")
	}
}

func TestHarness_CatchesATeardownThatLeavesResources(t *testing.T) {
	s := conforming()
	s.Unsubscribe = func() (int, error) { return 2, nil }
	if !broken(s) {
		t.Error("resources surviving unsubscribe must fail the harness — the consumer keeps paying (MIT-1124)")
	}
}

func TestHarness_CatchesAnErrorOnAbsentTeardown(t *testing.T) {
	s := conforming()
	s.UnsubscribeWhenAbsent = func() error { return errors.New("404 not found") }
	if !broken(s) {
		t.Error("erroring on an absent resource must fail — it turns teardown into an endless retry (MIT-1302)")
	}
}

func TestHarness_CatchesADestructiveUpgrade(t *testing.T) {
	s := conforming()
	s.Upgrade = func() (bool, error) { return false, nil }
	if !broken(s) {
		t.Error("an upgrade that clobbers existing state must fail the harness")
	}
}

func TestHarness_CatchesConvergingReportedAsAnError(t *testing.T) {
	s := conforming()
	s.ValidateWhileConverging = func() (bool, bool) { return false, true }
	if !broken(s) {
		t.Error("converging reported as an error must fail the harness (MIT-1293)")
	}
}

func TestHarness_CatchesADeadResourceReportedAsPending(t *testing.T) {
	// The opposite error, and the one that hangs a subscription forever.
	s := conforming()
	s.ValidateWhenDead = func() (bool, bool) { return true, false }
	if !broken(s) {
		t.Error("a dead resource reported pending must fail — it would wait forever (MIT-1291)")
	}
}

func TestHarness_ANilSeamFailsRatherThanSkipping(t *testing.T) {
	// The failure mode this whole package is written against: a check that
	// silently does not run looks exactly like one that passed.
	s := conforming()
	s.Upgrade = nil
	if !broken(s) {
		t.Error("a nil seam must FAIL — skipping reports coverage that does not exist")
	}
}

func TestHarness_NilSeamProblemNamesTheSeam(t *testing.T) {
	s := conforming()
	s.ValidateWhenDead = nil
	problems := kittest.CheckVerbSemantics(s)
	if len(problems) == 0 {
		t.Fatal("expected a problem")
	}
	if !strings.Contains(problems[0], "ValidateWhenDead") {
		t.Errorf("problem = %q, want the missing seam named", problems[0])
	}
}

func TestHarness_ReportsEveryViolationNotJustTheFirst(t *testing.T) {
	s := conforming()
	s.Unsubscribe = func() (int, error) { return 2, nil }
	s.Upgrade = func() (bool, error) { return false, nil }
	s.ValidateWhileConverging = func() (bool, bool) { return false, true }

	if got := len(kittest.CheckVerbSemantics(s)); got < 3 {
		t.Errorf("reported %d problems, want all 3 — a harness that stops at the first costs a CI round trip per defect", got)
	}
}
