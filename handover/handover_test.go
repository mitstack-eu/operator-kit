package handover_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/handover"
)

// fakeAPI is a scriptable EC2 stand-in; states is consumed one per poll.
type fakeAPI struct {
	volumeID   string
	findErr    error
	snapshotID string
	createErr  error
	states     []string
	stateErr   error

	gotVolumeID string
	gotTags     map[string]string
	gotDesc     string
	createCalls int
	stateCalls  int
}

func (f *fakeAPI) FindDataVolume(context.Context, string) (string, error) {
	return f.volumeID, f.findErr
}

func (f *fakeAPI) CreateSnapshot(_ context.Context, volumeID, desc string, tags map[string]string) (string, error) {
	f.createCalls++
	f.gotVolumeID, f.gotDesc, f.gotTags = volumeID, desc, tags
	return f.snapshotID, f.createErr
}

func (f *fakeAPI) SnapshotState(context.Context, string) (string, error) {
	f.stateCalls++
	if f.stateErr != nil {
		return "", f.stateErr
	}
	if len(f.states) == 0 {
		return "pending", nil
	}
	s := f.states[0]
	if len(f.states) > 1 {
		f.states = f.states[1:]
	}
	return s, nil
}

// fakeClock advances only when Sleep is called, so the test never depends on wall time.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }
func (c *fakeClock) Now() time.Time        { return c.now }

func testOpts() handover.Options {
	return handover.Options{
		Timeout: time.Minute,
		Poll:    time.Second,
		Clock:   &fakeClock{now: time.Unix(1_700_000_000, 0)},
	}
}

func TestSnapshot_ReturnsIDOnceCompleted(t *testing.T) {
	api := &fakeAPI{volumeID: "vol-123", snapshotID: "snap-abc", states: []string{"pending", "pending", "completed"}}

	got, err := handover.Snapshot(context.Background(), api, "app_1", testOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "snap-abc" {
		t.Fatalf("snapshot id = %q, want snap-abc", got)
	}
	if api.gotVolumeID != "vol-123" {
		t.Fatalf("snapshotted %q, want the app's data volume", api.gotVolumeID)
	}
	if api.stateCalls < 3 {
		t.Fatalf("must poll until completed, polled %d times", api.stateCalls)
	}
}

// EC2 returns an id before the data is captured, so an incomplete snapshot must not read as success.
func TestSnapshot_PendingForeverTimesOutRatherThanReportingSuccess(t *testing.T) {
	api := &fakeAPI{volumeID: "vol-123", snapshotID: "snap-abc", states: []string{"pending"}}

	_, err := handover.Snapshot(context.Background(), api, "app_1", testOpts())
	if err == nil {
		t.Fatal("an incomplete snapshot must be an error — the caller destroys the volume next")
	}
	if !strings.Contains(err.Error(), "must NOT be destroyed") {
		t.Fatalf("the error must tell the caller not to proceed, got: %v", err)
	}
}

func TestSnapshot_ErrorStateIsAFailure(t *testing.T) {
	api := &fakeAPI{volumeID: "vol-123", snapshotID: "snap-abc", states: []string{"error"}}

	if _, err := handover.Snapshot(context.Background(), api, "app_1", testOpts()); err == nil {
		t.Fatal("a snapshot in state 'error' captured nothing and must not be reported as a handover")
	}
}

func TestSnapshot_EmptyIDIsRefused(t *testing.T) {
	api := &fakeAPI{volumeID: "vol-123", snapshotID: "", states: []string{"completed"}}

	if _, err := handover.Snapshot(context.Background(), api, "app_1", testOpts()); err == nil {
		t.Fatal("an empty snapshot id must be refused, not returned as a successful handover")
	}
}

func TestSnapshot_NilAPIIsLoud(t *testing.T) {
	_, err := handover.Snapshot(context.Background(), nil, "app_1", testOpts())
	if err == nil {
		t.Fatal("a nil API must error — silence here reads as 'nothing to hand over' and the volume is destroyed")
	}
}

func TestSnapshot_EmptyAppIDIsRefusedBeforeCreating(t *testing.T) {
	api := &fakeAPI{volumeID: "vol-123", snapshotID: "snap-abc", states: []string{"completed"}}

	if _, err := handover.Snapshot(context.Background(), api, "", testOpts()); err == nil {
		t.Fatal("an empty appID must be refused")
	}
	if api.createCalls != 0 {
		t.Fatal("nothing may be created before the appID is validated — an untagged snapshot cannot be found again")
	}
}

func TestSnapshot_NoVolumeIsADistinctSentinel(t *testing.T) {
	api := &fakeAPI{volumeID: ""}

	_, err := handover.Snapshot(context.Background(), api, "app_1", testOpts())
	if !errors.Is(err, handover.ErrNoVolume) {
		t.Fatalf("want ErrNoVolume so callers can continue the teardown, got %v", err)
	}
}

func TestSnapshot_FindErrorPropagates(t *testing.T) {
	api := &fakeAPI{findErr: errors.New("AccessDenied")}

	_, err := handover.Snapshot(context.Background(), api, "app_1", testOpts())
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("a lookup failure must surface, got %v", err)
	}
}

