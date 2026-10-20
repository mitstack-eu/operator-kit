package main

// This guard is a Go test because nothing runs the kit's shell guards, and it reads the workflow by field because the kit is stdlib-only.

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

const workflowPath = "../.github/workflows/publish-block.yml"

// workflowFields returns every key in workflow under its dotted path, such as jobs.publish.steps.3.run, with a block scalar's lines joined and a mapping key's value empty.
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

func readWorkflow(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("reading %s: %v", workflowPath, err)
	}
	return raw
}

func allFields(t *testing.T) map[string]string {
	t.Helper()
	return workflowFields(string(readWorkflow(t)))
}

// step is one step's fields, keyed relative to the step.
type step map[string]string

func jobSteps(t *testing.T, all map[string]string, job string) []step {
	t.Helper()
	var steps []step
	for i := 0; ; i++ {
		prefix := "jobs." + job + ".steps." + strconv.Itoa(i)
		if _, ok := all[prefix]; !ok {
			break
		}
		s := step{}
		for k, v := range all {
			if rest, ok := strings.CutPrefix(k, prefix+"."); ok {
				s[rest] = v
			}
		}
		steps = append(steps, s)
	}
	if len(steps) == 0 {
		t.Fatalf("job %q has no steps in %s", job, workflowPath)
	}
	return steps
}

// label names a step by its name, or by its action without the pinned ref when it has none.
func (s step) label() string {
	if n, ok := s["name"]; ok {
		return n
	}
	action, _, _ := strings.Cut(s["uses"], "@")
	return action
}

func labels(steps []step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.label()
	}
	return out
}

func indexOf(steps []step, label string) int {
	return slices.Index(labels(steps), label)
}

func indexOfID(steps []step, id string) int {
	return slices.IndexFunc(steps, func(s step) bool { return s["id"] == id })
}

// ifOf returns a step's or job's if: expression with any ${{ }} wrapper removed.
func ifOf(expr string) string {
	expr = strings.TrimSpace(expr)
	expr = strings.TrimSuffix(strings.TrimPrefix(expr, "${{"), "}}")
	return strings.TrimSpace(expr)
}

func publishSteps(t *testing.T) []step {
	t.Helper()
	return jobSteps(t, allFields(t), "publish")
}

// script returns the run body of job's step named name, as the runner hands it to bash.
func script(t *testing.T, job, name string) kittest.WorkflowScript {
	t.Helper()
	s, err := kittest.ReadWorkflowScript(readWorkflow(t), job, name)
	if err != nil {
		t.Fatalf("reading the %q step of %s: %v", name, job, err)
	}
	return s
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

type scriptResult struct {
	stdout, stderr, combined string
	exitCode                 int
}

// lockedBuilder serialises writes, since exec copies stdout and stderr from two goroutines into the one combined buffer.
type lockedBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuilder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuilder) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// runBash runs the script at scriptPath in dir under the runner's bash flags, with env added to the process environment.
func runBash(t *testing.T, scriptPath, dir string, env ...string) scriptResult {
	t.Helper()
	cmd := exec.Command("bash", "--noprofile", "--norc", "-e", "-o", "pipefail", scriptPath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr strings.Builder
	combined := &lockedBuilder{}
	cmd.Stdout = io.MultiWriter(&stdout, combined)
	cmd.Stderr = io.MultiWriter(&stderr, combined)
	exitCode := 0
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running %s: %v", scriptPath, err)
		}
		exitCode = exitErr.ExitCode()
	}
	return scriptResult{stdout: stdout.String(), stderr: stderr.String(), combined: combined.String(), exitCode: exitCode}
}

// keyValues lets a later line for the same key win, as it does on the runner.
func keyValues(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = v
		}
	}
	return out
}

var (
	testDigest       = "sha256:" + strings.Repeat("ab", 32)
	testRecordDigest = "sha256:" + strings.Repeat("cd", 32) // differs from the image's digest so a test can tell the two apart
	testRecordTag    = "sha256-" + strings.Repeat("ab", 32) + ".prov"
	testSha          = strings.Repeat("e", 40)
)

