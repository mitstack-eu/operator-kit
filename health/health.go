// Package health, an optional helper, makes `validate` a verb that acts on an unhealthy resource, with bounded remediation and no replacement of serving content.
package health

import (
	"context"
	"errors"
	"fmt"
)

// Status is what a probe concluded about a resource.
type Status int

const (
	// Converging is the zero value so an unset status neither claims health nor triggers a destructive remediation.
	Converging Status = iota
	Healthy
	// Dead means it will not recover by waiting, so remediation is warranted.
	Dead
)

func (s Status) String() string {
	switch s {
	case Healthy:
		return "healthy"
	case Dead:
		return "dead"
	default:
		return "converging"
	}
}

// Check reports a resource's status, and must not call a converging resource Dead.
type Check func(ctx context.Context) (Status, error)

type Remediate func(ctx context.Context) error

// Content describes what a resource is currently serving, for the placeholder and last-good rules.
type Content struct {
	Ref           string
	IsPlaceholder bool
	Serving       bool
}

var ErrRemediationExhausted = errors.New("health: remediation attempts exhausted")

// ErrWouldReplaceServingContent is returned when a promotion would put a placeholder over serving content.
var ErrWouldReplaceServingContent = errors.New("health: refusing to replace serving content with a placeholder")

// Healer runs check, and remediates when the resource is Dead, at most MaxAttempts times, where zero means one rather than unlimited.
type Healer struct {
	Check       Check
	Remediate   Remediate
	MaxAttempts int

	attempts int
}

// Run probes once and remediates if warranted, returning the status observed before any remediation and whether it ran.
func (h *Healer) Run(ctx context.Context) (Status, bool, error) {
	st, err := h.Check(ctx)
	if err != nil {
		return Converging, false, fmt.Errorf("health: probe failed: %w", err)
	}
	if st != Dead {
		// Converging is never remediated: replacing a resource about to come up turns a slow boot into a loop.
		return st, false, nil
	}
	if h.Remediate == nil {
		return st, false, nil
	}

	cap := h.MaxAttempts
	if cap <= 0 {
		cap = 1
	}
	if h.attempts >= cap {
		return st, false, fmt.Errorf("%w after %d attempt(s)", ErrRemediationExhausted, h.attempts)
	}
	h.attempts++

	if err := h.Remediate(ctx); err != nil {
		return st, true, fmt.Errorf("health: remediation failed: %w", err)
	}
	return st, true, nil
}

func (h *Healer) Attempts() int { return h.attempts }

// Promote decides what a recreated resource comes back with: never a placeholder over serving content, and last-good in preference to a placeholder.
func Promote(current, lastGood, candidate Content) (Content, error) {
	if candidate.IsPlaceholder && current.Serving && !current.IsPlaceholder {
		return Content{}, fmt.Errorf("%w: %q is serving", ErrWouldReplaceServingContent, current.Ref)
	}
	if candidate.IsPlaceholder && lastGood.Ref != "" && !lastGood.IsPlaceholder {
		return lastGood, nil
	}
	return candidate, nil
}
