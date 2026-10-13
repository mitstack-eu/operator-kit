// verbseams_over_handler_test.go pins kittest.VerbSeamsOverHandler: it
// drives a real handler.Handler over signed webhook requests against a
// kittest.FakeResource and adapts the outcome into kittest.VerbSeams, so
// CheckVerbSemantics can judge a real handler exactly as it judges any
// operator's own hand-written seams.
//
// Only this test file imports handler, because only a test needs a real
// Handler to drive. kittest's own production code imports no handler
// package, which is what keeps the two importable in either direction.
//
// A harness is only worth having if it FAILS on the defects it claims to
// catch (see verbs_test.go's own note) — so this file proves BOTH halves:
// a conforming FakeResource passes cleanly, and each misconfigured one
// drives CheckVerbSemantics to its matching problem.
package kittest_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// buildSeams wires a fresh Signer and a real Handler over f, then returns
// the VerbSeams VerbSeamsOverHandler adapts from it. VerbBudget is kept
// small and Sleep is a no-op so a test that drives the settle loop to
// exhaustion (the dead-resource case) never waits on wall-clock time.
func buildSeams(t *testing.T, f *kittest.FakeResource) kittest.VerbSeams {
	t.Helper()
	s := kittest.NewSigner(t, "kid-1", 7)
	h, err := handler.New(handler.Config{
		Resource:     f,
		JWKSURL:      s.JWKSServer(t),
		VerbBudget:   2,
		PollInterval: time.Millisecond,
		Sleep:        func(context.Context, time.Duration) {},
	})
	if err != nil {
		t.Fatalf("handler.New: %v", err)
	}
	return kittest.VerbSeamsOverHandler(t, h, s, f)
}

func TestVerbSeamsOverHandler_ConformingFakeResourcePassesCleanly(t *testing.T) {
	f := &kittest.FakeResource{}
	problems := kittest.CheckVerbSemantics(buildSeams(t, f))
	if len(problems) != 0 {
		t.Errorf("a conforming FakeResource must drive CheckVerbSemantics to a clean pass through a real handler, got: %v", problems)
	}
}

func TestVerbSeamsOverHandler_DuplicateOnEnsureFailsIdempotentSubscribe(t *testing.T) {
	f := &kittest.FakeResource{DuplicateOnEnsure: true}
	problems := kittest.CheckVerbSemantics(buildSeams(t, f))
	if !anyContains(problems, "must CONVERGE, not duplicate") {
		t.Errorf("expected the idempotent-subscribe violation, got: %v", problems)
	}
}

func TestVerbSeamsOverHandler_LeakOnDeleteFailsTeardown(t *testing.T) {
	f := &kittest.FakeResource{LeakOnDelete: true}
	problems := kittest.CheckVerbSemantics(buildSeams(t, f))
	if !anyContains(problems, "survived unsubscribe") {
		t.Errorf("expected the teardown-survives violation, got: %v", problems)
	}
}

func TestVerbSeamsOverHandler_ErrorWhenAlreadyAbsentFailsAbsenceIsSuccess(t *testing.T) {
	f := &kittest.FakeResource{ErrorWhenAlreadyAbsent: true}
	problems := kittest.CheckVerbSemantics(buildSeams(t, f))
	if !anyContains(problems, "endless retry") {
		t.Errorf("expected the absence-is-not-success violation, got: %v", problems)
	}
}

// TestVerbSeamsOverHandler_ClobberOnUpgradeFailsConvergentUpgrade seeds an
// initial output so the adapter's upgrade check has something to observe
// surviving (or not) — the CONTRACT's Upgrade seam also seeds one when the
// fake carries none, so seeding here is not load-bearing, only explicit.
func TestVerbSeamsOverHandler_ClobberOnUpgradeFailsConvergentUpgrade(t *testing.T) {
	f := &kittest.FakeResource{
		Outputs:          map[string]string{"url": "https://example.test"},
		ClobberOnUpgrade: true,
	}
	problems := kittest.CheckVerbSemantics(buildSeams(t, f))
	if !anyContains(problems, "destructive upgrade") {
		t.Errorf("expected the destructive-upgrade violation, got: %v", problems)
	}
}

// DeadReportedPending answers Unknown with no error even though ObserveErr is set, so ValidateWhenDead reads pending and the harness must flag it.
func TestVerbSeamsOverHandler_DeadReportedPendingFailsDeadDetection(t *testing.T) {
	f := &kittest.FakeResource{
		DeadReportedPending: true,
		ObserveErr:          errors.New("this would normally be an unrecoverable failure"),
	}
	problems := kittest.CheckVerbSemantics(buildSeams(t, f))
	if !anyContains(problems, "would wait forever") {
		t.Errorf("expected the dead-resource-reported-pending violation (MIT-1291), got: %v", problems)
	}
}

func anyContains(problems []string, substr string) bool {
	for _, p := range problems {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}