func TestWorkflowFields_ReadsNestedKeysAndSequenceItemsByIndentation(t *testing.T) {
	sample := "jobs:\n  conformance:\n    needs: []\n    steps:\n      - run: go vet ./...\n  publish:\n    needs: conformance # a comment\n    steps:\n      - name: push\n        run: |\n          docker push\n          echo done\n"

	got := workflowFields(sample)

	want := map[string]string{
		"jobs": "", "jobs.conformance": "", "jobs.conformance.needs": "[]", "jobs.conformance.steps": "",
		"jobs.conformance.steps.0": "", "jobs.conformance.steps.0.run": "go vet ./...",
		"jobs.publish": "", "jobs.publish.needs": "conformance", "jobs.publish.steps": "",
		"jobs.publish.steps.0": "", "jobs.publish.steps.0.name": "push", "jobs.publish.steps.0.run": "docker push\necho done",
	}
	if len(got) != len(want) {
		t.Errorf("workflowFields = %q, want %q", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("workflowFields[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestWorkflow_Structure(t *testing.T) {
	all := allFields(t)
	conformance, publish := jobSteps(t, all, "conformance"), jobSteps(t, all, "publish")
	field := func(path string) string {
		if v, ok := all[path]; ok {
			return v
		}
		return "<absent>"
	}
	of := func(steps []step, label, key string) string {
		if i := indexOf(steps, label); i >= 0 {
			if v, ok := steps[i][key]; ok {
				return v
			}
		}
		return "<absent>"
	}
	cases := []struct{ name, got, want string }{
		{"it is a reusable workflow", field("on.workflow_call"), ""},
		{"it has a conformance job", field("jobs.conformance"), ""},
		{"it has a publish job", field("jobs.publish"), ""},
		{"publish needs conformance, so a red conformance run never starts it", field("jobs.publish.needs"), "conformance"},
		{"publish runs only on a push to the default branch, so a pull request gets conformance alone", ifOf(field("jobs.publish.if")),
			"github.event_name == 'push' && github.ref == format('refs/heads/{0}', github.event.repository.default_branch)"},
		{"conformance runs on every event", field("jobs.conformance.if"), "<absent>"},
		{"publish grants the id-token the record's OIDC token is minted with", field("jobs.publish.permissions.id-token"), "write"},
		{"conformance runs go vet", of(conformance, "go vet", "run"), "go vet ./..."},
		{"conformance runs go test", of(conformance, "go test", "run"), "go test ./..."},
		{"the base manifest is fetched only on a pull request", ifOf(of(conformance, "Write the base commit's manifest", "if")), "github.event_name == 'pull_request'"},
		{"the base manifest is fetched at the pull request's base sha", of(conformance, "Write the base commit's manifest", "env.BASE_SHA"), "${{ github.event.pull_request.base.sha }}"},
		{"the report is uploaded as an artifact", strings.SplitN(of(conformance, "Upload blockverify report", "uses"), "@", 2)[0], "actions/upload-artifact"},
		{"the report is uploaded whatever the verdict, since a failing run's report must not be lost", ifOf(of(conformance, "Upload blockverify report", "if")), "always()"},
		{"the version probe runs before the image is built", strconv.FormatBool(indexOfID(publish, "existing") >= 0 && indexOfID(publish, "existing") < indexOf(publish, "Build and push image")), "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("publish-block.yml field = %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// A check below matches a command inside a parsed run body, never the YAML around it.
func TestWorkflow_EachJobRunsItsCommands(t *testing.T) {
	cases := []struct {
		name, job, label string
		commands         []string
	}{
		{"conformance runs blockverify standalone with its JSON report", "conformance", "blockverify (standalone contract check)", []string{"./blockverify --standalone --json"}},
		{"conformance hands blockverify the base manifest on a pull request", "conformance", "blockverify (standalone contract check)", []string{`if [ "$EVENT_NAME" = "pull_request" ]`, "--base-manifest"}},
		{"conformance fetches the base commit a shallow clone does not hold", "conformance", "Write the base commit's manifest", []string{`git fetch --depth=1 origin "$BASE_SHA"`}},
		{"publish pushes the image", "publish", "Build and push image", []string{"docker push"}},
		{"publish builds the record", "publish", "blockprov build", []string{"./blockprov build"}},
		{"publish pushes the record", "publish", "blockprov push", []string{"./blockprov push"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := script(t, tc.job, tc.label).Run

			for _, c := range tc.commands {
				if !strings.Contains(run, c) {
					t.Errorf("%s %q run =\n%s\nwant it to run %q", tc.job, tc.label, run, c)
				}
			}
		})
	}
}

func TestWorkflow_ConformanceNeverPushesOrWritesARecord(t *testing.T) {
	all := allFields(t)

	for path, v := range all {
		if !strings.HasPrefix(path, "jobs.conformance.") {
			continue
		}
		for _, forbidden := range []string{"docker push", "blockprov push", "blockprov build"} {
			if strings.Contains(v, forbidden) {
				t.Errorf("%s = %q runs %q; a red run must produce no artefact beyond its report", path, v, forbidden)
			}
		}
	}
}

func TestWorkflow_EveryUsesIsPinnedToACommitSha(t *testing.T) {
	sha := regexp.MustCompile(`^[0-9a-f]{40}$`)
	n := 0

	for path, ref := range allFields(t) {
		if !strings.HasSuffix(path, ".uses") {
			continue
		}
		n++
		if _, pin, ok := strings.Cut(ref, "@"); !ok || !sha.MatchString(pin) {
			t.Errorf("%s = %q, want an action pinned to a 40-character commit sha, not a tag", path, ref)
		}
	}

	if n == 0 {
		t.Fatal("the workflow has no uses: at all, so nothing was checked")
	}
}

func TestWorkflow_HeaderStatesThePublishEventAndWhatARerunCompletes(t *testing.T) {
	var header []string
	for _, line := range strings.Split(string(readWorkflow(t)), "\n") {
		if strings.HasPrefix(line, "name:") {
			break
		}
		header = append(header, line)
	}

	text := strings.Join(strings.Fields(strings.ReplaceAll(strings.ToLower(strings.Join(header, " ")), "#", " ")), " ")

	for _, want := range []string{"push", "default branch", "rerun", "provenance record"} {
		if !strings.Contains(text, want) {
			t.Errorf("the header comment must state the publish event and what a rerun completes; it lacks %q", want)
		}
	}
}

// imageSteps push the image; recordSteps build and push the provenance record; writeSteps write to the registry or mint a signed record.
var (
	imageSteps  = []string{"Install Trivy", "Build and push image"}
	recordSteps = []string{
		"Determine the kit version pinned in go.mod",
		"blockverify (standalone, for blockprov build's report)",
		"blockprov build",
		"Fetch ECR password",
		"blockprov push",
	}
	writeSteps = []string{"Build and push image", "blockprov build", "Fetch ECR password", "blockprov push"}
)

func TestWorkflow_WriteStepsGateOnTheProbe(t *testing.T) {
	steps := publishSteps(t)
	for _, tc := range []struct {
		names  []string
		wantIf string
	}{
		{imageSteps, "steps.existing.outputs.published == 'false'"},
		{recordSteps, "steps.existing.outputs.record == 'false'"},
	} {
		for _, name := range tc.names {
			i := indexOf(steps, name)

			if i < 0 || ifOf(steps[i]["if"]) != tc.wantIf {
				t.Errorf("step %q (index %d) is not gated on %q", name, i, tc.wantIf)
			}
		}
	}
	// A skipped probe answers '', so a != gate against it would run its step.
	for _, s := range steps {
		if expr := ifOf(s["if"]); strings.Contains(expr, "steps.existing.outputs") && strings.Contains(expr, "!=") {
			t.Errorf("step %q is gated on %q; gate with == 'false' instead", s.label(), expr)
		}
	}
}

type probeResult struct {
	scriptResult
	outputs, env map[string]string
	argv         []string
}

// probedTags returns each imageTag the probe asked aws about, in order.
func (r probeResult) probedTags() []string {
	var tags []string
	for _, line := range r.argv {
		for _, f := range strings.Fields(line) {
			if tag, ok := strings.CutPrefix(f, "imageTag="); ok {
				tags = append(tags, tag)
			}
		}
	}
	return tags
}

// runProbe runs the version probe against a stub aws that answers per tag: imageMode for the version tag, recordMode for the record tag and shaMode for the commit-sha tag, each "exists", "absent" or "fail", and shaMode also "match" (the image's digest) or "other".
func runProbe(t *testing.T, version, imageMode, recordMode, shaMode string) probeResult {
	t.Helper()
	steps := publishSteps(t)
	i := indexOfID(steps, "existing")
	if i < 0 {
		t.Fatal("publish job has no step with id: existing, the tag-exists probe")
	}
	tmp := t.TempDir()
	body := strings.ReplaceAll(script(t, "publish", steps[i]["name"]).Run, "${{ inputs.ecr-repository }}", "mitstack-postgres-db")
	writeExecutable(t, filepath.Join(tmp, "probe.sh"), body)
	work, stub := filepath.Join(tmp, "work"), filepath.Join(tmp, "stub")
	for _, d := range []string{work, stub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable(t, filepath.Join(work, "manifest.yaml"), "spec:\n  version: "+version+"\n")
	writeExecutable(t, filepath.Join(stub, "aws"), "#!/bin/sh\n"+
		"echo \"$@\" >> \"$AWS_ARGV_FILE\"\n"+
		"mode=\"\"\n"+
		"for a in \"$@\"; do\n"+
		"  case \"$a\" in\n"+
		"    imageTag=v*) mode=\"$AWS_IMAGE_MODE\" ;;\n"+
		"    imageTag=sha256-*.prov) mode=\"$AWS_RECORD_MODE\"; digest=\"$AWS_RECORD_DIGEST\" ;;\n"+
		"    imageTag=$GITHUB_SHA) mode=\"$AWS_SHA_MODE\" ;;\n"+
		"  esac\n"+
		"done\n"+
		"if [ \"$mode\" = other ]; then mode=exists; digest=\"$AWS_RECORD_DIGEST\"; fi\n"+
		"if [ \"$mode\" = match ]; then mode=exists; fi\n"+
		"[ -n \"$digest\" ] || digest=\"$AWS_DIGEST\"\n"+
		"case \"$mode\" in\n"+
		"  exists) echo \"$digest\"; exit 0 ;;\n"+
		"  absent) echo \"None\"; exit 0 ;;\n"+
		"  fail) echo \"boom: access denied\" >&2; exit 254 ;;\n"+
		"  *) echo \"stub aws: no imageTag it recognises in: $*\" >&2; exit 99 ;;\n"+
		"esac\n")
	output, env, argv := filepath.Join(tmp, "github-output"), filepath.Join(tmp, "github-env"), filepath.Join(tmp, "aws-argv")
	for _, f := range []string{output, env, argv} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	res := runBash(t, filepath.Join(tmp, "probe.sh"), work,
		"PATH="+stub+":"+os.Getenv("PATH"), "GITHUB_OUTPUT="+output, "GITHUB_ENV="+env, "ECR_REPOSITORY=mitstack-postgres-db",
		"AWS_IMAGE_MODE="+imageMode, "AWS_RECORD_MODE="+recordMode, "AWS_SHA_MODE="+shaMode, "GITHUB_SHA="+testSha,
		"AWS_DIGEST="+testDigest, "AWS_RECORD_DIGEST="+testRecordDigest, "AWS_ARGV_FILE="+argv)

	read := func(f string) string { b, _ := os.ReadFile(f); return string(b) }
	return probeResult{res, keyValues(read(output)), keyValues(read(env)), strings.Split(strings.TrimSpace(read(argv)), "\n")}
}

func TestWorkflow_TheProbe(t *testing.T) {
	cases := []struct {
		name                        string
		version, image, record, sha string
		wantExit                    bool
		wantOutputs                 map[string]string
		wantImageDigestEnv          string
		wantTags                    []string
		wantStdout, wantInFailure   string
	}{
		{"a published tag with no record, built from this commit, is completed for its digest", "1.2.0", "exists", "absent", "match", true,
			map[string]string{"published": "true", "digest": testDigest, "record": "false"}, testDigest, []string{"v1.2.0", testRecordTag, testSha},
			"already published: v1.2.0 " + testDigest + "\nno provenance record at " + testRecordTag + "; completing it for " + testDigest + "\n", ""},
		{"a published tag with its record present writes nothing", "1.2.0", "exists", "exists", "match", true,
			map[string]string{"published": "true", "digest": testDigest, "record": "true"}, testDigest, []string{"v1.2.0", testRecordTag},
			"already published: v1.2.0 " + testDigest + "\nprovenance record present at " + testRecordTag + "\n", ""},
		{"a tag with its record needs no commit check", "1.2.0", "exists", "exists", "fail", true,
			map[string]string{"published": "true", "digest": testDigest, "record": "true"}, testDigest, []string{"v1.2.0", testRecordTag},
			"already published: v1.2.0 " + testDigest + "\nprovenance record present at " + testRecordTag + "\n", ""},
		{"an unpublished tag publishes and is never claimed as already published", "1.2.0", "absent", "absent", "match", true,
			map[string]string{"published": "false", "record": "false"}, "", []string{"v1.2.0"}, "", ""},
		{"an unpublished tag's record is never probed", "1.2.0", "absent", "fail", "match", true,
			map[string]string{"published": "false", "record": "false"}, "", []string{"v1.2.0"}, "", ""},
		{"a version bump probes and publishes its own tag", "1.3.0", "absent", "absent", "match", true,
			map[string]string{"published": "false", "record": "false"}, "", []string{"v1.3.0"}, "", ""},
		{"a version probe that cannot answer fails closed, naming the tag", "1.2.0", "fail", "absent", "match", false,
			map[string]string{}, "", []string{"v1.2.0"}, "", "v1.2.0"},
		{"a record probe that cannot answer fails closed, naming the record tag", "1.2.0", "exists", "fail", "match", false,
			map[string]string{"published": "true", "digest": testDigest}, testDigest, []string{"v1.2.0", testRecordTag}, "", testRecordTag},
		{"a published image another commit built is never given this commit's record", "1.2.0", "exists", "absent", "other", false,
			map[string]string{"published": "true", "digest": testDigest}, testDigest, []string{"v1.2.0", testRecordTag, testSha}, "", testSha},
		{"a published image with no sha tag is never given this commit's record", "1.2.0", "exists", "absent", "absent", false,
			map[string]string{"published": "true", "digest": testDigest}, testDigest, []string{"v1.2.0", testRecordTag, testSha}, "", testSha},
		{"a commit check that cannot answer gives no record answer", "1.2.0", "exists", "absent", "fail", false,
			map[string]string{"published": "true", "digest": testDigest}, testDigest, []string{"v1.2.0", testRecordTag, testSha}, "", testSha},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runProbe(t, tc.version, tc.image, tc.record, tc.sha)

			if (res.exitCode == 0) != tc.wantExit {
				t.Fatalf("exit code = %d, want success %v; output:\n%s", res.exitCode, tc.wantExit, res.combined)
			}
			if len(res.outputs) != len(tc.wantOutputs) || !slices.Equal(sortedPairs(res.outputs), sortedPairs(tc.wantOutputs)) {
				t.Errorf("$GITHUB_OUTPUT = %v, want %v", res.outputs, tc.wantOutputs)
			}
			if got := res.env["IMAGE_DIGEST"]; got != tc.wantImageDigestEnv {
				t.Errorf("$GITHUB_ENV IMAGE_DIGEST = %q, want %q", got, tc.wantImageDigestEnv)
			}
			if got := res.probedTags(); !slices.Equal(got, tc.wantTags) {
				t.Errorf("probed tags %q, want %q", got, tc.wantTags)
			}
			if tc.wantExit && res.stdout != tc.wantStdout {
				t.Errorf("stdout = %q, want %q", res.stdout, tc.wantStdout)
			}
			if !tc.wantExit && !strings.Contains(res.combined, tc.wantInFailure) {
				t.Errorf("output = %q, want it to name %s, the answer it could not confirm", res.combined, tc.wantInFailure)
			}
		})
	}
}

func TestWorkflow_TheProbeAsksAWSForTheRepositorysTag(t *testing.T) {
	res := runProbe(t, "1.2.0", "exists", "absent", "match")

	if want := "ecr batch-get-image --repository-name mitstack-postgres-db --image-ids imageTag=v1.2.0 --query images[0].imageId.imageDigest --output text"; res.argv[0] != want {
		t.Errorf("first aws argv = %q, want %q", res.argv[0], want)
	}
}

func sortedPairs(m map[string]string) []string {
	var out []string
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	slices.Sort(out)
	return out
}

// stepRuns fails the test on any if: form it does not know, so a new gate cannot pass unexamined.
func stepRuns(t *testing.T, s step, outputs map[string]string) bool {
	t.Helper()
	switch expr := ifOf(s["if"]); expr {
	case "", "always()":
		return true
	default:
		m := regexp.MustCompile(`^steps\.existing\.outputs\.(\w+) == '([^']*)'$`).FindStringSubmatch(expr)
		if m == nil {
			t.Fatalf("step if: %q is not a form this evaluator knows", expr)
		}
		return outputs[m[1]] == m[2]
	}
}

func TestWorkflow_TheProbeAnswerDecidesWhichWriteStepsRun(t *testing.T) {
	cases := []struct {
		name          string
		probe         []string
		wantRun, skip []string
	}{
		{"a rerun completes the record for a published tag without pushing the image", []string{"1.2.0", "exists", "absent", "match"}, recordSteps, imageSteps},
		{"a tag with its record writes nothing", []string{"1.2.0", "exists", "exists", "match"}, nil, writeSteps},
		{"an unpublished version runs the image and record steps", []string{"1.3.0", "absent", "absent", "match"}, append(slices.Clone(imageSteps), recordSteps...), nil},
		{"a probe that was itself skipped answers nothing, so no write step runs", nil, nil, writeSteps},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outputs := map[string]string{}
			if tc.probe != nil {
				res := runProbe(t, tc.probe[0], tc.probe[1], tc.probe[2], tc.probe[3])
				if res.exitCode != 0 {
					t.Fatalf("probe exit code = %d; output:\n%s", res.exitCode, res.combined)
				}
				outputs = res.outputs
			}
			steps := publishSteps(t)

			for _, want := range []struct {
				names []string
				runs  bool
			}{{tc.wantRun, true}, {tc.skip, false}} {
				for _, name := range want.names {
					i := indexOf(steps, name)
					if i < 0 {
						t.Fatalf("publish job has no step named %q", name)
					}
					if got := stepRuns(t, steps[i], outputs); got != want.runs {
						t.Errorf("step %q runs = %v under %v, want %v", name, got, outputs, want.runs)
					}
				}
			}
		})
	}
}

// runStepBody runs a publish step's script with its ${{ }} inputs filled in and a stub blockprov that records its argv.
func runStepBody(t *testing.T, name string, env map[string]string) string {
	t.Helper()
	literals := map[string]string{
		"inputs.block-id":       "postgres-db",
		"inputs.ecr-repository": "mitstack-postgres-db",
		"inputs.aws-region":     "eu-central-1",
		"github.repository":     "mitstack-eu/postgres-db",
		"github.server_url":     "https://github.com",
		"github.run_id":         "42",
	}
	body := regexp.MustCompile(`\$\{\{\s*([^}]+?)\s*\}\}`).ReplaceAllStringFunc(script(t, "publish", name).Run, func(s string) string {
		key := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "${{"), "}}"))
		v, ok := literals[key]
		if !ok {
			t.Fatalf("step %q uses ${{ %s }}, which this test has no literal for", name, key)
		}
		return v
	})
	tmp := t.TempDir()
	argvFile := filepath.Join(tmp, "blockprov-argv")
	writeExecutable(t, filepath.Join(tmp, "blockprov"), "#!/bin/sh\necho \"$@\" >> \""+argvFile+"\"\n")
	writeExecutable(t, filepath.Join(tmp, "manifest.yaml"), "spec:\n  version: 1.2.0\n")
	writeExecutable(t, filepath.Join(tmp, "step.sh"), body)
	var vars []string
	for k, v := range env {
		vars = append(vars, k+"="+v)
	}

	if res := runBash(t, filepath.Join(tmp, "step.sh"), tmp, vars...); res.exitCode != 0 {
		t.Fatalf("step %q failed: exit %d\n%s", name, res.exitCode, res.combined)
	}

	argv, _ := os.ReadFile(argvFile)
	return string(argv)
}

