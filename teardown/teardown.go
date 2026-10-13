// Package teardown reclaims what the IaC engine never tracked, scoped to the app's exact identity.
package teardown

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mitstack-eu/operator-kit/v2/adopt"
)

type Resource struct {
	Kind string
	ID   string
	Tags map[string]string
	// InUse marks a resource that is live; it is skipped and reported, never deleted.
	InUse bool
	Note  string
}

// Find queries the provider, not engine state, and must scope its query to the identity it is given.
type Class interface {
	Kind() string
	Find(ctx context.Context, id adopt.Identity) ([]Resource, error)
	Delete(ctx context.Context, r Resource) error
}

// Skip records a resource deliberately left alone, reported because "chose not to touch" is not "nothing there".
type Skip struct {
	Resource Resource
	Reason   string
}

type Failure struct {
	Resource Resource
	Err      error
}

type Report struct {
	Reclaimed []Resource
	Skipped   []Skip
	Failed    []Failure
	// Unsearched lists classes whose discovery failed, which are not clean.
	Unsearched []string
}

func (r Report) Clean() bool {
	return len(r.Failed) == 0 && len(r.Unsearched) == 0 && len(r.Skipped) == 0
}

func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "reclaimed %d", len(r.Reclaimed))
	for _, x := range r.Reclaimed {
		fmt.Fprintf(&b, " %s/%s", x.Kind, x.ID)
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(&b, "; skipped %d", len(r.Skipped))
		for _, s := range r.Skipped {
			fmt.Fprintf(&b, " %s/%s(%s)", s.Resource.Kind, s.Resource.ID, s.Reason)
		}
	}
	if len(r.Failed) > 0 {
		fmt.Fprintf(&b, "; FAILED %d", len(r.Failed))
		for _, f := range r.Failed {
			fmt.Fprintf(&b, " %s/%s", f.Resource.Kind, f.Resource.ID)
		}
	}
	if len(r.Unsearched) > 0 {
		fmt.Fprintf(&b, "; UNSEARCHED %s", strings.Join(r.Unsearched, ","))
	}
	return b.String()
}

// Sweep finds and reclaims every leftover across the given classes, returning the Report even alongside an error.
func Sweep(ctx context.Context, id adopt.Identity, classes []Class) (Report, error) {
	var rep Report

	if err := id.Validate(); err != nil {
		return rep, fmt.Errorf("teardown: refusing to sweep: %w", err)
	}

	var errs []error

	for _, c := range classes {
		found, err := c.Find(ctx, id)
		if err != nil {
			rep.Unsearched = append(rep.Unsearched, c.Kind())
			errs = append(errs, fmt.Errorf("teardown: %s: discovery failed (this class is UNSEARCHED, not clean): %w", c.Kind(), err))
			continue
		}

		for _, r := range found {
			if r.Kind == "" {
				r.Kind = c.Kind()
			}

			if reason, kind := skipReason(id, r); kind != notSkipped {
				rep.Skipped = append(rep.Skipped, Skip{Resource: r, Reason: reason})
				continue
			}

			if err := c.Delete(ctx, r); err != nil {
				if IsNotFound(err) {
					rep.Reclaimed = append(rep.Reclaimed, r)
					continue
				}
				rep.Failed = append(rep.Failed, Failure{Resource: r, Err: err})
				errs = append(errs, fmt.Errorf("teardown: %s/%s still present after reclaim: %w", r.Kind, r.ID, err))
				continue
			}
			rep.Reclaimed = append(rep.Reclaimed, r)
		}
	}

	if len(errs) > 0 {
		return rep, errors.Join(errs...)
	}
	return rep, nil
}

type skipKind int

const (
	notSkipped skipKind = iota
	// skipCannotTell means no owner tag, which proves nothing about identity either way.
	skipCannotTell
	// skipOwnStillStanding means ours and in use, which is evidence our app has not finished tearing down.
	skipOwnStillStanding
	skipForeign
)

// skipReason decides whether a resource must be left alone, in every case where deleting would be worse than leaking.
func skipReason(id adopt.Identity, r Resource) (string, skipKind) {
	owner := r.Tags[adopt.TagAppID]
	switch {
	case owner == "":
		return "untagged — no identity, so it cannot be proven ours", skipCannotTell
	case owner != id.AppID:
		return "belongs to " + owner, skipForeign
	}
	if mb, ok := r.Tags[adopt.TagManagedBy]; ok && mb != adopt.ManagedByValue {
		return "managed by " + mb, skipForeign
	}
	if r.InUse {
		return "in use", skipOwnStillStanding
	}
	return "", notSkipped
}

// notFoundPattern is generous because a false negative turns a benign race into a failed teardown, and Kubernetes says `"app" not found` with a space.
var notFoundPattern = regexp.MustCompile(
	`(?i)(not[ -]?found|NoSuchBucket|NoSuchEntity|NoSuchKey|ResourceNotFoundException|` +
		`does not exist|cannot be found|404)`)

// IsNotFound reports whether err means the resource is already gone, which is success for a teardown.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return notFoundPattern.MatchString(err.Error())
}
