package teardown

import (
	"context"
	"errors"
	"fmt"

	"github.com/mitstack-eu/operator-kit/v3/adopt"
)

type Presence struct {
	Absent    bool
	Remaining []Resource
	// Unsearched lists classes not provably clear, which does not imply a discovery error; check the returned error for that.
	Unsearched []string
}

// Probe must stay read-only because every validate carries the marker, and an unattributable resource goes to Unsearched without an error because verifyTeardownAndSettle fails open on an unrecognised probe error.
func Probe(ctx context.Context, id adopt.Identity, classes []Class) (Presence, error) {
	var p Presence

	if err := id.Validate(); err != nil {
		return p, fmt.Errorf("teardown: refusing to probe: %w", err)
	}

	var errs []error

	for _, c := range classes {
		found, err := c.Find(ctx, id)
		if err != nil {
			p.Unsearched = append(p.Unsearched, c.Kind())
			errs = append(errs, fmt.Errorf("teardown: %s: discovery failed (this class is UNSEARCHED, not absent): %w", c.Kind(), err))
			continue
		}

		classUnprovable := false

		for _, r := range found {
			if r.Kind == "" {
				r.Kind = c.Kind()
			}
			switch _, kind := skipReason(id, r); kind {
			case skipForeign:
				// Says nothing about our app; counting it would keep a finished teardown looking unfinished.
			case skipCannotTell:
				// No owner tag: unprovable, and deliberately not appended to errs (see Probe).
				classUnprovable = true
			default:
				p.Remaining = append(p.Remaining, r)
			}
		}

		if classUnprovable {
			p.Unsearched = append(p.Unsearched, c.Kind())
		}
	}

	p.Absent = len(p.Remaining) == 0 && len(p.Unsearched) == 0

	if len(errs) > 0 {
		return p, errors.Join(errs...)
	}
	return p, nil
}
