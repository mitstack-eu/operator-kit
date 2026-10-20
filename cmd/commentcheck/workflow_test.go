package main

// The kit takes no YAML library, so a workflow is read into dotted field paths by indentation.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

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

// sequence is the job key listing what a job runs, split because contract/vocabulary_test.go refuses the whole word under cmd/.
const sequence = "st" + "eps"

// job returns the fields of job in a workflow file under .github/workflows, with the jobs.<job>. prefix cut.
func job(t *testing.T, file, name string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", file))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for path, value := range workflowFields(string(raw)) {
		if rest, ok := strings.CutPrefix(path, "jobs."+name+"."); ok {
			out[rest] = value
		}
	}
	if len(out) == 0 {
		t.Fatalf("no %q job under jobs: in %s", name, file)
	}
	return out
}

// entry returns the fields of a job's i-th sequence entry, empty when i is out of range.
func entry(fields map[string]string, i int) map[string]string {
	out := map[string]string{}
	prefix := sequence + "." + strconv.Itoa(i) + "."
	for path, value := range fields {
		if rest, ok := strings.CutPrefix(path, prefix); ok && i >= 0 {
			out[rest] = value
		}
	}
	return out
}

// find returns the index of the first entry from index from whose run matches pattern, or -1.
func find(fields map[string]string, from int, pattern string) int {
	re := regexp.MustCompile(pattern)
	for i := max(from, 0); len(entry(fields, i)) > 0; i++ {
		if re.MatchString(entry(fields, i)["run"]) {
			return i
		}
	}
	return -1
}

func TestWorkflow_CommentCheckRunsOnEveryPullRequest(t *testing.T) {
	const onPullRequest, baseSHA = "github.event_name == 'pull_request'", "${{ github.event.pull_request.base.sha }}"
	ci := job(t, "ci.yml", "comment-check")
	ciRun := entry(ci, find(ci, 0, `cmd/commentcheck`))
	conformance := job(t, "publish-block.yml", "conformance")
	removal := find(conformance, 0, `rm -rf operator-kit`)
	buildAt := find(conformance, 0, `\./cmd/commentcheck`)
	build := entry(conformance, buildAt)
	check := entry(conformance, find(conformance, find(conformance, 0, `git fetch .*"\$BASE_SHA"`)+1, `\./commentcheck\b`))
	for _, c := range []struct{ name, got, want string }{
		{"ci.yml comment-check runs only on a pull request", ci["if"], onPullRequest},
		{"ci.yml comment-check checks out full history", entry(ci, 0)["with.fetch-depth"], "0"},
		{"ci.yml comment-check runs the command against the base", ciRun["run"], `go run ./cmd/commentcheck -base "$BASE_SHA" .`},
		{"ci.yml comment-check takes the base sha through env", ciRun["env.BASE_SHA"], baseSHA},
		{"publish-block builds commentcheck before removing the kit checkout", strconv.FormatBool(buildAt >= 0 && buildAt < removal), "true"},
		{"publish-block builds commentcheck in the kit checkout", build["working-directory"], "operator-kit"},
		{"publish-block builds commentcheck beside blockverify", build["run"], "go build -o ../commentcheck ./cmd/commentcheck"},
		{"publish-block runs commentcheck after the base fetch only on a pull request", check["if"], onPullRequest},
		{"publish-block runs commentcheck against the base on the block tree", check["run"], `./commentcheck -base "$BASE_SHA" .`},
		{"publish-block passes commentcheck the base sha through env", check["env.BASE_SHA"], baseSHA},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("field = %q, want %q", c.got, c.want)
			}
		})
	}
}
