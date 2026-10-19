package main

// This guard is a Go test because nothing runs the kit's shell guards, and it parses by indentation because the kit is stdlib-only.

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const workflowPath = "../.github/workflows/publish-block.yml"

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// block composes, so block(block(text, "jobs"), "conformance") reaches a nested key.
func block(text, key string) string {
	lines := strings.Split(text, "\n")
	var out []string
	found := false
	keyIndent := 0
	keyPattern := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `:`)
	for _, line := range lines {
		if !found {
			if keyPattern.MatchString(line) {
				keyIndent = indentOf(line)
				found = true
			}
			continue
		}
		if strings.TrimSpace(line) != "" && indentOf(line) <= keyIndent {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func jobNames(jobsBlock string) []string {
	var names []string
	namePattern := regexp.MustCompile(`^\s\s(\S[^:]*):`)
	for _, line := range strings.Split(jobsBlock, "\n") {
		if m := namePattern.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
		}
	}
	return names
}

func readWorkflow(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("reading %s: %v", workflowPath, err)
	}
	return string(raw)
}

func TestWorkflow_DeclaresWorkflowCall(t *testing.T) {
	text := readWorkflow(t)
	onBlock := block(text, "on")
	if !strings.Contains(onBlock, "workflow_call") && !strings.Contains(text, "on: workflow_call") {
		t.Errorf("%s must declare `on: workflow_call`, got on: block:\n%s", workflowPath, onBlock)
	}
}

func TestWorkflow_HasConformanceAndPublishJobs(t *testing.T) {
	text := readWorkflow(t)
	jobs := block(text, "jobs")
	names := jobNames(jobs)

	hasConformance, hasPublish := false, false
	for _, n := range names {
		if n == "conformance" {
			hasConformance = true
		}
		if n == "publish" {
			hasPublish = true
		}
	}
	if !hasConformance {
		t.Errorf("jobs: must include a %q job, found: %v", "conformance", names)
	}
	if !hasPublish {
		t.Errorf("jobs: must include a %q job, found: %v", "publish", names)
	}
}

func TestWorkflow_PublishNeedsConformance(t *testing.T) {
	text := readWorkflow(t)
	publish := block(block(text, "jobs"), "publish")
	if publish == "" {
		t.Fatal("no publish job block found")
	}
	needsPattern := regexp.MustCompile(`(?m)^\s*needs:\s*(.+)$`)
	m := needsPattern.FindStringSubmatch(publish)
	if m == nil {
		t.Fatal("publish job has no `needs:` key")
	}
	needs := strings.Trim(strings.TrimSpace(m[1]), "[]")
	if !strings.Contains(needs, "conformance") {
		t.Errorf("publish job's needs = %q, want it to name conformance — a red conformance run must never start publish", needs)
	}
}

func TestWorkflow_ConformanceJobRunsVetTestAndBlockverify(t *testing.T) {
	text := readWorkflow(t)
	conformance := block(block(text, "jobs"), "conformance")
	if conformance == "" {
		t.Fatal("no conformance job block found")
	}
	for _, want := range []string{"go vet ./...", "go test ./...", "blockverify"} {
		if !strings.Contains(conformance, want) {
			t.Errorf("conformance job must run %q, job block:\n%s", want, conformance)
		}
	}
	if !strings.Contains(conformance, "--standalone") || !strings.Contains(conformance, "--json") {
		t.Error("the blockverify invocation must run --standalone --json, matching the CLI's only supported mode")
	}
	// On a pull request, blockverify compares spec.version against the base
	// commit's manifest, which a shallow checkout does not hold.
	for _, want := range []string{"github.event.pull_request.base.sha", "git fetch --depth=1", "--base-manifest", "pull_request"} {
		if !strings.Contains(conformance, want) {
			t.Errorf("conformance job must pass the base commit's manifest.yaml to blockverify on a pull request, and has no %q", want)
		}
	}
}

func TestWorkflow_ConformanceJobNeverPushesOrWritesARecord(t *testing.T) {
	text := readWorkflow(t)
	conformance := block(block(text, "jobs"), "conformance")
	if conformance == "" {
		t.Fatal("no conformance job block found")
	}
	for _, forbidden := range []string{"docker push", "blockprov push", "blockprov build"} {
		if strings.Contains(conformance, forbidden) {
			t.Errorf("conformance job must never run %q — a red run must produce no artefact beyond its report", forbidden)
		}
	}
}

func TestWorkflow_PublishJobPushesAndWritesTheRecord(t *testing.T) {
	text := readWorkflow(t)
	publish := block(block(text, "jobs"), "publish")
	if publish == "" {
		t.Fatal("no publish job block found")
	}
	for _, want := range []string{"docker push", "blockprov build", "blockprov push"} {
		if !strings.Contains(publish, want) {
			t.Errorf("publish job must run %q, job block:\n%s", want, publish)
		}
	}
}

