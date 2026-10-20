package teardown_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/adopt"
	"github.com/mitstack-eu/operator-kit/v2/teardown"
)

func id() adopt.Identity { return adopt.Identity{AppID: "app-42"} }

func owned(rid string) teardown.Resource {
	return teardown.Resource{Kind: "instance", ID: rid, Tags: map[string]string{adopt.TagAppID: "app-42"}}
}

func tagged(rid string, tags map[string]string) teardown.Resource {
	return teardown.Resource{Kind: "instance", ID: rid, Tags: tags}
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

func classes(stubs []*stub) []teardown.Class {
	out := make([]teardown.Class, len(stubs))
	for i, s := range stubs {
		out[i] = s
	}
	return out
}

// Every row compares the whole report through its String rendering, so a reclaim, skip, failure or unsearched class cannot go unreported.
func TestSweep(t *testing.T) {
	inUse := owned("i-1")
	inUse.InUse = true
	cases := []struct {
		name        string
		identity    adopt.Identity
		stubs       []*stub
		wantDeleted [][]string
		wantErr     string
		wantReport  string // empty for a refused sweep, whose report means nothing
		wantClean   bool
	}{
		{"it reclaims exactly what the detector selected", id(), []*stub{{kind: "instance", found: []teardown.Resource{owned("i-1"), owned("i-2")}}},
			[][]string{{"i-1", "i-2"}}, "", "reclaimed 2 instance/i-1 instance/i-2", true},
		{"another app's resource is never touched", id(), []*stub{{kind: "instance", found: []teardown.Resource{owned("i-1"), tagged("i-foreign", map[string]string{adopt.TagAppID: "app-99"})}}},
			[][]string{{"i-1"}}, "", "reclaimed 1 instance/i-1; skipped 1 instance/i-foreign(belongs to app-99)", false},
		{"an untagged resource is never reclaimed and is reported as skipped", id(), []*stub{{kind: "instance", found: []teardown.Resource{tagged("i-untagged", map[string]string{})}}},
			[][]string{nil}, "", "reclaimed 0; skipped 1 instance/i-untagged(untagged — no identity, so it cannot be proven ours)", false},
		{"a resource still in use is skipped and says why", id(), []*stub{{kind: "instance", found: []teardown.Resource{inUse}}},
			[][]string{nil}, "", "reclaimed 0; skipped 1 instance/i-1(in use)", false},
		{"nothing left is a clean success", id(), []*stub{{kind: "instance"}}, [][]string{nil}, "", "reclaimed 0", true},
		{"a delete of a resource that already vanished is success", id(), []*stub{{kind: "instance", found: []teardown.Resource{owned("i-1")},
			delErr: errors.New("InvalidInstanceID.NotFound: The instance ID 'i-1' does not exist")}},
			[][]string{{"i-1"}}, "", "reclaimed 1 instance/i-1", true},
		{"a genuine delete failure is returned naming the resource still costing money", id(), []*stub{{kind: "instance", found: []teardown.Resource{owned("i-1")},
			delErr: errors.New("DependencyViolation: resource is attached")}},
			[][]string{{"i-1"}}, "i-1", "reclaimed 0; FAILED 1 instance/i-1", false},
		{"one class failing does not stop the others, and both are reported", id(), []*stub{
			{kind: "volume", found: []teardown.Resource{owned("vol-1")}, delErr: errors.New("DependencyViolation")},
			{kind: "eip", found: []teardown.Resource{owned("eip-1")}},
		}, [][]string{{"vol-1"}, {"eip-1"}}, "vol-1", "reclaimed 1 instance/eip-1; FAILED 1 instance/vol-1", false},
		{"a discovery failure is unsearched, never an all-clear", id(), []*stub{{kind: "instance", findErr: errors.New("AccessDenied")}},
			[][]string{nil}, "AccessDenied", "reclaimed 0; UNSEARCHED instance", false},
		{"an identity that would match everything is refused before any delete", adopt.Identity{}, []*stub{{kind: "instance", found: []teardown.Resource{owned("i-1")}}},
			[][]string{nil}, "refusing to sweep", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := teardown.Sweep(context.Background(), tc.identity, classes(tc.stubs))

			if (err != nil) != (tc.wantErr != "") || (err != nil && !strings.Contains(err.Error(), tc.wantErr)) {
				t.Errorf("Sweep error = %v, want one naming %q (none when empty)", err, tc.wantErr)
			}
			if got := rep.String(); tc.wantReport != "" && (got != tc.wantReport || rep.Clean() != tc.wantClean) {
				t.Errorf("report = %q (clean %v), want %q (clean %v)", got, rep.Clean(), tc.wantReport, tc.wantClean)
			}
			for i, s := range tc.stubs {
				if !slices.Equal(s.deleted, tc.wantDeleted[i]) {
					t.Errorf("class %s deleted %q, want %q", s.kind, s.deleted, tc.wantDeleted[i])
				}
			}
		})
	}
}

func TestIsNotFound(t *testing.T) {
	for _, tc := range []struct {
		msg  string
		want bool
	}{
		{"InvalidInstanceID.NotFound: does not exist", true},
		{"NoSuchBucket: The specified bucket does not exist", true},
		{"ResourceNotFoundException", true},
		{"NoSuchEntity: The role with name x cannot be found", true},
		{"InvalidAllocationID.NotFound", true},
		{"404 Not Found", true},
		{`claims.aws.mitstack.dev "app" not found`, true},
		{"Error from server (NotFound): pods not found", true},
		{"secret not-found", true},
		{"DependencyViolation: resource is attached", false},
		{"AccessDenied", false},
	} {
		if got := teardown.IsNotFound(errors.New(tc.msg)); got != tc.want {
			t.Errorf("IsNotFound(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
	if teardown.IsNotFound(nil) {
		t.Error("IsNotFound(nil) = true, want false")
	}
}
