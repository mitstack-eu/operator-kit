// Package scripts_test pins the ci.yml jobs that test the kit and cut its release tag with scripts/release-operator-kit.sh.
package scripts_test

import (
	"os"
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

// stepWith reports whether one of job's steps sets key to value.
func stepWith(all map[string]string, job, key, value string) bool {
	for i := 0; ; i++ {
		prefix := "jobs." + job + ".steps." + strconv.Itoa(i) + "."
		if _, ok := all[strings.TrimSuffix(prefix, ".")]; !ok {
			return false
		}
		if v, ok := all[prefix+key]; ok && v == value {
			return true
		}
	}
}

func TestCIWorkflow_TestsTheKitAndCutsItsReleaseOnlyFromMain(t *testing.T) {
	const checkout, setupGo = "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"
	raw, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("reading ci.yml: %v", err)
	}
	all := workflowFields(string(raw))
	field := func(path string) string {
		v, ok := all[path]
		if !ok {
			return "<absent>"
		}
		return v
	}
	cases := []struct{ name, got, want string }{
		{"it runs on a pull request", field("on.pull_request"), ""},
		{"it runs on a push to main", field("on.push.branches"), "[main]"},
		{"it runs on a dispatch", field("on.workflow_dispatch"), ""},
		{"the test job runs on vars.CI_RUNS_ON with the ubicloud-standard-2 fallback", field("jobs.test.runs-on"), `${{ fromJSON(vars.CI_RUNS_ON || '["ubicloud-standard-2"]') }}`},
		{"the test job pins checkout to the platform's sha", strconv.FormatBool(stepWith(all, "test", "uses", checkout)), "true"},
		{"the test job pins setup-go to the platform's sha", strconv.FormatBool(stepWith(all, "test", "uses", setupGo)), "true"},
		{"the test job pins go-version to 1.26.8", strconv.FormatBool(stepWith(all, "test", "with.go-version", "'1.26.8'")), "true"},
		{"the test job runs go vet ./...", strconv.FormatBool(stepWith(all, "test", "run", "go vet ./...")), "true"},
		{"the test job runs go test ./...", strconv.FormatBool(stepWith(all, "test", "run", "go test ./...")), "true"},
		{"the release job needs the test job", field("jobs.release.needs"), "test"},
		{"the release job runs only on main, by a push or a dispatch, so no unmerged commit is tagged", field("jobs.release.if"),
			"(github.event_name == 'push' || github.event_name == 'workflow_dispatch') && github.ref == 'refs/heads/main'"},
		{"the release job grants contents: write", field("jobs.release.permissions.contents"), "write"},
		{"the release job's concurrency group is kit-release", field("jobs.release.concurrency.group"), "kit-release"},
		{"the release job never cancels a release in progress", field("jobs.release.concurrency.cancel-in-progress"), "false"},
		{"the release job pins checkout to the platform's sha", strconv.FormatBool(stepWith(all, "release", "uses", checkout)), "true"},
		{"the release job checks out the tags the version scan reads", strconv.FormatBool(stepWith(all, "release", "with.fetch-depth", "0")), "true"},
		{"the release job runs its script under bash, so pipefail fails a red release", strconv.FormatBool(stepWith(all, "release", "shell", "bash")), "true"},
		{"the release job runs the release script into the job summary", strconv.FormatBool(stepWith(all, "release", "run", `bash scripts/release-operator-kit.sh | tee -a "$GITHUB_STEP_SUMMARY"`)), "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("ci.yml field = %q, want %q", tc.got, tc.want)
			}
		})
	}
}