func TestWorkflow_GrantsIDTokenWrite(t *testing.T) {
	text := readWorkflow(t)
	if !strings.Contains(text, "id-token: write") {
		t.Error("the workflow must grant id-token: write somewhere — publish mints the OIDC token this whole record depends on")
	}
}

func TestWorkflow_EveryUsesIsPinnedToACommitSha(t *testing.T) {
	text := readWorkflow(t)
	usesPattern := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*(\S+)`)
	shaPattern := regexp.MustCompile(`^[0-9a-f]{40}$`)

	matches := usesPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		t.Fatal("workflow has no `uses:` steps at all — nothing to pin")
	}
	for _, m := range matches {
		ref := m[1]
		at := strings.LastIndex(ref, "@")
		if at < 0 {
			t.Errorf("uses: %q has no @ref at all, want a commit sha", ref)
			continue
		}
		sha := ref[at+1:]
		if !shaPattern.MatchString(sha) {
			t.Errorf("uses: %q is pinned to %q, want a 40-character commit sha, not a tag", ref, sha)
		}
	}
}

func TestWorkflow_ReportIsUploadedAsAnArtifactRegardlessOfVerdict(t *testing.T) {
	text := readWorkflow(t)
	conformance := block(block(text, "jobs"), "conformance")
	if conformance == "" {
		t.Fatal("no conformance job block found")
	}
	if !strings.Contains(conformance, "upload-artifact") {
		t.Fatal("conformance job must upload the blockverify report as an artifact")
	}
	if !strings.Contains(conformance, "if: always()") {
		t.Error("the report upload must run with if: always() — a failing run's report is exactly the one that must not be lost")
	}
}

// sanity-check the test's own regexes against a known-shaped fixture, so a
// failure above is trusted to mean the workflow, not a bug in block().
func TestBlockHelper_ExtractsNestedKeysByIndentation(t *testing.T) {
	sample := strings.Join([]string{
		"jobs:",
		"  conformance:",
		"    needs: []",
		"    steps:",
		"      - run: go vet ./...",
		"  publish:",
		"    needs: conformance",
		"    steps:",
		"      - run: docker push",
	}, "\n")

	jobs := block(sample, "jobs")
	names := jobNames(jobs)
	if len(names) != 2 || names[0] != "conformance" || names[1] != "publish" {
		t.Fatalf("jobNames = %v, want [conformance publish]", names)
	}

	publish := block(jobs, "publish")
	if !strings.Contains(publish, "docker push") {
		t.Fatalf("block(jobs, publish) = %q, want it to contain the publish job's own steps", publish)
	}
	if strings.Contains(publish, "go vet") {
		t.Fatalf("block(jobs, publish) leaked the conformance job's content: %q", publish)
	}
}

// A republish of an already-tagged version must push nothing and write no record, so the guard step's script runs for real against a stub aws and every downstream step's if: is pinned.

// stepBlocks starts a new chunk only at the minimum indent used by any "- " item, so a step's nested lines stay inside it.
func stepBlocks(jobBlock string) []string {
	lines := strings.Split(jobBlock, "\n")
	itemPattern := regexp.MustCompile(`^(\s*)-\s`)

	minIndent := -1
	for _, line := range lines {
		if m := itemPattern.FindStringSubmatch(line); m != nil {
			if ind := len(m[1]); minIndent == -1 || ind < minIndent {
				minIndent = ind
			}
		}
	}

	var starts []int
	for i, line := range lines {
		if m := itemPattern.FindStringSubmatch(line); m != nil && len(m[1]) == minIndent {
			starts = append(starts, i)
		}
	}

	var blocks []string
	for idx, start := range starts {
		end := len(lines)
		if idx+1 < len(starts) {
			end = starts[idx+1]
		}
		blocks = append(blocks, strings.Join(lines[start:end], "\n"))
	}
	return blocks
}

func indexOfStepByID(blocks []string, id string) int {
	idPattern := regexp.MustCompile(`(?m)^\s*id:\s*` + regexp.QuoteMeta(id) + `\s*$`)
	for i, b := range blocks {
		if idPattern.MatchString(b) {
			return i
		}
	}
	return -1
}

func indexOfStepByName(blocks []string, name string) int {
	namePattern := regexp.MustCompile(`(?m)^\s*-?\s*name:\s*"?` + regexp.QuoteMeta(name) + `"?\s*$`)
	for i, b := range blocks {
		if namePattern.MatchString(b) {
			return i
		}
	}
	return -1
}

func ifOf(stepBlock string) string {
	m := regexp.MustCompile(`(?m)^\s*if:\s*(.+)\s*$`).FindStringSubmatch(stepBlock)
	if m == nil {
		return ""
	}
	expr := strings.TrimSpace(m[1])
	expr = strings.TrimPrefix(expr, "${{")
	expr = strings.TrimSuffix(expr, "}}")
	return strings.TrimSpace(expr)
}

func runScript(stepBlock string) string {
	body := block(stepBlock, "run")
	lines := strings.Split(body, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	minIndent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if ind := indentOf(l); minIndent == -1 || ind < minIndent {
			minIndent = ind
		}
	}
	if minIndent < 0 {
		minIndent = 0
	}
	for i, l := range lines {
		if len(l) >= minIndent {
			lines[i] = l[minIndent:]
		} else {
			lines[i] = strings.TrimLeft(l, " ")
		}
	}
	return strings.Join(lines, "\n")
}

func publishStepBlocks(t *testing.T) []string {
	t.Helper()
	text := readWorkflow(t)
	publish := block(block(text, "jobs"), "publish")
	if publish == "" {
		t.Fatal("no publish job block found")
	}
	return stepBlocks(publish)
}

func existingStepRunScript(t *testing.T) string {
	t.Helper()
	blocks := publishStepBlocks(t)
	idx := indexOfStepByID(blocks, "existing")
	if idx < 0 {
		t.Fatal("publish job has no step with `id: existing` — the tag-exists guard step is missing")
	}
	script := runScript(blocks[idx])
	if strings.TrimSpace(script) == "" {
		t.Fatal("publish job's `id: existing` step has no run: script")
	}
	return script
}

var testDigest = "sha256:" + strings.Repeat("ab", 32)

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

type guardResult struct {
	stdout, stderr, combined string
	exitCode                 int
	githubOutput             string
	githubEnv                string
	awsArgv                  string
}

func runGuardScript(t *testing.T, version, awsMode string) guardResult {
	t.Helper()
	return runProbe(t, version, awsMode, "absent")
}

// runProbe runs the probe against a stub aws that answers per requested tag, with imageMode for the version tag and recordMode for the record tag, each "exists", "absent" or "fail".
func runProbe(t *testing.T, version, imageMode, recordMode string) guardResult {
	t.Helper()
	return runProbeWithSha(t, version, imageMode, recordMode, "match")
}

var testSha = strings.Repeat("e", 40)

// runProbeWithSha adds shaMode for the commit-sha tag, where "match" answers the image's digest and "other" a different one.
func runProbeWithSha(t *testing.T, version, imageMode, recordMode, shaMode string) guardResult {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}

	script := existingStepRunScript(t)
	script = strings.ReplaceAll(script, "${{ inputs.ecr-repository }}", "mitstack-postgres-db")

	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "guard.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatalf("writing guard script: %v", err)
	}

	workDir := filepath.Join(tmp, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "spec:\n  version: " + version + "\n"
	if err := os.WriteFile(filepath.Join(workDir, "manifest.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	stubDir := filepath.Join(tmp, "stub")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	argvFile := filepath.Join(tmp, "aws-argv")
	stub := "#!/bin/sh\n" +
		"echo \"$@\" >> \"$AWS_ARGV_FILE\"\n" +
		"mode=\"\"\n" +
		"for a in \"$@\"; do\n" +
		"  case \"$a\" in\n" +
		"    imageTag=v*) mode=\"$AWS_IMAGE_MODE\" ;;\n" +
		"    imageTag=sha256-*.prov) mode=\"$AWS_RECORD_MODE\"; digest=\"$AWS_RECORD_DIGEST\" ;;\n" +
		"    imageTag=$GITHUB_SHA) mode=\"$AWS_SHA_MODE\" ;;\n" +
		"  esac\n" +
		"done\n" +
		"if [ \"$mode\" = other ]; then mode=exists; digest=\"$AWS_RECORD_DIGEST\"; fi\n" +
		"if [ \"$mode\" = match ]; then mode=exists; fi\n" +
		"[ -n \"$digest\" ] || digest=\"$AWS_DIGEST\"\n" +
		"case \"$mode\" in\n" +
		"  exists) echo \"$digest\"; exit 0 ;;\n" +
		"  absent) echo \"None\"; exit 0 ;;\n" +
		"  fail) echo \"boom: access denied\" >&2; exit 254 ;;\n" +
		"  *) echo \"stub aws: no imageTag it recognises in: $*\" >&2; exit 99 ;;\n" +
		"esac\n"
	writeExecutable(t, filepath.Join(stubDir, "aws"), stub)

	outputFile := filepath.Join(tmp, "github-output")
	envFile := filepath.Join(tmp, "github-env")
	for _, f := range []string{outputFile, envFile} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("bash", "--noprofile", "--norc", "-e", "-o", "pipefail", scriptPath)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"PATH="+stubDir+":"+os.Getenv("PATH"),
		"GITHUB_OUTPUT="+outputFile,
		"GITHUB_ENV="+envFile,
		"ECR_REPOSITORY=mitstack-postgres-db",
		"AWS_IMAGE_MODE="+imageMode,
		"AWS_RECORD_MODE="+recordMode,
		"AWS_SHA_MODE="+shaMode,
		"GITHUB_SHA="+testSha,
		"AWS_DIGEST="+testDigest,
		"AWS_RECORD_DIGEST="+testRecordDigest,
		"AWS_ARGV_FILE="+argvFile,
	)

	var stdout, stderr, combined strings.Builder
	cmd.Stdout = io.MultiWriter(&stdout, &combined)
	cmd.Stderr = io.MultiWriter(&stderr, &combined)

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("running guard script: %v", err)
		}
	}

	outputBytes, _ := os.ReadFile(outputFile)
	envBytes, _ := os.ReadFile(envFile)
	argvBytes, _ := os.ReadFile(argvFile)

	return guardResult{
		stdout:       stdout.String(),
		stderr:       stderr.String(),
		combined:     combined.String(),
		exitCode:     exitCode,
		githubOutput: string(outputBytes),
		githubEnv:    string(envBytes),
		awsArgv:      string(argvBytes),
	}
}

// testRecordDigest differs from the image's digest so a test can tell the two apart.
var testRecordDigest = "sha256:" + strings.Repeat("cd", 32)

var testRecordTag = "sha256-" + strings.Repeat("ab", 32) + ".prov"

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

func TestWorkflow_ExistingStepPrintsAndRecordsWhenTagExists(t *testing.T) {
	res := runGuardScript(t, "1.2.0", "exists")

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; combined output:\n%s", res.exitCode, res.combined)
	}
	want := "already published: v1.2.0 " + testDigest
	if !strings.Contains(res.stdout, want) {
		t.Errorf("stdout = %q, want it to contain %q", res.stdout, want)
	}
	if !strings.Contains(res.githubOutput, "published=true") {
		t.Errorf("$GITHUB_OUTPUT = %q, want it to contain %q", res.githubOutput, "published=true")
	}
	if !strings.Contains(res.githubOutput, "digest="+testDigest) {
		t.Errorf("$GITHUB_OUTPUT = %q, want it to contain %q", res.githubOutput, "digest="+testDigest)
	}
	for _, want := range []string{"ecr", "batch-get-image", "--repository-name", "mitstack-postgres-db", "imageTag=v1.2.0"} {
		if !strings.Contains(res.awsArgv, want) {
			t.Errorf("aws argv = %q, want it to contain %q", res.awsArgv, want)
		}
	}
}

func TestWorkflow_ExistingStepPublishesFalseWhenTagAbsent(t *testing.T) {
	res := runGuardScript(t, "1.2.0", "absent")

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; combined output:\n%s", res.exitCode, res.combined)
	}
	if strings.Contains(res.stdout, "already published") {
		t.Errorf("stdout = %q, must not claim a tag as already published when the registry said None", res.stdout)
	}
	if !strings.Contains(res.githubOutput, "published=false") {
		t.Errorf("$GITHUB_OUTPUT = %q, want it to contain %q", res.githubOutput, "published=false")
	}
}

func TestWorkflow_ExistingStepFailsClosedWhenTheProbeCannotAnswer(t *testing.T) {
	res := runGuardScript(t, "1.2.0", "fail")

	if res.exitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero — a probe that cannot answer must never be read as \"not published\"; combined output:\n%s", res.combined)
	}
	if !strings.Contains(res.combined, "v1.2.0") {
		t.Errorf("combined output = %q, want it to name the tag v1.2.0 it could not confirm", res.combined)
	}
}

func TestWorkflow_ExistingStepAVersionBumpStillPublishes(t *testing.T) {
	res := runGuardScript(t, "1.3.0", "absent")

	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; combined output:\n%s", res.exitCode, res.combined)
	}
	if strings.Contains(res.stdout, "already published") {
		t.Errorf("stdout = %q, must not claim a tag as already published when the registry said None", res.stdout)
	}
	if !strings.Contains(res.githubOutput, "published=false") {
		t.Errorf("$GITHUB_OUTPUT = %q, want it to contain %q", res.githubOutput, "published=false")
	}
	if !strings.Contains(res.awsArgv, "imageTag=v1.3.0") {
		t.Errorf("aws argv = %q, want the bumped version's tag imageTag=v1.3.0 — a version bump must still probe and publish through the same workflow", res.awsArgv)
	}
}

func TestWorkflow_ExistingStepPrecedesBuildAndPushImage(t *testing.T) {
	blocks := publishStepBlocks(t)
	existingIdx := indexOfStepByID(blocks, "existing")
	if existingIdx < 0 {
		t.Fatal("publish job has no step with `id: existing` — the tag-exists guard step is missing")
	}
	buildIdx := indexOfStepByName(blocks, "Build and push image")
	if buildIdx < 0 {
		t.Fatal(`publish job has no "Build and push image" step`)
	}
	if existingIdx >= buildIdx {
		t.Errorf("the `id: existing` guard step is at position %d, the \"Build and push image\" step is at position %d — the guard must run first", existingIdx, buildIdx)
	}
}

// imageSteps push the image; recordSteps build and push the provenance
// record. writeSteps are the ones that write to the registry or mint a
// signed record.
var (
	imageSteps  = []string{"Build and push image"}
	recordSteps = []string{
		"Determine the kit version pinned in go.mod",
		"blockverify (standalone, for blockprov build's report)",
		"blockprov build",
		"Fetch ECR password",
		"blockprov push",
	}
	writeSteps = []string{"Build and push image", "blockprov build", "Fetch ECR password", "blockprov push"}
)

func TestWorkflow_ImageStepsGateOnThePublishedAnswer(t *testing.T) {
	blocks := publishStepBlocks(t)
	const wantIf = "steps.existing.outputs.published == 'false'"
	for _, name := range imageSteps {
		idx := indexOfStepByName(blocks, name)
		if idx < 0 {
			t.Errorf("publish job has no step named %q", name)
			continue
		}
		if got := ifOf(blocks[idx]); got != wantIf {
			t.Errorf("step %q has if: %q, want %q — the image is pushed only when the version tag is not published", name, got, wantIf)
		}
	}
}

func TestWorkflow_RecordStepsGateOnTheRecordAnswer(t *testing.T) {
	blocks := publishStepBlocks(t)
	const wantIf = "steps.existing.outputs.record == 'false'"
	for _, name := range recordSteps {
		idx := indexOfStepByName(blocks, name)
		if idx < 0 {
			t.Errorf("publish job has no step named %q", name)
			continue
		}
		if got := ifOf(blocks[idx]); got != wantIf {
			t.Errorf("step %q has if: %q, want %q — the record is written only when the probe found none", name, got, wantIf)
		}
	}
}

// A skipped step's output is the empty string, so a `!=` gate against the
// probe runs its step when the probe never answered.
func TestWorkflow_NoStepGatesOnAnInequalityAgainstTheProbe(t *testing.T) {
	for _, b := range publishStepBlocks(t) {
		expr := ifOf(b)
		if strings.Contains(expr, "steps.existing.outputs") && strings.Contains(expr, "!=") {
			t.Errorf("step gated on %q: a skipped probe answers '' and `!=` then runs the step; gate with == 'false' instead", expr)
		}
	}
}

func jobIf(jobBlock string) string {
	minIndent := -1
	for _, line := range strings.Split(jobBlock, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if ind := indentOf(line); minIndent == -1 || ind < minIndent {
			minIndent = ind
		}
	}
	for _, line := range strings.Split(jobBlock, "\n") {
		if indentOf(line) == minIndent && strings.HasPrefix(strings.TrimSpace(line), "if:") {
			return ifOf(line)
		}
	}
	return ""
}

func TestWorkflow_PublishRunsOnlyOnAPushToTheDefaultBranch(t *testing.T) {
	jobs := block(readWorkflow(t), "jobs")
	const want = "github.event_name == 'push' && github.ref == format('refs/heads/{0}', github.event.repository.default_branch)"
	if got := jobIf(block(jobs, "publish")); got != want {
		t.Errorf("publish job's if: = %q, want %q — a pull-request run of a caller must get conformance without a publish", got, want)
	}
	if got := jobIf(block(jobs, "conformance")); got != "" {
		t.Errorf("conformance job's if: = %q, want none — a pull request must still run conformance", got)
	}
}

func TestWorkflow_HeaderStatesThePublishEventAndWhatARerunCompletes(t *testing.T) {
	var header []string
	for _, line := range strings.Split(readWorkflow(t), "\n") {
		if strings.HasPrefix(line, "name:") {
			break
		}
		header = append(header, line)
	}
	text := strings.ToLower(strings.Join(header, " "))
	text = strings.Join(strings.Fields(strings.ReplaceAll(text, "#", " ")), " ")
	for _, want := range []string{"push", "default branch", "rerun", "provenance record"} {
		if !strings.Contains(text, want) {
			t.Errorf("the header comment must state the publish event and what a rerun completes; it lacks %q", want)
		}
	}
}

// stepRuns fails the test on any if: form it does not know, so a new gate cannot pass unexamined.
func stepRuns(t *testing.T, stepBlock string, outputs map[string]string) bool {
	t.Helper()
	expr := ifOf(stepBlock)
	switch expr {
	case "":
		return true
	case "always()":
		return true
	}
	m := regexp.MustCompile(`^steps\.existing\.outputs\.(\w+) == '([^']*)'$`).FindStringSubmatch(expr)
	if m == nil {
		t.Fatalf("step if: %q is not a form this evaluator knows", expr)
	}
	return outputs[m[1]] == m[2]
}

