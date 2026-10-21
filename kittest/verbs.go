package kittest

import (
	"fmt"
	"sort"
	"testing"
)

// VerbSeams is what an operator supplies to have its verb semantics checked, and a nil seam fails rather than skipping, Redrive alone excepted.
type VerbSeams struct {
	// Subscribe is called twice by the idempotency check.
	Subscribe func() (resourceCount int, err error)

	Unsubscribe func() (remaining int, err error)

	// UnsubscribeWhenAbsent must not error.
	UnsubscribeWhenAbsent func() error

	Upgrade func() (preservedExistingState bool, err error)

	// ValidateWhileConverging must be pending, not failed.
	ValidateWhileConverging func() (pending bool, isError bool)

	// ValidateWhenDead must not be pending.
	ValidateWhenDead func() (pending bool, isError bool)

	// Redrive is left nil by a block with no step that outlasts one subscribe.
	Redrive func() (RedriveRun, error)
}

// RedriveRun is what a block answered across a long step, its first subscribe, the validate once the step finished and the re-driven subscribe, with Changed counting the resources the first subscribe made that the re-drive changed.
type RedriveRun struct {
	FirstPending, Redrive, Settled bool
	Changed                        int
}

// CheckVerbSemantics returns one problem per violation and takes no *testing.T, because t.Run on a bare testing.T never executes and would make its self-tests vacuous.
func CheckVerbSemantics(s VerbSeams) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, sprintf(format, args...)) }

	for name, isNil := range map[string]bool{
		"Subscribe":               s.Subscribe == nil,
		"Unsubscribe":             s.Unsubscribe == nil,
		"UnsubscribeWhenAbsent":   s.UnsubscribeWhenAbsent == nil,
		"Upgrade":                 s.Upgrade == nil,
		"ValidateWhileConverging": s.ValidateWhileConverging == nil,
		"ValidateWhenDead":        s.ValidateWhenDead == nil,
	} {
		if isNil {
			add("VerbSeams.%s is nil — every seam is required. A harness that skips the property it was asked to check reports coverage that does not exist.", name)
		}
	}
	if len(problems) > 0 {
		sortStrings(problems)
		return problems
	}

	first, err := s.Subscribe()
	switch {
	case err != nil:
		add("first subscribe failed: %v", err)
	default:
		second, err := s.Subscribe()
		if err != nil {
			add("RETRIED subscribe failed: %v — subscribe is retried by the engine, so this is a normal condition, not an edge case", err)
		} else if second != first {
			add("a retried subscribe changed the resource count %d -> %d — it must CONVERGE, not duplicate (T-501/T-585)", first, second)
		}
	}

	if _, err := s.Subscribe(); err != nil {
		add("subscribe before teardown failed: %v", err)
	} else if remaining, err := s.Unsubscribe(); err != nil {
		add("unsubscribe failed: %v", err)
	} else if remaining != 0 {
		add("%d resource(s) survived unsubscribe — the consumer is still paying for them (MIT-1124)", remaining)
	}

	if err := s.UnsubscribeWhenAbsent(); err != nil {
		add("unsubscribe errored on a resource that was never there: %v — absence is SUCCESS; erroring turns teardown into an endless retry instead of a finish (MIT-1302/MIT-1278)", err)
	}

	if _, err := s.Subscribe(); err != nil {
		add("subscribe before upgrade failed: %v", err)
	} else if preserved, err := s.Upgrade(); err != nil {
		add("upgrade failed: %v", err)
	} else if !preserved {
		add("upgrade did not preserve pre-existing state — a destructive upgrade is worse than a failed one")
	}

	if pending, isErr := s.ValidateWhileConverging(); !pending {
		add("a resource still coming up was not reported PENDING")
	} else if isErr {
		add("a resource still coming up was reported as an ERROR — a block merely waiting is never marked failed and never consumes a retry (MIT-1293)")
	}
	if deadPending, _ := s.ValidateWhenDead(); deadPending {
		add("a resource that will not recover was reported PENDING — it would wait forever instead of failing (MIT-1291)")
	}
	if s.Redrive != nil {
		problems = append(problems, checkRedrive(s.Redrive)...)
	}

	return problems
}

func checkRedrive(redrive func() (RedriveRun, error)) []string {
	run, err := redrive()
	switch {
	case err != nil:
		return []string{sprintf("the long step failed: %v", err)}
	case !run.FirstPending:
		return []string{"the first subscribe settled, so the long step it was given never outlasted one call"}
	}
	var problems []string
	if !run.Redrive {
		problems = append(problems, "the validate after the long step finished did not answer redrive beside pending — the engine never re-drives subscribe, and the subscription waits out its settle budget")
	}
	if run.Changed > 0 {
		problems = append(problems, sprintf("the re-driven subscribe changed %d resource(s) the first subscribe made — it must resume from what it observes, not redo a step", run.Changed))
	}
	if !run.Settled {
		problems = append(problems, "the re-driven subscribe left the step pending — it did not take the step the finished one made possible")
	}
	return problems
}

// AssertVerbSemantics runs every property and fails t with one error per violation.
func AssertVerbSemantics(t *testing.T, s VerbSeams) {
	t.Helper()
	for _, p := range CheckVerbSemantics(s) {
		t.Error(p)
	}
}

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
func sortStrings(s []string)                    { sort.Strings(s) }
