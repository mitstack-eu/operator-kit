package teardown_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/adopt"
	"github.com/mitstack-eu/operator-kit/v2/teardown"
)

func id() adopt.Identity { return adopt.Identity{AppID: "app-42"} }

func owned(rid string) teardown.Resource {
	return teardown.Resource{Kind: "instance", ID: rid, Tags: map[string]string{adopt.TagAppID: "app-42"}}
}

type stub struct {
	kind    string
	found   []teardown.Resource
	findErr error
	delErr  error
	deleted []string
}

func (s *stub) Kind() string { return s.kind }
func (s *stub) Find(_ context.Context, _ adopt.Identity) ([]teardown.Resource, error) {
	return s.found, s.findErr
}
func (s *stub) Delete(_ context.Context, r teardown.Resource) error {
	s.deleted = append(s.deleted, r.ID)
	return s.delErr
}

func TestSweep_ReclaimsExactlyWhatTheDetectorSelected(t *testing.T) {
	s := &stub{kind: "instance", found: []teardown.Resource{owned("i-1"), owned("i-2")}}

	rep, err := teardown.Sweep(context.Background(), id(), []teardown.Class{s})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(s.deleted, ",") != "i-1,i-2" {
		t.Errorf("deleted %v, want exactly what Find selected", s.deleted)
	}
	if len(rep.Reclaimed) != 2 {
		t.Errorf("report lists %d reclaimed, want 2 — the report IS the evidence a teardown test asserts on", len(rep.Reclaimed))
	}
}

func TestSweep_NeverTouchesAnotherAppsResource(t *testing.T) {
	// A class that returns a foreign resource must be filtered, not trusted.
	foreign := teardown.Resource{Kind: "instance", ID: "i-foreign",
		Tags: map[string]string{adopt.TagAppID: "app-99"}}
	s := &stub{kind: "instance", found: []teardown.Resource{owned("i-1"), foreign}}

	if _, err := teardown.Sweep(context.Background(), id(), []teardown.Class{s}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range s.deleted {
		if d == "i-foreign" {
			t.Fatal("swept a resource carrying another app's AppId")
		}
	}
}

func TestSweep_NeverTouchesAnUntaggedResource(t *testing.T) {
	untagged := teardown.Resource{Kind: "instance", ID: "i-untagged", Tags: map[string]string{}}
	s := &stub{kind: "instance", found: []teardown.Resource{untagged}}

	rep, err := teardown.Sweep(context.Background(), id(), []teardown.Class{s})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.deleted) != 0 {
		t.Errorf("deleted %v — an untagged resource has no identity and must never be reclaimed", s.deleted)
	}
	if len(rep.Skipped) != 1 {
		t.Errorf("a skipped resource must be REPORTED, not silently dropped: %+v", rep)
	}
}

func TestSweep_SkipsAResourceStillInUse(t *testing.T) {
	inUse := owned("i-1")
	inUse.InUse = true
	s := &stub{kind: "instance", found: []teardown.Resource{inUse}}

	rep, err := teardown.Sweep(context.Background(), id(), []teardown.Class{s})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.deleted) != 0 {
		t.Errorf("deleted %v — an in-use resource must be skipped", s.deleted)
	}
	if len(rep.Skipped) != 1 || !strings.Contains(rep.Skipped[0].Reason, "in use") {
		t.Errorf("skip must say why: %+v", rep.Skipped)
	}
}

func TestSweep_AnAbsentResourceIsSuccess(t *testing.T) {
	s := &stub{kind: "instance", found: nil}

	rep, err := teardown.Sweep(context.Background(), id(), []teardown.Class{s})
	if err != nil {
		t.Fatalf("an absent resource must be success, got %v", err)
	}
	if len(rep.Reclaimed) != 0 {
		t.Errorf("nothing to reclaim, got %+v", rep.Reclaimed)
	}
}

func TestSweep_DeleteOfAVanishedResourceIsSuccess(t *testing.T) {
	s := &stub{kind: "instance", found: []teardown.Resource{owned("i-1")},
		delErr: errors.New("InvalidInstanceID.NotFound: The instance ID 'i-1' does not exist")}

	if _, err := teardown.Sweep(context.Background(), id(), []teardown.Class{s}); err != nil {
		t.Fatalf("a resource that vanished before deletion must be success, got %v", err)
	}
}

