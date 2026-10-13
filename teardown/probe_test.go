package teardown_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/adopt"
	"github.com/mitstack-eu/operator-kit/v2/teardown"
)

func probeIdentity() adopt.Identity {
	return adopt.Identity{AppID: "app_1", Name: "checkout"}
}

func TestProbe_NothingFoundIsAbsent(t *testing.T) {
	got, err := teardown.Probe(context.Background(), probeIdentity(),
		[]teardown.Class{&stub{kind: "ebs-volume"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Absent {
		t.Fatalf("an empty discovery across every class is absent, got %+v", got)
	}
}

func TestProbe_AResourceStillThereIsNotAbsent(t *testing.T) {
	c := &stub{kind: "ebs-volume", found: []teardown.Resource{
		{ID: "vol-1", Tags: map[string]string{adopt.TagAppID: "app_1"}},
	}}
	got, err := teardown.Probe(context.Background(), probeIdentity(), []teardown.Class{c})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Absent {
		t.Fatal("a resource that is still there means not absent")
	}
	if len(got.Remaining) != 1 || got.Remaining[0].ID != "vol-1" {
		t.Fatalf("the probe must name what is left, got %+v", got.Remaining)
	}
}

// "We could not look" must never be reported as "there is nothing there".
func TestProbe_ADiscoveryFailureIsNeverAbsent(t *testing.T) {
	c := &stub{kind: "ebs-volume", findErr: errors.New("throttled")}
	got, err := teardown.Probe(context.Background(), probeIdentity(), []teardown.Class{c})
	if err == nil {
		t.Fatal("a discovery failure must surface as an error, not be swallowed")
	}
	if got.Absent {
		t.Fatal("a class that could not be searched is not a class that is clean")
	}
	if len(got.Unsearched) != 1 || got.Unsearched[0] != "ebs-volume" {
		t.Fatalf("the probe must name the class it could not search, got %+v", got.Unsearched)
	}
}

func TestProbe_OneUnsearchableClassDefeatsTheCleanOnes(t *testing.T) {
	got, _ := teardown.Probe(context.Background(), probeIdentity(), []teardown.Class{
		&stub{kind: "ebs-volume"},
		&stub{kind: "ecr-repository", findErr: errors.New("access denied")},
	})
	if got.Absent {
		t.Fatal("absent must require EVERY class to have been searched and found empty")
	}
}

func TestProbe_AnotherAppsResourceDoesNotHoldUsBack(t *testing.T) {
	c := &stub{kind: "ebs-volume", found: []teardown.Resource{
		{ID: "vol-other", Tags: map[string]string{adopt.TagAppID: "app_2"}},
	}}
	got, err := teardown.Probe(context.Background(), probeIdentity(), []teardown.Class{c})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Absent {
		t.Fatalf("a foreign app's resource must not make our app look present: %+v", got)
	}
}

// Probe runs on live subscriptions too, so it must never delete.
func TestProbe_NeverDeletesAnything(t *testing.T) {
	c := &stub{kind: "ebs-volume", found: []teardown.Resource{
		{ID: "vol-1", Tags: map[string]string{adopt.TagAppID: "app_1"}},
	}}
	if _, err := teardown.Probe(context.Background(), probeIdentity(), []teardown.Class{c}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.deleted) != 0 {
		t.Fatalf("Probe deleted %v — it is a read, and validate calls it on live apps", c.deleted)
	}
}

func TestProbe_RefusesAnEmptyIdentity(t *testing.T) {
	got, err := teardown.Probe(context.Background(), adopt.Identity{}, []teardown.Class{&stub{kind: "ebs-volume"}})
	if err == nil {
		t.Fatal("an unvalidated identity must be refused")
	}
	if got.Absent {
		t.Fatal("a refused probe is not an absent one")
	}
}

// An untagged resource means "we could not tell", which is the Unsearched case, not a vote for absent.
func TestProbe_UntaggedResourceIsUnsearchedNotAbsent(t *testing.T) {
	c := &stub{kind: "ebs-volume", found: []teardown.Resource{
		{ID: "vol-untagged", Tags: map[string]string{}},
	}}
	got, _ := teardown.Probe(context.Background(), probeIdentity(), []teardown.Class{c})

	if got.Absent {
		t.Fatal("an untagged resource has no identity — we could not tell, so this must not be absent")
	}
	found := false
	for _, u := range got.Unsearched {
		if u == "ebs-volume" {
			found = true
		}
	}
	if !found {
		t.Fatalf("an untagged resource must mark its class UNSEARCHED, got %+v", got)
	}
}

func TestProbe_OwnedResourceInUseAppearsInRemaining(t *testing.T) {
	c := &stub{kind: "ebs-volume", found: []teardown.Resource{
		{ID: "vol-inuse", Tags: map[string]string{adopt.TagAppID: "app_1"}, InUse: true},
	}}
	got, err := teardown.Probe(context.Background(), probeIdentity(), []teardown.Class{c})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Absent {
		t.Fatal("our own in-use resource is affirmative evidence we are not absent")
	}
	if len(got.Remaining) != 1 || got.Remaining[0].ID != "vol-inuse" {
		t.Fatalf("an in-use resource of ours must be named in Remaining, got %+v", got.Remaining)
	}
}

func TestProbe_ForeignAppIDResourceStaysDropped(t *testing.T) {
	c := &stub{kind: "ebs-volume", found: []teardown.Resource{
		{ID: "vol-foreign", Tags: map[string]string{adopt.TagAppID: "app_2"}},
	}}
	got, err := teardown.Probe(context.Background(), probeIdentity(), []teardown.Class{c})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Absent {
		t.Fatalf("a foreign app's resource must not hold our teardown back: %+v", got)
	}
	if len(got.Remaining) != 0 || len(got.Unsearched) != 0 {
		t.Fatalf("a foreign app's resource must be dropped, not surfaced: %+v", got)
	}
}

func TestProbe_ForeignManagedByResourceStaysDropped(t *testing.T) {
	c := &stub{kind: "ebs-volume", found: []teardown.Resource{
		{ID: "vol-foreign-mb", Tags: map[string]string{
			adopt.TagAppID:     "app_1",
			adopt.TagManagedBy: "some-other-platform",
		}},
	}}
	got, err := teardown.Probe(context.Background(), probeIdentity(), []teardown.Class{c})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Absent {
		t.Fatalf("a resource managed by another platform must not hold our teardown back: %+v", got)
	}
	if len(got.Remaining) != 0 || len(got.Unsearched) != 0 {
		t.Fatalf("a foreign-managed resource must be dropped, not surfaced: %+v", got)
	}
}

// Sweep and Probe must never disagree about the same evidence, since Probe is what settles a subscription.
func TestProbeAndSweep_AgreeOnASharedFixture(t *testing.T) {
	fixture := []teardown.Resource{
		{ID: "vol-untagged", Tags: map[string]string{}},
		{ID: "vol-inuse", Tags: map[string]string{adopt.TagAppID: "app_1"}, InUse: true},
		{ID: "vol-foreign-app", Tags: map[string]string{adopt.TagAppID: "app_2"}},
		{ID: "vol-foreign-mb", Tags: map[string]string{
			adopt.TagAppID:     "app_1",
			adopt.TagManagedBy: "some-other-platform",
		}},
	}

	sweepStub := &stub{kind: "ebs-volume", found: fixture}
	sweepRep, sweepErr := teardown.Sweep(context.Background(), probeIdentity(), []teardown.Class{sweepStub})
	if sweepErr != nil {
		t.Fatalf("unexpected sweep error: %v", sweepErr)
	}
	if sweepRep.Clean() {
		t.Fatalf("sweep fixture sanity check failed — expected skips to make this not-clean: %+v", sweepRep)
	}

	probeStub := &stub{kind: "ebs-volume", found: fixture}
	probePresence, probeErr := teardown.Probe(context.Background(), probeIdentity(), []teardown.Class{probeStub})
	if probeErr != nil {
		t.Fatalf("unexpected probe error: %v", probeErr)
	}

	if probePresence.Absent {
		t.Fatalf("the sweep sees this app as not-clean (%+v) but the probe says absent:true (%+v) — "+
			"the same fixture must not yield contradictory verdicts", sweepRep, probePresence)
	}
	if len(probePresence.Remaining) != 1 || probePresence.Remaining[0].ID != "vol-inuse" {
		t.Errorf("Remaining must name exactly the in-use resource of ours, got %+v", probePresence.Remaining)
	}
	unsearched := false
	for _, u := range probePresence.Unsearched {
		if u == "ebs-volume" {
			unsearched = true
		}
	}
	if !unsearched {
		t.Errorf("Unsearched must carry the class the untagged resource made unprovable, got %+v", probePresence.Unsearched)
	}
}