func stepRunsByName(t *testing.T, blocks []string, name string, outputs map[string]string) bool {
	t.Helper()
	idx := indexOfStepByName(blocks, name)
	if idx < 0 {
		t.Fatalf("publish job has no step named %q", name)
	}
	return stepRuns(t, blocks[idx], outputs)
}

func runStepBody(t *testing.T, name string, env map[string]string) string {
	t.Helper()
	blocks := publishStepBlocks(t)
	idx := indexOfStepByName(blocks, name)
	if idx < 0 {
		t.Fatalf("publish job has no step named %q", name)
	}
	literals := map[string]string{
		"inputs.block-id":       "postgres-db",
		"inputs.ecr-repository": "mitstack-postgres-db",
		"inputs.aws-region":     "eu-central-1",
		"github.repository":     "mitstack-eu/postgres-db",
		"github.server_url":     "https://github.com",
		"github.run_id":         "42",
	}
	script := regexp.MustCompile(`\$\{\{\s*([^}]+?)\s*\}\}`).ReplaceAllStringFunc(runScript(blocks[idx]), func(s string) string {
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
	if err := os.WriteFile(filepath.Join(tmp, "manifest.yaml"), []byte("spec:\n  version: 1.2.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(tmp, "step.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "--noprofile", "--norc", "-e", "-o", "pipefail", scriptPath)
	cmd.Dir = tmp
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("step %q failed: %v\n%s", name, err, out)
	}
	argv, _ := os.ReadFile(argvFile)
	return string(argv)
}

func TestWorkflow_ExistingStepReportsARecordAbsentBesideAPublishedTag(t *testing.T) {
	res := runProbe(t, "1.2.0", "exists", "absent")
	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; combined output:\n%s", res.exitCode, res.combined)
	}
	out := keyValues(res.githubOutput)
	if out["published"] != "true" || out["record"] != "false" || out["digest"] != testDigest {
		t.Errorf("$GITHUB_OUTPUT = %q, want published=true, record=false, digest=%s", res.githubOutput, testDigest)
	}
	if got := keyValues(res.githubEnv)["IMAGE_DIGEST"]; got != testDigest {
		t.Errorf("$GITHUB_ENV IMAGE_DIGEST = %q, want the published tag's digest %q", got, testDigest)
	}
	if !strings.Contains(res.awsArgv, "imageTag="+testRecordTag) {
		t.Errorf("aws argv = %q, want a probe for the record tag %s", res.awsArgv, testRecordTag)
	}
	if !strings.Contains(res.stdout, "no provenance record") || !strings.Contains(res.stdout, testRecordTag) {
		t.Errorf("stdout = %q, want it to say there is no provenance record and name %s", res.stdout, testRecordTag)
	}
}

func TestWorkflow_ExistingStepReportsARecordPresentBesideAPublishedTag(t *testing.T) {
	res := runProbe(t, "1.2.0", "exists", "exists")
	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; combined output:\n%s", res.exitCode, res.combined)
	}
	out := keyValues(res.githubOutput)
	if out["published"] != "true" || out["record"] != "true" || out["digest"] != testDigest {
		t.Errorf("$GITHUB_OUTPUT = %q, want published=true, record=true, digest=%s", res.githubOutput, testDigest)
	}
	if !strings.Contains(res.stdout, "provenance record present") {
		t.Errorf("stdout = %q, want it to say the provenance record is present", res.stdout)
	}
}

