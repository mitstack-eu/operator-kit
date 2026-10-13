package teardown

import (
	"context"
	"fmt"
	"log"

	"github.com/mitstack-eu/operator-kit/v2/events"
)

type Check struct {
	Kind string
	// Present answers the question; any non-nil error means it could not be answered, whatever the bool says.
	Present func(ctx context.Context) (bool, error)
}

// Options carries the side effects Survivor, Verify and Absent perform, defaulted when nil.
type Options struct {
	Logf  func(format string, args ...any)
	Scrub func(string) string
	// Emit reports a confirmed survivor; defaults to events.EmitFailed.
	Emit   func(ctx context.Context, detail string) error
	Detail func(kind string) string
}

func resolved(opts Options) Options {
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	if opts.Scrub == nil {
		opts.Scrub = func(s string) string { return s }
	}
	if opts.Emit == nil {
		opts.Emit = events.EmitFailed
	}
	if opts.Detail == nil {
		opts.Detail = func(kind string) string {
			return fmt.Sprintf("teardown reported success but %s is still present", kind)
		}
	}
	return opts
}

// Only a confident positive, Present returning (true, nil), may turn a reported success into a failure, since an error is evidence of neither survival nor absence.
func Survivor(ctx context.Context, opts Options, checks ...Check) string {
	opts = resolved(opts)

	if len(checks) == 0 {
		opts.Logf("teardown: Survivor called with zero checks — nothing was asked, this reports nothing")
		return ""
	}

	for _, c := range checks {
		if c.Present == nil {
			opts.Logf("teardown: %s: no Present func — could not ask, not treated as gone or as a survivor", opts.Scrub(c.Kind))
			continue
		}

		present, err := c.Present(ctx)
		if err != nil {
			opts.Logf("teardown: %s: could not confirm (error is not evidence): %s", opts.Scrub(c.Kind), opts.Scrub(err.Error()))
			continue
		}
		if present {
			return c.Kind
		}
	}

	return ""
}

// Verify returns whether a survivor was found even when Emit failed, since an unreported survivor is still a survivor.
func Verify(ctx context.Context, opts Options, checks ...Check) bool {
	opts = resolved(opts)

	kind := Survivor(ctx, opts, checks...)
	if kind == "" {
		return false
	}

	detail := opts.Scrub(opts.Detail(kind))
	opts.Logf("teardown: survivor confirmed: %s", detail)

	if err := opts.Emit(ctx, detail); err != nil {
		opts.Logf("teardown: failed to report survivor %s: %s", opts.Scrub(kind), opts.Scrub(err.Error()))
	}

	return true
}

// Absent is not the negation of Survivor: any error, nil Present or zero checks defeats absence.
func Absent(ctx context.Context, opts Options, checks ...Check) bool {
	opts = resolved(opts)

	if len(checks) == 0 {
		opts.Logf("teardown: Absent called with zero checks — no evidence to assert absence from")
		return false
	}

	allGone := true

	for _, c := range checks {
		if c.Present == nil {
			opts.Logf("teardown: %s: no Present func — could not ask, not treated as absent", opts.Scrub(c.Kind))
			allGone = false
			continue
		}

		present, err := c.Present(ctx)
		if err != nil {
			opts.Logf("teardown: %s: could not confirm (error is not evidence): %s", opts.Scrub(c.Kind), opts.Scrub(err.Error()))
			allGone = false
			continue
		}
		if present {
			allGone = false
		}
	}

	return allGone
}