func TestSweep_AGenuineDeleteFailureIsReturned(t *testing.T) {
	s := &stub{kind: "instance", found: []teardown.Resource{owned("i-1")},
		delErr: errors.New("DependencyViolation: resource is attached")}

	rep, err := teardown.Sweep(context.Background(), id(), []teardown.Class{s})
	if err == nil {
		t.Fatal("a genuine reclaim failure must surface, never be swallowed")
	}
	if !strings.Contains(err.Error(), "i-1") {
		t.Errorf("the error must name the resource still costing money: %v", err)
	}
	if len(rep.Failed) != 1 {
		t.Errorf("the report must list the failure: %+v", rep)
	}
}

func TestSweep_ContinuesPastOneFailureAndReportsThemAll(t *testing.T) {
	bad := &stub{kind: "volume", found: []teardown.Resource{owned("vol-1")},
		delErr: errors.New("DependencyViolation")}
	good := &stub{kind: "eip", found: []teardown.Resource{owned("eip-1")}}

	rep, err := teardown.Sweep(context.Background(), id(), []teardown.Class{bad, good})
	if err == nil {
		t.Fatal("expected the failure to surface")
	}
	if len(good.deleted) != 1 {
		t.Error("a failure in one class must not stop the others being swept")
	}
	if len(rep.Reclaimed) != 1 || len(rep.Failed) != 1 {
		t.Errorf("report = %+v, want both the success and the failure", rep)
	}
}

func TestSweep_ADiscoveryFailureIsNotAnAllClear(t *testing.T) {
	s := &stub{kind: "instance", findErr: errors.New("AccessDenied")}

	rep, err := teardown.Sweep(context.Background(), id(), []teardown.Class{s})
	if err == nil {
		t.Fatal("a discovery failure must surface — an unsearched class is not a clean one")
	}
	if rep.Clean() {
		t.Error("Clean() must be false when a class could not be searched")
	}
}

func TestReport_CleanOnlyWhenEverythingWasSearchedAndNothingFailed(t *testing.T) {
	rep, err := teardown.Sweep(context.Background(), id(), []teardown.Class{&stub{kind: "instance"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.Clean() {
		t.Error("a fully-searched sweep with no leftovers and no failures is clean")
	}
}

func TestReport_IsNonTrivialEvidence(t *testing.T) {
	s := &stub{kind: "instance", found: []teardown.Resource{owned("i-1")}}
	rep, _ := teardown.Sweep(context.Background(), id(), []teardown.Class{s})

	if rep.Reclaimed[0].Kind != "instance" || rep.Reclaimed[0].ID != "i-1" {
		t.Errorf("reclaimed entry = %+v, want kind and id", rep.Reclaimed[0])
	}
	if !strings.Contains(rep.String(), "i-1") {
		t.Errorf("String() = %q, want the reclaimed resource named", rep.String())
	}
}

func TestSweep_RejectsAnIdentityThatWouldMatchEverything(t *testing.T) {
	s := &stub{kind: "instance", found: []teardown.Resource{owned("i-1")}}

	if _, err := teardown.Sweep(context.Background(), adopt.Identity{}, []teardown.Class{s}); err == nil {
		t.Fatal("an empty identity must refuse — a sweep scoped to nothing is scoped to everything")
	}
	if len(s.deleted) != 0 {
		t.Errorf("deleted %v before validating the identity", s.deleted)
	}
}

func TestIsNotFound(t *testing.T) {
	for _, msg := range []string{
		"InvalidInstanceID.NotFound: does not exist",
		"NoSuchBucket: The specified bucket does not exist",
		"ResourceNotFoundException",
		"NoSuchEntity: The role with name x cannot be found",
		"InvalidAllocationID.NotFound",
		"404 Not Found",
	} {
		if !teardown.IsNotFound(errors.New(msg)) {
			t.Errorf("IsNotFound(%q) = false, want true", msg)
		}
	}
	for _, msg := range []string{
		"DependencyViolation: resource is attached",
		"AccessDenied",
	} {
		if teardown.IsNotFound(errors.New(msg)) {
			t.Errorf("IsNotFound(%q) = true, want false — this resource is still there", msg)
		}
	}
	if teardown.IsNotFound(nil) {
		t.Error("IsNotFound(nil) must be false")
	}
}

func TestIsNotFound_MatchesKubernetesSpelling(t *testing.T) {
	// Kubernetes says `... "app" not found`, with a space.
	for _, msg := range []string{
		`claims.aws.mitstack.dev "app" not found`,
		"Error from server (NotFound): pods not found",
		"secret not-found",
	} {
		if !teardown.IsNotFound(errors.New(msg)) {
			t.Errorf("IsNotFound(%q) = false, want true", msg)
		}
	}
	if teardown.IsNotFound(errors.New("DependencyViolation: resource is attached")) {
		t.Error("a dependency violation is not absence")
	}
}