func TestWorkflow_ExistingStepDoesNotProbeARecordForAnUnpublishedTag(t *testing.T) {
	res := runProbe(t, "1.2.0", "absent", "fail")
	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; combined output:\n%s", res.exitCode, res.combined)
	}
	out := keyValues(res.githubOutput)
	if out["published"] != "false" || out["record"] != "false" {
		t.Errorf("$GITHUB_OUTPUT = %q, want published=false and record=false", res.githubOutput)
	}
	if strings.Contains(res.awsArgv, ".prov") {
		t.Errorf("aws argv = %q, want no record probe when the image tag is not published", res.awsArgv)
	}
}

func TestWorkflow_ExistingStepFailsClosedWhenTheRecordProbeCannotAnswer(t *testing.T) {
	res := runProbe(t, "1.2.0", "exists", "fail")
	if res.exitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero — a record probe that cannot answer must never be read as \"no record\"; combined output:\n%s", res.combined)
	}
	if !strings.Contains(res.combined, testRecordTag) {
		t.Errorf("combined output = %q, want it to name the record tag %s it could not confirm", res.combined, testRecordTag)
	}
}

func TestWorkflow_ARerunCompletesTheRecordForAPublishedTagWithoutPushingTheImage(t *testing.T) {
	res := runProbe(t, "1.2.0", "exists", "absent")
	if res.exitCode != 0 {
		t.Fatalf("probe exit code = %d; combined output:\n%s", res.exitCode, res.combined)
	}
	outputs := keyValues(res.githubOutput)
	blocks := publishStepBlocks(t)

	for _, name := range imageSteps {
		if stepRunsByName(t, blocks, name, outputs) {
			t.Errorf("step %q runs, want it skipped — the published image is never pushed again", name)
		}
	}
	for _, name := range recordSteps {
		if !stepRunsByName(t, blocks, name, outputs) {
			t.Errorf("step %q is skipped, want it to run — the missing record must be completed", name)
		}
	}

	env := keyValues(res.githubEnv)
	env["REGISTRY"] = "123456789012.dkr.ecr.eu-central-1.amazonaws.com"
	env["KIT_VERSION"] = "v0.9.0"
	env["ECR_PASSWORD"] = "stub-password"
	env["GITHUB_SHA"] = strings.Repeat("f", 40)
	env["ACTIONS_ID_TOKEN_REQUEST_URL"] = "https://token.example"
	env["ACTIONS_ID_TOKEN_REQUEST_TOKEN"] = "stub-token"

	build := runStepBody(t, "blockprov build", env)
	if !strings.Contains(build, "--image-digest "+testDigest) {
		t.Errorf("blockprov build argv = %q, want --image-digest %s", build, testDigest)
	}
	if !strings.Contains(build, "--image-ref "+env["REGISTRY"]+"/mitstack-postgres-db@"+testDigest) {
		t.Errorf("blockprov build argv = %q, want --image-ref ending @%s", build, testDigest)
	}
	push := runStepBody(t, "blockprov push", env)
	if !strings.Contains(push, "--image-digest "+testDigest) {
		t.Errorf("blockprov push argv = %q, want --image-digest %s", push, testDigest)
	}
}

