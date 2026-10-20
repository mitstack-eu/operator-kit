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

func api(states ...string) *fakeAPI {
	return &fakeAPI{volumeID: "vol-123", snapshotID: "snap-abc", states: states}
}

func TestSnapshot(t *testing.T) {
	cases := []struct {
		name      string
		api       *fakeAPI
		appID     string
		zeroOpts  bool
		wantID    string
		wantErrIs error
		wantInErr string
		check     func(t *testing.T, f *fakeAPI)
	}{
		{"it polls the app's data volume's snapshot until completed and returns its id", api("pending", "pending", "completed"), "app_1", false, "snap-abc", nil, "",
			func(t *testing.T, f *fakeAPI) {
				if f.gotVolumeID != "vol-123" || f.stateCalls < 3 {
					t.Errorf("snapshotted %q and polled %d times; want vol-123 polled until completed", f.gotVolumeID, f.stateCalls)
				}
			}},
		{"the snapshot is tagged as a retained handover of its app", api("completed"), "app_1", false, "snap-abc", nil, "",
			func(t *testing.T, f *fakeAPI) {
				if !handover.IsHandover(f.gotTags) || f.gotTags[handover.TagAppID] != "app_1" {
					t.Errorf("tags = %v; want a handover tag carrying app_1, or a sweep reclaims it", f.gotTags)
				}
			}},
		{"the description is non-empty ASCII, since EC2 rejects anything else", api("completed"), "app_1", false, "snap-abc", nil, "",
			func(t *testing.T, f *fakeAPI) {
				if f.gotDesc == "" || strings.IndexFunc(f.gotDesc, func(r rune) bool { return r > 127 }) >= 0 {
					t.Errorf("description = %q, want non-empty ASCII", f.gotDesc)
				}
			}},
		{"zero Options select working defaults", api("completed"), "app_1", true, "snap-abc", nil, "", nil},
		{"a snapshot pending past the timeout tells the caller not to destroy the volume", api("pending"), "app_1", false, "", nil, "must NOT be destroyed", nil},
		{"a snapshot in state error captured nothing", api("error"), "app_1", false, "", nil, "NOT captured", nil},
		{"an empty snapshot id is refused", &fakeAPI{volumeID: "vol-123", states: []string{"completed"}}, "app_1", false, "", nil, "returned no id", nil},
		{"a nil API is loud, never read as nothing to hand over", nil, "app_1", false, "", nil, "no EC2 API configured", nil},
		{"an empty appID is refused before anything is created", api("completed"), "", false, "", nil, "appID is required",
			func(t *testing.T, f *fakeAPI) {
				if f.createCalls != 0 {
					t.Errorf("CreateSnapshot called %d times; an untagged snapshot cannot be found again", f.createCalls)
				}
			}},
		{"no data volume is the ErrNoVolume sentinel, so teardown can continue", &fakeAPI{}, "app_1", false, "", handover.ErrNoVolume, "", nil},
		{"a lookup failure surfaces", &fakeAPI{findErr: errors.New("AccessDenied")}, "app_1", false, "", nil, "AccessDenied", nil},
		{"a create failure surfaces", &fakeAPI{volumeID: "vol-123", createErr: errors.New("SnapshotLimitExceeded")}, "app_1", false, "", nil, "SnapshotLimitExceeded", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := handover.Options{Timeout: time.Minute, Poll: time.Second, Clock: &fakeClock{now: time.Unix(1_700_000_000, 0)}}
			if tc.zeroOpts {
				opts = handover.Options{}
			}
			var ec2 handover.API
			if tc.api != nil {
				ec2 = tc.api
			}

			got, err := handover.Snapshot(context.Background(), ec2, tc.appID, opts)

			if tc.wantID != "" && (err != nil || got != tc.wantID) {
				t.Fatalf("Snapshot = %q, %v; want %q", got, err, tc.wantID)
			}
			if tc.wantID == "" && err == nil {
				t.Fatalf("Snapshot = %q with no error; want a failure, since the caller destroys the volume next", got)
			}
			if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
				t.Errorf("Snapshot error = %v, want %v", err, tc.wantErrIs)
			}
			if tc.wantInErr != "" && !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("Snapshot error = %v, want it to say %q", err, tc.wantInErr)
			}
			if tc.check != nil {
				tc.check(t, tc.api)
			}
		})
	}
}

func TestIsHandover(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags map[string]string
		want bool
	}{
		{"nil tags are an orphan", nil, false},
		{"an unrelated tag set is not a handover", map[string]string{"Name": "something"}, false},
		{"Tag and IsHandover agree, or a sweep reclaims every handover", handover.Tag("app_1"), true},
	} {
		if got := handover.IsHandover(tc.tags); got != tc.want {
			t.Errorf("%s: IsHandover(%v) = %v, want %v", tc.name, tc.tags, got, tc.want)
		}
	}
}

func TestOutputAndParamKeysMatch(t *testing.T) {
	if handover.OutputKey != handover.ParamKey {
		t.Fatalf("OutputKey %q and ParamKey %q must match, or the handle never arrives", handover.OutputKey, handover.ParamKey)
	}
}
