package teardown_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/adopt"
	"github.com/mitstack-eu/operator-kit/v3/teardown"
)

func probeIdentity() adopt.Identity {
	return adopt.Identity{AppID: "app_1", Name: "checkout"}
}

func volume(rid string, tags map[string]string, inUse bool) teardown.Resource {
	return teardown.Resource{ID: rid, Tags: tags, InUse: inUse}
}

var (
	ours          = map[string]string{adopt.TagAppID: "app_1"}
	anotherApp    = map[string]string{adopt.TagAppID: "app_2"}
	otherPlatform = map[string]string{adopt.TagAppID: "app_1", adopt.TagManagedBy: "some-other-platform"}
)

func ids(rs []teardown.Resource) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

// Probe runs on live subscriptions too, so every row also checks that it deleted nothing.
func TestProbe(t *testing.T) {
	cases := []struct {
		name           string
		identity       adopt.Identity
		stubs          []*stub
		wantErr        bool
		wantAbsent     bool
		wantRemaining  []string
		wantUnsearched []string
	}{
		{"nothing found across every class is absent", probeIdentity(), []*stub{{kind: "ebs-volume"}}, false, true, nil, nil},
		{"a resource still there is not absent and is named", probeIdentity(), []*stub{{kind: "ebs-volume", found: []teardown.Resource{volume("vol-1", ours, false)}}},
			false, false, []string{"vol-1"}, nil},
		{"a discovery failure surfaces and names the class it could not search", probeIdentity(), []*stub{{kind: "ebs-volume", findErr: errors.New("throttled")}},
			true, false, nil, []string{"ebs-volume"}},
		{"one unsearchable class defeats the clean ones", probeIdentity(), []*stub{{kind: "ebs-volume"}, {kind: "ecr-repository", findErr: errors.New("access denied")}},
			true, false, nil, []string{"ecr-repository"}},
		{"another app's resource is dropped and does not hold us back", probeIdentity(), []*stub{{kind: "ebs-volume", found: []teardown.Resource{volume("vol-other", anotherApp, false)}}},
			false, true, nil, nil},
		{"a resource another platform manages is dropped", probeIdentity(), []*stub{{kind: "ebs-volume", found: []teardown.Resource{volume("vol-foreign-mb", otherPlatform, false)}}},
			false, true, nil, nil},
		{"an untagged resource marks its class unsearched, never absent", probeIdentity(), []*stub{{kind: "ebs-volume", found: []teardown.Resource{volume("vol-untagged", map[string]string{}, false)}}},
			false, false, nil, []string{"ebs-volume"}},
		{"our own resource in use is affirmative evidence and is named", probeIdentity(), []*stub{{kind: "ebs-volume", found: []teardown.Resource{volume("vol-inuse", ours, true)}}},
			false, false, []string{"vol-inuse"}, nil},
		{"an empty identity is refused, and a refused probe is not absent", adopt.Identity{}, []*stub{{kind: "ebs-volume"}}, true, false, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := teardown.Probe(context.Background(), tc.identity, classes(tc.stubs))

			if (err != nil) != tc.wantErr {
				t.Errorf("Probe error = %v, want error %v", err, tc.wantErr)
			}
			if got.Absent != tc.wantAbsent || !slices.Equal(ids(got.Remaining), tc.wantRemaining) || !slices.Equal(got.Unsearched, tc.wantUnsearched) {
				t.Errorf("Probe = absent %v, remaining %q, unsearched %q; want %v, %q, %q", got.Absent, ids(got.Remaining), got.Unsearched, tc.wantAbsent, tc.wantRemaining, tc.wantUnsearched)
			}
			for _, s := range tc.stubs {
				if len(s.deleted) != 0 {
					t.Errorf("Probe deleted %v from %s; it is a read", s.deleted, s.kind)
				}
			}
		})
	}
}

// Sweep and Probe must never disagree about the same evidence, since Probe is what settles a subscription.
func TestProbeAndSweep_AgreeOnASharedFixture(t *testing.T) {
	fixture := []teardown.Resource{
		volume("vol-untagged", map[string]string{}, false),
		volume("vol-inuse", ours, true),
		volume("vol-foreign-app", anotherApp, false),
		volume("vol-foreign-mb", otherPlatform, false),
	}

	sweep, sweepErr := teardown.Sweep(context.Background(), probeIdentity(), []teardown.Class{&stub{kind: "ebs-volume", found: fixture}})
	probe, probeErr := teardown.Probe(context.Background(), probeIdentity(), []teardown.Class{&stub{kind: "ebs-volume", found: fixture}})

	if sweepErr != nil || probeErr != nil {
		t.Fatalf("Sweep error %v, Probe error %v; want neither", sweepErr, probeErr)
	}
	if sweep.Clean() || probe.Absent {
		t.Errorf("sweep clean %v (%s), probe absent %v; want the same fixture to read as not done by both", sweep.Clean(), sweep, probe.Absent)
	}
	if !slices.Equal(ids(probe.Remaining), []string{"vol-inuse"}) || !slices.Equal(probe.Unsearched, []string{"ebs-volume"}) {
		t.Errorf("probe remaining %q, unsearched %q; want [vol-inuse] and [ebs-volume]", ids(probe.Remaining), probe.Unsearched)
	}
}