func TestWorkflow_ATagWithItsRecordWritesNothing(t *testing.T) {
	res := runProbe(t, "1.2.0", "exists", "exists")
	if res.exitCode != 0 {
		t.Fatalf("probe exit code = %d, want 0 so the job ends green; combined output:\n%s", res.exitCode, res.combined)
	}
	outputs := keyValues(res.githubOutput)
	blocks := publishStepBlocks(t)
	for _, name := range writeSteps {
		if stepRunsByName(t, blocks, name, outputs) {
			t.Errorf("step %q runs, want every write step skipped when the tag and its record both exist", name)
		}
	}
}

func TestWorkflow_AnUnpublishedVersionRunsTheImageAndRecordSteps(t *testing.T) {
	res := runProbe(t, "1.3.0", "absent", "absent")
	if res.exitCode != 0 {
		t.Fatalf("probe exit code = %d; combined output:\n%s", res.exitCode, res.combined)
	}
	outputs := keyValues(res.githubOutput)
	blocks := publishStepBlocks(t)
	for _, name := range append(append([]string{}, imageSteps...), recordSteps...) {
		if !stepRunsByName(t, blocks, name, outputs) {
			t.Errorf("step %q is skipped, want it to run for a version not yet published", name)
		}
	}
}

// A probe that was itself skipped answers nothing; no write step may run.
func TestWorkflow_ASilentProbeRunsNoWriteStep(t *testing.T) {
	blocks := publishStepBlocks(t)
	for _, name := range writeSteps {
		if stepRunsByName(t, blocks, name, map[string]string{}) {
			t.Errorf("step %q runs with no probe answer, want it skipped", name)
		}
	}
}