func TestWorkflow_ARerunBuildsAndPushesTheRecordForThePublishedDigest(t *testing.T) {
	res := runProbe(t, "1.2.0", "exists", "absent", "match")
	if res.exitCode != 0 {
		t.Fatalf("probe exit code = %d; output:\n%s", res.exitCode, res.combined)
	}
	env := res.env
	for k, v := range map[string]string{
		"REGISTRY": "123456789012.dkr.ecr.eu-central-1.amazonaws.com", "KIT_VERSION": "v0.9.0", "ECR_PASSWORD": "stub-password",
		"GITHUB_SHA": strings.Repeat("f", 40), "ACTIONS_ID_TOKEN_REQUEST_URL": "https://token.example", "ACTIONS_ID_TOKEN_REQUEST_TOKEN": "stub-token",
	} {
		env[k] = v
	}

	build, push := runStepBody(t, "blockprov build", env), runStepBody(t, "blockprov push", env)

	for _, want := range []struct{ argv, flag string }{
		{build, "--image-digest " + testDigest},
		{build, "--image-ref " + env["REGISTRY"] + "/mitstack-postgres-db@" + testDigest},
		{push, "--image-digest " + testDigest},
	} {
		if !strings.Contains(want.argv, want.flag) {
			t.Errorf("blockprov argv = %q, want %q", want.argv, want.flag)
		}
	}
}

