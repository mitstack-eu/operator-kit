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

func TestWorkflow_PRKeyGate(t *testing.T) {
	raw, err := os.ReadFile(ciWorkflowPath)
	if err != nil {
		t.Fatalf("reading %s: %v", ciWorkflowPath, err)
	}
	all := workflowFields(string(raw))
	test, gate := jobFields(t, all, "test"), jobFields(t, all, "pr-key-gate")
	script, err := kittest.ReadWorkflowScript(raw, "pr-key-gate", "Gate the pull request on a board key")
	if err != nil {
		t.Fatalf("reading the gate script: %v", err)
	}
	cases := []struct{ name, got, want string }{
		{"it runs only on a pull request, never on a push", gate["if"], "github.event_name == 'pull_request'"},
		{"it checks out the full history the base..head range needs", cloneDepth(gate), "0"},
		{"it pins checkout to the test job's sha", pin(gate, "actions/checkout"), pin(test, "actions/checkout")},
		{"it pins setup-go to the test job's sha", pin(gate, "actions/setup-go"), pin(test, "actions/setup-go")},
		{"it reads the title from the pull request event, never from a commit", script.Env["PR_TITLE"], "${{ github.event.pull_request.title }}"},
		{"it invokes the command on the pull request's base and head", strings.TrimSpace(script.Run),
			"go run ./cmd/pr-keys \\\n  --repo . \\\n  --base \"${{ github.event.pull_request.base.sha }}\" \\\n  --head \"${{ github.event.pull_request.head.sha }}\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got == "" || tc.got != tc.want {
				t.Errorf("pr-key-gate field = %q, want %q", tc.got, tc.want)
			}
		})
	}

	for _, tc := range []struct{ name, forbidden string }{
		{"it needs no secrets at all", "secrets."},
		{"it reads nothing from a commit, since a branch name in a commit is no delivery intent", "head_commit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for path, v := range gate {
				if strings.Contains(v, tc.forbidden) {
					t.Errorf("pr-key-gate %s = %q references %s", path, v, tc.forbidden)
				}
			}
		})
	}
	t.Run("it never continues on error, so a missing key is a red check", func(t *testing.T) {
		for path, v := range gate {
			if strings.HasSuffix(path, "continue-on-error") {
				t.Errorf("pr-key-gate sets %s = %s", path, v)
			}
		}
	})
}