// The kit is a public module, so the publish job assumes its role for the push alone and fetches the kit with no credential.
func TestWorkflow_PublishFetchesThePublicKitWithNoCredential(t *testing.T) {
	publish := block(block(readWorkflow(t), "jobs"), "publish")
	if publish == "" {
		t.Fatal("no publish job block found")
	}
	for _, banned := range []string{"MITSTACK_GITHUB_APP", "access_tokens", "kit_token", "kit-token", "KIT_TOKEN: ${{", "GOPRIVATE", "insteadOf"} {
		if strings.Contains(publish, banned) {
			t.Errorf("the publish job names %q, so it still mints or carries a kit credential", banned)
		}
	}
	if !strings.Contains(publish, "role-to-assume: ${{ vars.AWS_BLOCK_PUBLISH_ROLE_ARN }}") {
		t.Error("the publish job no longer assumes the publish role, which the image push needs")
	}
	blocks := stepBlocks(publish)
	checkout := indexOfStepByName(blocks, "Checkout operator-kit")
	if checkout < 0 {
		t.Fatal(`publish job has no "Checkout operator-kit" step`)
	}
	if with := block(blocks[checkout], "with"); regexp.MustCompile(`(?m)^\s*token:`).MatchString(with) {
		t.Errorf("publish kit checkout with: =\n%s\nwant no token key, so the public kit is read with the run's default token", with)
	}
	build := indexOfStepByName(blocks, "Build and push image")
	if build < 0 {
		t.Fatal(`publish job has no "Build and push image" step`)
	}
	if strings.Contains(runScript(blocks[build]), "--secret") {
		t.Errorf("publish image build run =\n%s\nwant no --secret, so the image fetches the public kit with no credential", runScript(blocks[build]))
	}
}