// Trivy reads these files from its working directory with no flag, so one anywhere in the tree fails the job straight after the checkout.
func TestWorkflow_ATrivyIgnoreOrConfigFileFailsThePublishJob(t *testing.T) {
	const name = "Refuse a committed Trivy ignore or config file"
	steps := publishSteps(t)
	if i := indexOf(steps, name); i != 1 || steps[i]["if"] != "" {
		t.Fatalf("the refusal is publish step %d, want an unconditional step 1", i)
	}
	refuse := filepath.Join(t.TempDir(), "refuse.sh")
	writeExecutable(t, refuse, script(t, "publish", name).Run)

	for _, file := range []string{"", ".trivyignore", ".trivyignore.yaml", "trivy.yaml", "deploy/trivy-secret.yaml"} {
		tree := t.TempDir()
		_ = os.MkdirAll(filepath.Join(tree, "deploy"), 0o755)
		_ = os.WriteFile(filepath.Join(tree, file), nil, 0o644)

		res := runBash(t, refuse, tree)

		if (res.exitCode != 0) != (file != "") || !strings.Contains(res.stderr, file) {
			t.Errorf("with %q in the tree: exit %d, stderr %q", file, res.exitCode, res.stderr)
		}
	}
}

// A finding fails the build step after the build and before any push, and no file, variable or flag narrows the scan.
func TestWorkflow_AFindingStopsThePushOfTheImageItScanned(t *testing.T) {
	if install := script(t, "publish", "Install Trivy").Run; !strings.Contains(install, "1f889576636db0623539cb339df7d06c40ebf39e7919489eafa1160e29808f19  $RUNNER_TEMP/trivy\" | sha256sum -c -") {
		t.Errorf("Install Trivy run =\n%s\nwant it to check the binary against its pinned sha256", install)
	}
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "docker"), "#!/bin/sh\necho docker $*\n")
	writeExecutable(t, filepath.Join(dir, "trivy"), "#!/bin/sh\necho \"trivy $* cwd=[$(ls -A)] env=[$(env | grep ^TRIVY_)]\"\nexit 1\n")
	writeExecutable(t, filepath.Join(dir, "manifest.yaml"), "spec:\n  version: 1.2.0\n")
	writeExecutable(t, filepath.Join(dir, "build.sh"), strings.ReplaceAll(script(t, "publish", "Build and push image").Run, "${{ inputs.ecr-repository }}", "repo"))

	res := runBash(t, filepath.Join(dir, "build.sh"), dir, "PATH="+dir+":"+os.Getenv("PATH"), "RUNNER_TEMP="+dir, "REGISTRY=r", "GITHUB_SHA=c0ffee", "TRIVY_SEVERITY=CRITICAL")

	lines := strings.Split(strings.TrimSuffix(res.stdout, "\n"), "\n")
	if res.exitCode == 0 || len(lines) != 2 || lines[0] != "docker build -t r/repo:v1.2.0 -t r/repo:c0ffee ." ||
		!strings.HasPrefix(lines[1], "trivy image --image-src docker --exit-code 1 --ignore-unfixed ") ||
		!strings.HasSuffix(lines[1], " r/repo:c0ffee cwd=[] env=[]") || strings.Contains(lines[1], "--severity") {
		t.Errorf("exit %d, output =\n%s\nwant the build, then one scan of its sha tag with no --severity that fails the step before any push", res.exitCode, res.combined)
	}
}

