package teardown_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/teardown"
)

const credentialText = "not-a-real-token"

// spec is one Check: its answer, or no Present func at all when unset is true.
type spec struct {
	kind    string
	present bool
	err     error
	unset   bool
}

// harness builds the checks from specs, recording each kind a check was asked about, and captures what Options logs.
type harness struct {
	asked []string
	logs  []string
	opts  teardown.Options
}

func newHarness(scrub bool) *harness {
	h := &harness{}
	h.opts.Logf = func(format string, args ...any) { h.logs = append(h.logs, fmt.Sprintf(format, args...)) }
	if scrub {
		h.opts.Scrub = func(s string) string { return strings.ReplaceAll(s, credentialText, "[redacted]") }
	}
	return h
}

func (h *harness) checks(specs []spec) []teardown.Check {
	out := make([]teardown.Check, 0, len(specs))
	for _, s := range specs {
		c := teardown.Check{Kind: s.kind}
		if !s.unset {
			c.Present = func(context.Context) (bool, error) { h.asked = append(h.asked, s.kind); return s.present, s.err }
		}
		out = append(out, c)
	}
	return out
}

func couldNotConfirm(kind, err string) string {
	return "teardown: " + kind + ": could not confirm (error is not evidence): " + err
}

func TestSurvivor(t *testing.T) {
	cases := []struct {
		name      string
		specs     []spec
		scrub     bool
		want      string
		wantAsked []string
		wantLogs  []string
	}{
		{"a confident positive is the survivor", []spec{{kind: "instance", present: true}}, false, "instance", []string{"instance"}, nil},
		{"an error is logged and passed over, never read as a survivor", []spec{{kind: "instance", err: errors.New("throttled by provider")}}, false, "",
			[]string{"instance"}, []string{couldNotConfirm("instance", "throttled by provider")}},
		{"three errors are three unanswered questions, each logged, not one survivor", []spec{
			{kind: "instance", err: errors.New("timeout")}, {kind: "volume", err: errors.New("access denied")}, {kind: "eip", present: true, err: errors.New("rate limited")},
		}, false, "", []string{"instance", "volume", "eip"},
			[]string{couldNotConfirm("instance", "timeout"), couldNotConfirm("volume", "access denied"), couldNotConfirm("eip", "rate limited")}},
		{"a confident negative is passed over", []spec{{kind: "instance"}, {kind: "volume", present: true}}, false, "volume", []string{"instance", "volume"}, nil},
		{"present true with an error is not a survivor", []spec{{kind: "instance", present: true, err: errors.New("ambiguous response")}}, false, "",
			[]string{"instance"}, []string{couldNotConfirm("instance", "ambiguous response")}},
		{"a nil Present is logged by kind and is no survivor", []spec{{kind: "oauth-app", unset: true}}, false, "", nil,
			[]string{"teardown: oauth-app: no Present func — could not ask, not treated as gone or as a survivor"}},
		{"the first survivor wins and the second check is never asked", []spec{{kind: "instance", present: true}, {kind: "volume", present: true}}, false, "instance", []string{"instance"}, nil},
		{"zero checks are logged, never a silent answer", nil, false, "", nil, []string{"teardown: Survivor called with zero checks — nothing was asked, this reports nothing"}},
		{"Scrub applies to a logged error", []spec{{kind: "instance", err: errors.New("auth failed with " + credentialText)}}, true, "",
			[]string{"instance"}, []string{couldNotConfirm("instance", "auth failed with [redacted]")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(tc.scrub)

			got := teardown.Survivor(context.Background(), h.opts, h.checks(tc.specs)...)

			if got != tc.want || !slices.Equal(h.asked, tc.wantAsked) || !slices.Equal(h.logs, tc.wantLogs) {
				t.Errorf("Survivor = %q, asked %q, logged %q; want %q, %q, %q", got, h.asked, h.logs, tc.want, tc.wantAsked, tc.wantLogs)
			}
		})
	}
}

func TestSurvivor_ZeroOptionsDefaultEverySideEffect(t *testing.T) {
	got := teardown.Survivor(context.Background(), teardown.Options{}, newHarness(false).checks([]spec{{kind: "instance", present: true}})...)

	if got != "instance" {
		t.Errorf("Survivor with zero Options = %q, want instance", got)
	}
}