// A record signs this run's commit, so it is completed only for an image this commit built.
func TestWorkflow_ExistingStepChecksThePublishedImageWasBuiltFromThisCommit(t *testing.T) {
	res := runProbeWithSha(t, "1.2.0", "exists", "absent", "match")
	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; combined output:\n%s", res.exitCode, res.combined)
	}
	if !strings.Contains(res.awsArgv, "imageTag="+testSha) {
		t.Errorf("aws argv = %q, want a probe for this commit's tag %s before a record is completed", res.awsArgv, testSha)
	}
	if got := keyValues(res.githubOutput)["record"]; got != "false" {
		t.Errorf("record = %q, want false so the record steps complete it", got)
	}
}

func TestWorkflow_ExistingStepRefusesToCompleteARecordForAnotherCommitsImage(t *testing.T) {
	for _, shaMode := range []string{"other", "absent", "fail"} {
		t.Run(shaMode, func(t *testing.T) {
			res := runProbeWithSha(t, "1.2.0", "exists", "absent", shaMode)
			if res.exitCode == 0 {
				t.Fatalf("exit code = 0, want non-zero — a record signed at this commit must never describe an image another commit built; combined output:\n%s", res.combined)
			}
			if got := keyValues(res.githubOutput)["record"]; got == "false" {
				t.Errorf("record = false, want no answer the record steps would act on")
			}
			if !strings.Contains(res.combined, testSha) {
				t.Errorf("combined output = %q, want it to name this commit %s", res.combined, testSha)
			}
		})
	}
}

