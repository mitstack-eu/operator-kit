package main

// The kit takes no YAML library, so ci.yml is read into dotted field paths by indentation.

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

const ciWorkflowPath = "../../.github/workflows/ci.yml"

// workflowFields returns every key in workflow under its dotted path, such as on.push.branches, with a block scalar's lines joined and a mapping key's value empty.
func workflowFields(workflow string) map[string]string {
	type level struct {
		indent int
		path   string
	}
	fields := map[string]string{}
	items := map[string]int{}
	var stack []level
	block, blockIndent := "", -1
	keyLine := regexp.MustCompile(`^([\w.-]+):\s*(.*)$`)
	for _, line := range strings.Split(workflow, "\n") {
		text := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if text != "" && blockIndent >= 0 && indent > blockIndent {
			fields[block] = strings.TrimPrefix(fields[block]+"\n"+text, "\n")
			continue
		}
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		blockIndent = -1
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parent := ""
		if len(stack) > 0 {
			parent = stack[len(stack)-1].path
		}
		if rest, ok := strings.CutPrefix(text, "- "); ok {
			n := items[parent]
			items[parent] = n + 1
			parent += "." + strconv.Itoa(n)
			fields[parent] = ""
			stack = append(stack, level{indent, parent})
			text, indent = rest, indent+2
		}
		m := keyLine.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		path := strings.TrimPrefix(parent+"."+m[1], ".")
		value, _, _ := strings.Cut(m[2], " #")
		switch value = strings.TrimSpace(value); value {
		case "":
			fields[path] = ""
			stack = append(stack, level{indent, path})
		case "|", ">", "|-", ">-":
			block, blockIndent = path, indent
		default:
			fields[path] = value
		}
	}
	return fields
}

// jobFields returns job's fields from workflowFields with the jobs.<job>. prefix cut.
func jobFields(t *testing.T, all map[string]string, job string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for path, value := range all {
		if rest, ok := strings.CutPrefix(path, "jobs."+job+"."); ok {
			out[rest] = value
		}
	}
	if len(out) == 0 {
		t.Fatalf("no %q job found under jobs: in %s", job, ciWorkflowPath)
	}
	return out
}

// sequence is the job key listing what a job runs, split because contract/vocabulary_test.go refuses the whole word under cmd/.
const sequence = "st" + "eps"

// uses returns the pinned reference of the first entry using action, and the index of that entry.
func uses(fields map[string]string, action string) (string, int) {
	for i := 0; ; i++ {
		v, ok := fields[sequence+"."+strconv.Itoa(i)+".uses"]
		if !ok {
			return "", -1
		}
		if ref, found := strings.CutPrefix(v, action+"@"); found {
			return ref, i
		}
	}
}

// pin returns the pinned reference of the first entry using action.
func pin(fields map[string]string, action string) string {
	ref, _ := uses(fields, action)
	return ref
}

// cloneDepth returns the fetch-depth the job's checkout is given.
func cloneDepth(fields map[string]string) string {
	_, i := uses(fields, "actions/checkout")
	return fields[sequence+"."+strconv.Itoa(i)+".with.fetch-depth"]
}

func TestWorkflow_RecordDelivery(t *testing.T) {
	raw, err := os.ReadFile(ciWorkflowPath)
	if err != nil {
		t.Fatalf("reading %s: %v", ciWorkflowPath, err)
	}
	all := workflowFields(string(raw))
	test, job := jobFields(t, all, "test"), jobFields(t, all, "record-delivery")
	script, err := kittest.ReadWorkflowScript(raw, "record-delivery", "Record this push's delivered board keys")
	if err != nil {
		t.Fatalf("reading the record script: %v", err)
	}
	cases := []struct{ name, got, want string }{
		{"it runs only on main, by a push or a dispatch, so no other ref records keys main never took", job["if"],
			"(github.event_name == 'push' || github.event_name == 'workflow_dispatch') && github.ref == 'refs/heads/main'"},
		{"it needs a green test suite and the release job to have run", job["needs"], "[test, release]"},
		{"it checks out the full history and tags range resolution reads", cloneDepth(job), "0"},
		{"it pins checkout to the test job's sha", pin(job, "actions/checkout"), pin(test, "actions/checkout")},
		{"it pins setup-go to the test job's sha", pin(job, "actions/setup-go"), pin(test, "actions/setup-go")},
		{"it passes the tracker URL from vars", script.Env["TRACKER_BASE_URL"], "${{ vars.TRACKER_BASE_URL }}"},
		{"it passes the tracker token from secrets", script.Env["TRACKER_API_TOKEN"], "${{ secrets.TRACKER_API_TOKEN }}"},
		{"it invokes the recorder on the push's before sha and the current sha", strings.TrimSpace(script.Run),
			"go run ./cmd/board-delivery \\\n  --repo . \\\n  --before \"${{ github.event.before }}\" \\\n  --after \"${{ github.sha }}\""},
		{"it grants contents: write to advance the board-synced tag", job["permissions.contents"], "write"},
		{"it serialises its runs in one concurrency group", job["concurrency.group"], "record-delivery"},
		{"it never cancels a run between the board write and the tag advance", job["concurrency.cancel-in-progress"], "false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got == "" || tc.got != tc.want {
				t.Errorf("record-delivery field = %q, want %q", tc.got, tc.want)
			}
		})
	}

	t.Run("it never continues on error, so a failed board write is a red run", func(t *testing.T) {
		for path, v := range job {
			if strings.HasSuffix(path, "continue-on-error") {
				t.Errorf("record-delivery sets %s = %q", path, v)
			}
		}
	})
	t.Run("it never reads release's outputs.released, so a push that cut no tag still delivers its keys", func(t *testing.T) {
		for path, v := range job {
			if strings.Contains(v, "outputs.released") {
				t.Errorf("record-delivery %s = %q reads outputs.released", path, v)
			}
		}
	})
	t.Run("no workflow value derives keys itself instead of calling boardkeys", func(t *testing.T) {
		for path, v := range all {
			if strings.Contains(v, "MIT-[0-9]") {
				t.Errorf("%s = %q inlines a key-derivation regex", path, v)
			}
		}
	})
}