// The kit is a public module, so each job fetches it with no credential: no step mints a token, and the kit checkout uses the run's default token.
func TestWorkflow_JobsFetchThePublicKitWithNoCredential(t *testing.T) {
	all := allFields(t)
	for _, tc := range []struct {
		job     string
		idToken string
		want    []string
	}{
		{"conformance", "<absent>", []string{
			"actions/checkout", "actions/setup-go", "go vet", "go test", "Checkout operator-kit", "Build blockverify", "Build commentcheck",
			"Remove the kit checkout from the block tree", "Write the base commit's manifest", "Judge the comments this pull request adds",
			"blockverify (standalone contract check)", "Upload blockverify report",
		}},
		{"publish", "write", []string{
			"actions/checkout", "Refuse a committed Trivy ignore or config file", "actions/setup-go", "Configure AWS credentials", "Checkout operator-kit",
			"Build blockverify and blockprov", "Remove the kit checkout from the block tree", "Log in to ECR", "Probe the version tag and its provenance record",
			"Install Trivy", "Build and push image", "Determine the kit version pinned in go.mod", "blockverify (standalone, for blockprov build's report)",
			"blockprov build", "Fetch ECR password", "blockprov push",
		}},
	} {
		t.Run(tc.job, func(t *testing.T) {
			steps := jobSteps(t, all, tc.job)
			idToken, ok := all["jobs."+tc.job+".permissions.id-token"]
			if !ok {
				idToken = "<absent>"
			}

			checkout := indexOf(steps, "Checkout operator-kit")

			if got := labels(steps); !slices.Equal(got, tc.want) {
				t.Errorf("%s steps =\n  %s\nwant\n  %s", tc.job, strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
			if checkout < 0 {
				t.Fatalf("%s job has no step %q", tc.job, "Checkout operator-kit")
			}
			if token, ok := steps[checkout]["with.token"]; ok {
				t.Errorf("%s kit checkout passes token %q, want none, so the public kit is read with the run's default token", tc.job, token)
			}
			if idToken != tc.idToken {
				t.Errorf("%s id-token permission = %q, want %q, since only the publish job assumes an AWS role and mints the record's OIDC token", tc.job, idToken, tc.idToken)
			}
			if i := indexOf(steps, "Build and push image"); i >= 0 && strings.Contains(steps[i]["run"], "--secret") {
				t.Errorf("%s image build run =\n%s\nwant no --secret, so the image fetches the public kit with no credential", tc.job, steps[i]["run"])
			}
		})
	}
}

// The kit checkout must be gone before blockverify or the image build reads the tree, or the block is judged by the kit's own source.
func TestWorkflow_TheKitCheckoutLeavesTheBlockTreeBeforeItIsJudged(t *testing.T) {
	const name = "Remove the kit checkout from the block tree"
	all := allFields(t)
	for _, tc := range []struct{ job, build, firstReader string }{
		{"conformance", "Build blockverify", "blockverify (standalone contract check)"},
		{"publish", "Build blockverify and blockprov", "Build and push image"},
	} {
		t.Run(tc.job, func(t *testing.T) {
			steps := jobSteps(t, all, tc.job)
			rm, b, r := indexOf(steps, name), indexOf(steps, tc.build), indexOf(steps, tc.firstReader)
			if rm < 0 {
				t.Fatalf("%s job has no step %q", tc.job, name)
			}
			tree := t.TempDir()
			if err := os.MkdirAll(filepath.Join(tree, "operator-kit", "events"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeExecutable(t, filepath.Join(tree, "operator-kit", "events", "events.go"), "package events\n")
			writeExecutable(t, filepath.Join(tree, "main.go"), "package main\n")
			rmScript := filepath.Join(t.TempDir(), "rm.sh")
			writeExecutable(t, rmScript, script(t, tc.job, name).Run)

			res := runBash(t, rmScript, tree)

			if b < 0 || rm < b || r < 0 || rm > r {
				t.Errorf("%q is at %d, want it after %q at %d and before %q at %d", name, rm, tc.build, b, tc.firstReader, r)
			}
			if got := ifOf(steps[rm]["if"]); got != "" {
				t.Errorf("%q has if: %q, want none", name, got)
			}
			if res.exitCode != 0 {
				t.Fatalf("step failed: exit %d\n%s", res.exitCode, res.combined)
			}
			if _, err := os.Stat(filepath.Join(tree, "operator-kit")); !os.IsNotExist(err) {
				t.Errorf("operator-kit/ still exists after the step (stat err %v)", err)
			}
			if _, err := os.Stat(filepath.Join(tree, "main.go")); err != nil {
				t.Errorf("the block's own main.go is gone: %v", err)
			}
		})
	}
}

func TestWorkflow_KitVersionStepReadsTheKitPinFromGoMod(t *testing.T) {
	const name = "Determine the kit version pinned in go.mod"
	for _, tc := range []struct {
		name, goMod, want string
	}{
		{"a block on kit v2 requires the major-version path",
			"module github.com/mitstack-eu/kube-app-deploy\n\ngo 1.26\n\nrequire github.com/mitstack-eu/operator-kit/v2 v2.2.0\n", "v2.2.0"},
		{"a block on kit v2 lists it inside a require block",
			"module github.com/mitstack-eu/kube-app-deploy\n\ngo 1.26\n\nrequire (\n\tgithub.com/google/uuid v1.6.0\n\tgithub.com/mitstack-eu/operator-kit/v2 v2.2.0\n)\n", "v2.2.0"},
		{"a block on kit v1 requires the root module path",
			"module github.com/mitstack-eu/postgres-db\n\ngo 1.26\n\nrequire github.com/mitstack-eu/operator-kit v1.32.0\n", "v1.32.0"},
		{"a block still on the legacy monorepo path",
			"module github.com/mitstack-eu/redis-cache\n\ngo 1.26\n\nrequire github.com/mitstack-eu/mitstack/examples/operator-kit v0.5.1\n", "v0.5.1"},
		{"a go.mod requiring no operator-kit module fails",
			"module github.com/mitstack-eu/stray\n\ngo 1.26\n\nrequire github.com/google/uuid v1.6.0\n", ""},
		{"a go.mod requiring only a sibling module whose path starts with operator-kit fails",
			"module github.com/mitstack-eu/stray\n\ngo 1.26\n\nrequire github.com/mitstack-eu/operator-kit-extras v1.0.0\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := script(t, "publish", name).Run
			if strings.TrimSpace(body) == "" || strings.Contains(body, "${{") {
				t.Fatalf("step %q has no plain run: script to execute:\n%s", name, body)
			}
			tmp := t.TempDir()
			block, envFile := filepath.Join(tmp, "block"), filepath.Join(tmp, "github-env")
			if err := os.MkdirAll(block, 0o755); err != nil {
				t.Fatal(err)
			}
			writeExecutable(t, filepath.Join(tmp, "step.sh"), body)
			writeExecutable(t, envFile, "")
			writeExecutable(t, filepath.Join(block, "go.mod"), tc.goMod)

			res := runBash(t, filepath.Join(tmp, "step.sh"), block, "GITHUB_ENV="+envFile)

			envBytes, _ := os.ReadFile(envFile)
			got, set := keyValues(string(envBytes))["KIT_VERSION"]
			switch {
			case tc.want != "" && (res.exitCode != 0 || got != tc.want):
				t.Errorf("exit %d, KIT_VERSION = %q; want 0 and %q; output:\n%s", res.exitCode, got, tc.want, res.combined)
			case tc.want == "" && (res.exitCode != 1 || set || !strings.Contains(res.stderr, "no operator-kit version pin found in go.mod")):
				t.Errorf("exit %d, KIT_VERSION set %v, stderr %q; want exit 1, no KIT_VERSION and the step's failure message", res.exitCode, set, res.stderr)
			}
		})
	}
}