func TestWorkflow_ARecordPresentNeedsNoCommitCheck(t *testing.T) {
	res := runProbeWithSha(t, "1.2.0", "exists", "exists", "fail")
	if res.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 — a tag with its record writes nothing, so its commit is not asked; combined output:\n%s", res.exitCode, res.combined)
	}
}

// The kit checkout must be gone before blockverify or the image build reads the tree, or the block is judged by the kit's own source.
func TestWorkflow_TheKitCheckoutLeavesTheBlockTreeBeforeItIsJudged(t *testing.T) {
	jobs := block(readWorkflow(t), "jobs")
	for _, tc := range []struct{ job, build, firstReader string }{
		{"conformance", "Build blockverify", "blockverify (standalone contract check)"},
		{"publish", "Build blockverify and blockprov", "Build and push image"},
	} {
		t.Run(tc.job, func(t *testing.T) {
			blocks := stepBlocks(block(jobs, tc.job))
			const name = "Remove the kit checkout from the block tree"
			rm := indexOfStepByName(blocks, name)
			if rm < 0 {
				t.Fatalf("%s job has no step %q", tc.job, name)
			}
			if b := indexOfStepByName(blocks, tc.build); b < 0 || rm < b {
				t.Errorf("%q is at %d, want it after %q at %d", name, rm, tc.build, b)
			}
			if r := indexOfStepByName(blocks, tc.firstReader); r < 0 || rm > r {
				t.Errorf("%q is at %d, want it before %q at %d", name, rm, tc.firstReader, r)
			}
			if got := ifOf(blocks[rm]); got != "" {
				t.Errorf("%q has if: %q, want none", name, got)
			}

			tmp := t.TempDir()
			if err := os.MkdirAll(filepath.Join(tmp, "operator-kit", "events"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(tmp, "operator-kit", "events", "events.go"), []byte("package events\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(t.TempDir(), "rm.sh")
			if err := os.WriteFile(script, []byte(runScript(blocks[rm])), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "--noprofile", "--norc", "-e", "-o", "pipefail", script)
			cmd.Dir = tmp
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("step failed: %v\n%s", err, out)
			}
			if _, err := os.Stat(filepath.Join(tmp, "operator-kit")); !os.IsNotExist(err) {
				t.Errorf("operator-kit/ still exists after the step (stat err %v)", err)
			}
			if _, err := os.Stat(filepath.Join(tmp, "main.go")); err != nil {
				t.Errorf("the block's own main.go is gone: %v", err)
			}
		})
	}
}
