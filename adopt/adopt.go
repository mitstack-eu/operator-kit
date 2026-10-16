// Package adopt, an optional helper, is the idempotency contract for a provisioning operator whose retried subscribe must converge without adopting another app's resources.
package adopt

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	// TagAppID is the immutable app id, the only tag adoption matches identity on.
	TagAppID = "AppId"
	// TagSubscriptionID records which subscription provisioned the resource.
	TagSubscriptionID = "SubscriptionId"
	// TagManagedBy marks a resource as mitstack-managed, so one another system owns is never adopted.
	TagManagedBy = "ManagedBy"
)

// ManagedByValue is the TagManagedBy value mitstack writes.
const ManagedByValue = "mitstack"

// Identity is what a resource must match to be adopted.
type Identity struct {
	AppID string
	// SubscriptionID is written as a tag but not matched on, since a resubscribe yields a new one for the same app.
	SubscriptionID string
	// Name is carried for tagging and diagnostics and never used to match.
	Name string
}

// Validate rejects an identity that could not distinguish one app from another.
func (id Identity) Validate() error {
	if strings.TrimSpace(id.AppID) == "" {
		return errors.New("adopt: AppID is required — an identity without it matches every app's resources")
	}
	return nil
}

// Tags returns the tag set to write onto a provisioned resource, omitting an unknown subscription id rather than writing it empty.
func (id Identity) Tags() map[string]string {
	t := map[string]string{
		TagAppID:     id.AppID,
		TagManagedBy: ManagedByValue,
	}
	if id.SubscriptionID != "" {
		t[TagSubscriptionID] = id.SubscriptionID
	}
	if id.Name != "" {
		t["Name"] = id.Name
	}
	return t
}

// Filter returns an AWS describe filter on the AppId tag value, never the tag key, which would match every app's resources.
func (id Identity) Filter() string {
	return "Name=tag:" + TagAppID + ",Values=" + id.AppID
}

// Action is what to do about a candidate resource.
type Action int

const (
	// Refuse is the zero value so a decision never made fails safe rather than adopting.
	Refuse Action = iota
	// Create means nothing exists, so provision it.
	Create
	// Adopt means an exact-identity match exists, so import it.
	Adopt
)

func (a Action) String() string {
	switch a {
	case Create:
		return "Create"
	case Adopt:
		return "Adopt"
	default:
		return "Refuse"
	}
}

// Candidate is a resource discovered in the account that might be ours.
type Candidate struct {
	// ResourceID is the provider's own id, used for reporting and import and never for matching.
	ResourceID string
	Tags       map[string]string
}

// Decision is the outcome for one resource.
type Decision struct {
	Action    Action
	Candidate Candidate
}

// Decide chooses between creating, adopting and refusing, and refuses whenever identity is not exact.
func Decide(id Identity, candidates []Candidate) (Decision, error) {
	if err := id.Validate(); err != nil {
		return Decision{Action: Refuse}, err
	}

	var matches []Candidate
	var conflicts []Candidate
	for _, c := range candidates {
		owner := c.Tags[TagAppID]
		switch {
		case owner == id.AppID:
			if mb, ok := c.Tags[TagManagedBy]; ok && mb != ManagedByValue {
				conflicts = append(conflicts, c)
				continue
			}
			matches = append(matches, c)
		default:
			conflicts = append(conflicts, c)
		}
	}

	if len(conflicts) > 0 {
		return Decision{Action: Refuse}, conflictError(id, conflicts)
	}

	switch len(matches) {
	case 0:
		return Decision{Action: Create}, nil
	case 1:
		return Decision{Action: Adopt, Candidate: matches[0]}, nil
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, m.ResourceID)
		}
		sort.Strings(ids)
		return Decision{Action: Refuse}, fmt.Errorf(
			"adopt: %d resources claim %s=%s (%s) — the invariant is already broken; adopting one would orphan the rest",
			len(matches), TagAppID, id.AppID, strings.Join(ids, ", "))
	}
}

func conflictError(id Identity, conflicts []Candidate) error {
	parts := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		owner := c.Tags[TagAppID]
		if owner == "" {
			owner = "<untagged>"
		}
		if mb, ok := c.Tags[TagManagedBy]; ok && mb != ManagedByValue {
			owner += " managed by " + mb
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", c.ResourceID, owner))
	}
	sort.Strings(parts)
	return fmt.Errorf(
		"adopt: refusing — %s does not exactly match %s=%s. A name or shape that merely looks right is not identity (MIT-1229); adopting here would take over a resource that is not this app's",
		strings.Join(parts, ", "), TagAppID, id.AppID)
}

// Plan decides for a whole resource graph at once, and refuses entirely if any member refuses, since a partial adoption half-rewires an app across two owners.
func Plan(id Identity, found map[string][]Candidate) (map[string]Decision, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}

	out := make(map[string]Decision, len(found))
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		d, err := Decide(id, found[name])
		if err != nil {
			return nil, fmt.Errorf("adopt: %s: %w", name, err)
		}
		out[name] = d
	}
	return out, nil
}

// Matches the AlreadyExists family across AWS services and the plain `-no-color` text an IaC engine prints, which is the form it actually emits.
var alreadyExistsPattern = regexp.MustCompile(
	`(?i)(EntityAlreadyExists|BucketAlreadyOwnedByYou|BucketAlreadyExists|` +
		`RepositoryAlreadyExistsException|AlreadyExistsException|ResourceAlreadyExistsException|` +
		`InvalidGroup\.Duplicate|InvalidKeyPair\.Duplicate|InvalidPermission\.Duplicate|` +
		`already exists)`)

// IsAlreadyExists reports whether output describes a collision with an existing resource, the signal to import rather than fail.
func IsAlreadyExists(output string) bool {
	if strings.TrimSpace(output) == "" {
		return false
	}
	return alreadyExistsPattern.MatchString(output)
}
