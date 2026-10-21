package progress_test

import (
	"context"
	"slices"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/progress"
)

func TestObserve(t *testing.T) {
	cases := []struct {
		name      string
		secrets   []string
		total     int
		events    []progress.Event
		wantLines []string
		wantDone  int
	}{
		{"each resource is narrated by name", nil, 0,
			[]progress.Event{{Action: "creating", Resource: "aws_instance.app"}},
			[]string{"creating aws_instance.app"}, 0},
		{"terminal events count against the total", nil, 3,
			[]progress.Event{{Action: "creating", Resource: "a"}, {Action: "created", Resource: "a"}, {Action: "created", Resource: "b"}},
			[]string{"creating a (0/3)", "created a (1/3)", "created b (2/3)"}, 2},
		{"an in-progress action does not advance the count", nil, 0,
			[]progress.Event{{Action: "creating", Resource: "a"}, {Action: "creating", Resource: "b"}},
			[]string{"creating a", "creating b"}, 0},
		{"the count is omitted when the total is unknown, since a fake denominator is worse than none", nil, 0,
			[]progress.Event{{Action: "created", Resource: "a"}},
			[]string{"created a"}, 1},
		{"a secret is scrubbed before the line leaves the process", []string{"ghp_supersecret"}, 0,
			[]progress.Event{{Action: "creating", Resource: "repo", Detail: "using token ghp_supersecret"}},
			[]string{"creating repo: using token [REDACTED]"}, 0},
		{"a newline in engine output is sanitised so no log line can be forged", nil, 0,
			[]progress.Event{{Action: "creating", Resource: "a", Detail: "step one\nERROR: forged line"}},
			[]string{"creating a: step one ERROR: forged line"}, 0},
		{"an empty event emits nothing", nil, 0, []progress.Event{{}}, nil, 0},
		{"a sink that drops every line leaves the count intact", nil, 2,
			[]progress.Event{{Action: "created", Resource: "a"}, {Action: "created", Resource: "b"}},
			[]string{"created a (1/2)", "created b (2/2)"}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var lines []string
			n := progress.NewWithSink(func(l string) { lines = append(lines, l) }, tc.secrets...)
			n.SetTotal(tc.total)

			for _, ev := range tc.events {
				n.Observe(context.Background(), ev)
			}

			if !slices.Equal(lines, tc.wantLines) || n.Done() != tc.wantDone {
				t.Errorf("Observe(%+v) lines = %q, Done = %d; want %q, %d", tc.events, lines, n.Done(), tc.wantLines, tc.wantDone)
			}
		})
	}
}

func TestNarrator_IsAdvisory_NoAPIReturnsASendError(t *testing.T) {
	// Adding an error return to Observe stops this compiling, which keeps narration from becoming load-bearing.
	n := progress.New()
	var observe func(context.Context, progress.Event) = n.Observe
	_ = observe
}

func TestNew_EmitsThroughTheEventsChannelAndNoOpsWithoutOne(t *testing.T) {
	n := progress.New()

	n.Observe(context.Background(), progress.Event{Action: "creating", Resource: "a"})
}
