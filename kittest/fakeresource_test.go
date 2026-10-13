// fakeresource_test.go pins kittest.FakeResource, the Resource whose
// behaviour a test dictates. Its method set matches handler.Resource
// structurally rather than by declaration, so neither this file nor the
// FakeResource it exercises imports the handler package, and kittest stays
// importable from a handler test without a cycle.
package kittest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

func testIdentity() contract.Identity {
	return contract.Identity{SubscriptionID: "sub-1", BlockID: "block-1", OrgID: "org-1"}
}

func testDesired() contract.Desired {
	return contract.Desired{Identity: testIdentity()}
}

// TestFakeResource_ObserveReflectsCountByDefault pins the documented
// default: Observe reports Present when Count > 0 and Absent when
// Count == 0, driven by Ensure/Delete converging Count, unless
// ObserveState overrides it.
func TestFakeResource_ObserveReflectsCountByDefault(t *testing.T) {
	f := &kittest.FakeResource{}
	ctx := context.Background()

	obs, err := f.Observe(ctx, testDesired())
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.State != contract.Absent {
		t.Errorf("State = %v, want Absent when Count == 0", obs.State)
	}

	if err := f.Ensure(ctx, testDesired()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	obs, err = f.Observe(ctx, testDesired())
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.State != contract.Present {
		t.Errorf("State = %v, want Present when Count > 0", obs.State)
	}

	if _, err := f.Delete(ctx, testDesired()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	obs, err = f.Observe(ctx, testDesired())
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.State != contract.Absent {
		t.Errorf("State = %v, want Absent after Delete converges Count back to 0", obs.State)
	}
}

// TestFakeResource_ObserveStateOverridesCount pins that a set ObserveState
// is reported regardless of Count.
func TestFakeResource_ObserveStateOverridesCount(t *testing.T) {
	unknown := contract.Unknown
	f := &kittest.FakeResource{Count: 1, ObserveState: &unknown}

	obs, err := f.Observe(context.Background(), testDesired())
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.State != contract.Unknown {
		t.Errorf("State = %v, want the overridden Unknown even though Count > 0", obs.State)
	}
}

// With DeadReportedPending set, Observe answers Unknown with no error even where ObserveErr is also set.
func TestFakeResource_DeadReportedPendingReportsUnknownEvenWithObserveErrSet(t *testing.T) {
	f := &kittest.FakeResource{
		DeadReportedPending: true,
		ObserveErr:          errors.New("this would normally be a permanent failure"),
	}

	obs, err := f.Observe(context.Background(), testDesired())
	if err != nil {
		t.Fatalf("Observe: %v, want no error even though ObserveErr is set (DeadReportedPending)", err)
	}
	if obs.State != contract.Unknown {
		t.Errorf("State = %v, want Unknown", obs.State)
	}
}

// TestFakeResource_ObserveReportsOutputsAndStatusDetail pins that Observe
// surfaces the fake's own Outputs/StatusDetail fields verbatim.
func TestFakeResource_ObserveReportsOutputsAndStatusDetail(t *testing.T) {
	f := &kittest.FakeResource{
		Outputs:      map[string]string{"url": "https://example.test"},
		StatusDetail: "all good",
	}

	obs, err := f.Observe(context.Background(), testDesired())
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.Outputs["url"] != "https://example.test" {
		t.Errorf("Outputs[url] = %q, want the fake's own output", obs.Outputs["url"])
	}
	if obs.StatusDetail != "all good" {
		t.Errorf("StatusDetail = %q, want the fake's own detail", obs.StatusDetail)
	}
}

// TestFakeResource_EnsureObserveDeleteReturnTheirConfiguredErrors pins that
// each method returns the matching *Err field when set.
func TestFakeResource_EnsureObserveDeleteReturnTheirConfiguredErrors(t *testing.T) {
	ensureErr := errors.New("ensure failed")
	observeErr := errors.New("observe failed")
	deleteErr := errors.New("delete failed")
	f := &kittest.FakeResource{EnsureErr: ensureErr, ObserveErr: observeErr, DeleteErr: deleteErr}
	ctx := context.Background()

	if err := f.Ensure(ctx, testDesired()); !errors.Is(err, ensureErr) {
		t.Errorf("Ensure error = %v, want %v", err, ensureErr)
	}
	if _, err := f.Observe(ctx, testDesired()); !errors.Is(err, observeErr) {
		t.Errorf("Observe error = %v, want %v", err, observeErr)
	}
	if _, err := f.Delete(ctx, testDesired()); !errors.Is(err, deleteErr) {
		t.Errorf("Delete error = %v, want %v", err, deleteErr)
	}
}

// TestFakeResource_DuplicateOnEnsureAddsARresourceEveryCall pins the
// harness-breaking behaviour: with DuplicateOnEnsure, a second Ensure call
// increases Count again instead of converging.
func TestFakeResource_DuplicateOnEnsureAddsAResourceEveryCall(t *testing.T) {
	f := &kittest.FakeResource{DuplicateOnEnsure: true}
	ctx := context.Background()

	if err := f.Ensure(ctx, testDesired()); err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	first := f.Count
	if err := f.Ensure(ctx, testDesired()); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if f.Count <= first {
		t.Errorf("Count after a retried Ensure = %d, want it to have grown past %d (DuplicateOnEnsure)", f.Count, first)
	}
}

// TestFakeResource_LeakOnDeleteLeavesOneResourceBehind pins that Delete
// under LeakOnDelete does not bring Count to zero.
func TestFakeResource_LeakOnDeleteLeavesOneResourceBehind(t *testing.T) {
	f := &kittest.FakeResource{LeakOnDelete: true}
	ctx := context.Background()

	if err := f.Ensure(ctx, testDesired()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := f.Delete(ctx, testDesired()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if f.Count == 0 {
		t.Error("Count == 0 after Delete, want at least one resource left behind (LeakOnDelete)")
	}
}

// TestFakeResource_ErrorWhenAlreadyAbsentFailsDeleteOnNothingProvisioned
// pins that Delete on an already-absent resource errors under this flag.
func TestFakeResource_ErrorWhenAlreadyAbsentFailsDeleteOnNothingProvisioned(t *testing.T) {
	f := &kittest.FakeResource{ErrorWhenAlreadyAbsent: true}

	if _, err := f.Delete(context.Background(), testDesired()); err == nil {
		t.Error("Delete on a never-provisioned resource returned nil, want an error (ErrorWhenAlreadyAbsent)")
	}
}

// TestFakeResource_ClobberOnUpgradeDropsPriorOutputsOnASecondEnsure pins
// the destructive-upgrade fake: a second Ensure call must drop whatever
// Outputs the first one left in place.
func TestFakeResource_ClobberOnUpgradeDropsPriorOutputsOnASecondEnsure(t *testing.T) {
	f := &kittest.FakeResource{
		Outputs:          map[string]string{"url": "https://example.test"},
		ClobberOnUpgrade: true,
	}
	ctx := context.Background()

	if err := f.Ensure(ctx, testDesired()); err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	obs, err := f.Observe(ctx, testDesired())
	if err != nil {
		t.Fatalf("Observe after first Ensure: %v", err)
	}
	if obs.Outputs["url"] == "" {
		t.Fatal("the first Ensure must not itself have dropped the seeded outputs")
	}

	if err := f.Ensure(ctx, testDesired()); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	obs, err = f.Observe(ctx, testDesired())
	if err != nil {
		t.Fatalf("Observe after second Ensure: %v", err)
	}
	if obs.Outputs["url"] != "" {
		t.Error("outputs survived a second Ensure under ClobberOnUpgrade, want them dropped (a destructive upgrade)")
	}
}

// TestFakeResource_CallCountersRecordEveryCall pins EnsureCalls/
// ObserveCalls/DeleteCalls as counts of calls made, not of successes.
func TestFakeResource_CallCountersRecordEveryCall(t *testing.T) {
	f := &kittest.FakeResource{EnsureErr: errors.New("boom")}
	ctx := context.Background()

	_ = f.Ensure(ctx, testDesired())
	_ = f.Ensure(ctx, testDesired())
	_, _ = f.Observe(ctx, testDesired())
	_, _ = f.Observe(ctx, testDesired())
	_, _ = f.Observe(ctx, testDesired())
	_, _ = f.Delete(ctx, testDesired())

	if f.EnsureCalls != 2 {
		t.Errorf("EnsureCalls = %d, want 2 (counted even though each call errored)", f.EnsureCalls)
	}
	if f.ObserveCalls != 3 {
		t.Errorf("ObserveCalls = %d, want 3", f.ObserveCalls)
	}
	if f.DeleteCalls != 1 {
		t.Errorf("DeleteCalls = %d, want 1", f.DeleteCalls)
	}
}