func TestSnapshot_CreateErrorPropagates(t *testing.T) {
	api := &fakeAPI{volumeID: "vol-123", createErr: errors.New("SnapshotLimitExceeded")}

	_, err := handover.Snapshot(context.Background(), api, "app_1", testOpts())
	if err == nil || !strings.Contains(err.Error(), "SnapshotLimitExceeded") {
		t.Fatalf("a create failure must surface, got %v", err)
	}
}

func TestSnapshot_IsTaggedAsARetainedHandover(t *testing.T) {
	api := &fakeAPI{volumeID: "vol-123", snapshotID: "snap-abc", states: []string{"completed"}}

	if _, err := handover.Snapshot(context.Background(), api, "app_1", testOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handover.IsHandover(api.gotTags) {
		t.Fatalf("the snapshot must be tagged as a handover or a sweep will reclaim it: %v", api.gotTags)
	}
	if api.gotTags[handover.TagAppID] != "app_1" {
		t.Fatalf("the snapshot must carry its app id, got %v", api.gotTags)
	}
}

func TestSnapshot_DescriptionIsASCII(t *testing.T) {
	api := &fakeAPI{volumeID: "vol-123", snapshotID: "snap-abc", states: []string{"completed"}}

	if _, err := handover.Snapshot(context.Background(), api, "app_1", testOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, r := range api.gotDesc {
		if r > 127 {
			t.Fatalf("description must be pure ASCII (EC2 rejects otherwise), got %q", api.gotDesc)
		}
	}
	if api.gotDesc == "" {
		t.Fatal("a description is how a human recognises the snapshot months later")
	}
}

func TestIsHandover(t *testing.T) {
	if handover.IsHandover(nil) {
		t.Fatal("nil tags must not read as a handover — an untagged snapshot is an orphan")
	}
	if handover.IsHandover(map[string]string{"Name": "something"}) {
		t.Fatal("an unrelated tag set must not read as a handover")
	}
	if !handover.IsHandover(handover.Tag("app_1")) {
		t.Fatal("Tag() and IsHandover must agree — a sweeper matching a tag nobody writes reclaims every handover")
	}
}

func TestSnapshot_ZeroOptionsAreUsable(t *testing.T) {
	api := &fakeAPI{volumeID: "vol-123", snapshotID: "snap-abc", states: []string{"completed"}}

	got, err := handover.Snapshot(context.Background(), api, "app_1", handover.Options{})
	if err != nil || got != "snap-abc" {
		t.Fatalf("zero Options must select working defaults, got (%q, %v)", got, err)
	}
}

func TestOutputAndParamKeysMatch(t *testing.T) {
	if handover.OutputKey != handover.ParamKey {
		t.Fatalf("OutputKey %q and ParamKey %q must match, or the handle never arrives",
			handover.OutputKey, handover.ParamKey)
	}
}
