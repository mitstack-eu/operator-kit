package kittest_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/kittest"
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
		Redrive:                 func() (kittest.RedriveRun, error) { return completedRedrive, nil },
	}
}

var completedRedrive = kittest.RedriveRun{FirstPending: true, Redrive: true, Settled: true}

func redrive(edit func(*kittest.RedriveRun)) func(s *kittest.VerbSeams) {
	return func(s *kittest.VerbSeams) {
		run := completedRedrive
		edit(&run)
		s.Redrive = func() (kittest.RedriveRun, error) { return run, nil }
	}
}

func TestCheckVerbSemantics(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(s *kittest.VerbSeams)
		// wantAtLeast is how many problems the broken seams must yield; 0 means none at all.
		wantAtLeast int
		wantNamed   string
	}{
		{"a conforming operator passes, or the harness is unusable and will be disabled", func(*kittest.VerbSeams) {}, 0, ""},
		{"a subscribe that duplicates on every retry fails", func(s *kittest.VerbSeams) {
			calls := 0
			s.Subscribe = func() (int, error) { calls++; return calls * 3, nil }
		}, 1, ""},
		{"resources surviving unsubscribe fail, since the consumer keeps paying", func(s *kittest.VerbSeams) {
			s.Unsubscribe = func() (int, error) { return 2, nil }
		}, 1, ""},
		{"erroring on an absent resource fails, since it turns teardown into an endless retry", func(s *kittest.VerbSeams) {
			s.UnsubscribeWhenAbsent = func() error { return errors.New("404 not found") }
		}, 1, ""},
		{"an upgrade that clobbers existing state fails", func(s *kittest.VerbSeams) { s.Upgrade = func() (bool, error) { return false, nil } }, 1, ""},
		{"converging reported as an error fails", func(s *kittest.VerbSeams) { s.ValidateWhileConverging = func() (bool, bool) { return false, true } }, 1, ""},
		{"a dead resource reported pending fails, since it would wait forever", func(s *kittest.VerbSeams) { s.ValidateWhenDead = func() (bool, bool) { return true, false } }, 1, ""},
		{"a block with no long step leaves Redrive nil and passes", func(s *kittest.VerbSeams) { s.Redrive = nil }, 0, ""},
		{"a long step that never outlasts the first subscribe fails", redrive(func(r *kittest.RedriveRun) { r.FirstPending = false }), 1, "never outlasted"},
		{"a finished step the validate does not report fails, since the engine never re-drives", redrive(func(r *kittest.RedriveRun) { r.Redrive = false }), 1, "redrive"},
		{"a re-drive that changes a resource the first subscribe made fails", redrive(func(r *kittest.RedriveRun) { r.Changed = 1 }), 1, "changed 1 resource"},
		{"a re-drive that leaves the step pending fails", redrive(func(r *kittest.RedriveRun) { r.Settled = false }), 1, "left the step pending"},
		{"a nil seam fails rather than skipping, since a skipped check looks like a pass", func(s *kittest.VerbSeams) { s.Upgrade = nil }, 1, ""},
		{"a nil seam's problem names the seam", func(s *kittest.VerbSeams) { s.ValidateWhenDead = nil }, 1, "ValidateWhenDead"},
		{"every violation is reported, not just the first", func(s *kittest.VerbSeams) {
			s.Unsubscribe = func() (int, error) { return 2, nil }
			s.Upgrade = func() (bool, error) { return false, nil }
			s.ValidateWhileConverging = func() (bool, bool) { return false, true }
		}, 3, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := conforming()
			tc.break_(&s)

			problems := kittest.CheckVerbSemantics(s)

			if (tc.wantAtLeast == 0 && len(problems) != 0) || len(problems) < tc.wantAtLeast {
				t.Errorf("CheckVerbSemantics = %q, want at least %d problems (none when 0)", problems, tc.wantAtLeast)
			}
			if tc.wantNamed != "" && (len(problems) == 0 || !strings.Contains(problems[0], tc.wantNamed)) {
				t.Errorf("CheckVerbSemantics = %q, want the first problem to name %s", problems, tc.wantNamed)
			}
		})
	}
}