func TestSurvivor_PassesItsContextThroughUnchanged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var seen context.Context
	check := teardown.Check{Kind: "instance", Present: func(c context.Context) (bool, error) { seen = c; return false, nil }}

	teardown.Survivor(ctx, teardown.Options{}, check)

	if seen != ctx {
		t.Errorf("Present saw context %v, want the exact cancelled context Survivor was given", seen)
	}
}

func TestVerify(t *testing.T) {
	cases := []struct {
		name        string
		specs       []spec
		scrub       bool
		detail      func(string) string
		emitErr     error
		want        bool
		wantEmitted []string
		wantLogs    []string
	}{
		{"a survivor is emitted exactly once with Options.Detail's text", []spec{{kind: "instance", present: true}}, false,
			func(k string) string { return "left behind: " + k }, nil, true, []string{"left behind: instance"}, []string{"teardown: survivor confirmed: left behind: instance"}},
		{"no survivor emits nothing", []spec{{kind: "instance"}}, false, nil, nil, false, nil, nil},
		{"a failed emit still reports the survivor and logs both", []spec{{kind: "instance", present: true}}, false,
			func(k string) string { return "teardown left " + k + " in place" }, errors.New("ingest endpoint unreachable"), true, []string{"teardown left instance in place"},
			[]string{"teardown: survivor confirmed: teardown left instance in place", "teardown: failed to report survivor instance: ingest endpoint unreachable"}},
		{"Scrub covers the emitted detail and the log", []spec{{kind: "oauth-app", present: true}}, true,
			func(k string) string { return "found leftover credential " + credentialText + " on " + k }, nil, true, []string{"found leftover credential [redacted] on oauth-app"},
			[]string{"teardown: survivor confirmed: found leftover credential [redacted] on oauth-app"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(tc.scrub)
			var emitted []string
			h.opts.Emit = func(_ context.Context, d string) error { emitted = append(emitted, d); return tc.emitErr }
			h.opts.Detail = tc.detail

			got := teardown.Verify(context.Background(), h.opts, h.checks(tc.specs)...)

			if got != tc.want || !slices.Equal(emitted, tc.wantEmitted) || !slices.Equal(h.logs, tc.wantLogs) {
				t.Errorf("Verify = %v, emitted %q, logged %q; want %v, %q, %q", got, emitted, h.logs, tc.want, tc.wantEmitted, tc.wantLogs)
			}
		})
	}
}

// events.EmitFailed, the default Emit, is a no-op on a context carrying no events envelope.
func TestVerify_ZeroOptionsDefaultEmitToEventsEmitFailed(t *testing.T) {
	got := teardown.Verify(context.Background(), teardown.Options{}, newHarness(false).checks([]spec{{kind: "instance", present: true}})...)

	if !got {
		t.Error("Verify with zero Options = false, want true for a confirmed survivor")
	}
}

func TestAbsent(t *testing.T) {
	cases := []struct {
		name     string
		specs    []spec
		scrub    bool
		want     bool
		wantLogs []string
	}{
		{"every check asked and gone is absent", []spec{{kind: "instance"}, {kind: "volume"}}, false, true, nil},
		{"one confident presence defeats absence", []spec{{kind: "instance"}, {kind: "volume", present: true}}, false, false, nil},
		{"one error defeats absence even when the others are gone, and is logged", []spec{{kind: "instance"}, {kind: "volume", err: errors.New("throttled")}}, false, false,
			[]string{couldNotConfirm("volume", "throttled")}},
		{"present true with an error is evidence of nothing", []spec{{kind: "instance", present: true, err: errors.New("ambiguous response")}}, false, false,
			[]string{couldNotConfirm("instance", "ambiguous response")}},
		{"a nil Present is logged by kind and defeats absence", []spec{{kind: "oauth-app", unset: true}}, false, false,
			[]string{"teardown: oauth-app: no Present func — could not ask, not treated as absent"}},
		{"zero checks are no evidence of absence, and are logged", nil, false, false, []string{"teardown: Absent called with zero checks — no evidence to assert absence from"}},
		{"Scrub applies to a logged error", []spec{{kind: "instance", err: errors.New("auth failed with " + credentialText)}}, true, false,
			[]string{couldNotConfirm("instance", "auth failed with [redacted]")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(tc.scrub)

			got := teardown.Absent(context.Background(), h.opts, h.checks(tc.specs)...)

			if got != tc.want || !slices.Equal(h.logs, tc.wantLogs) {
				t.Errorf("Absent = %v, logged %q; want %v, %q", got, h.logs, tc.want, tc.wantLogs)
			}
		})
	}
}
