package main

// kit_fetch_retry_workflow_test.go guards the bounded kit fetch retry in publish-block.yml.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func jobStepBlocks(t *testing.T, job string) []string {
	t.Helper()
	b := block(block(readWorkflow(t), "jobs"), job)
	if b == "" {
		t.Fatalf("no %s job block found", job)
	}
	return stepBlocks(b)
}

func stepByName(t *testing.T, job, name string) (int, string) {
	t.Helper()
	blocks := jobStepBlocks(t, job)
	idx := indexOfStepByName(blocks, name)
	if idx < 0 {
		t.Fatalf("%s job has no step named %q", job, name)
	}
	return idx, blocks[idx]
}

var fetchRetryCases = []struct {
	job, step, command, before string
}{
	{"conformance", "Fetch Go modules", "go mod download", "go vet"},
	{"publish", "Fetch Go modules", "go mod download", "Build and push image"},
}

func TestWorkflow_EachJobRetriesTheKitFetchBeforeUsingIt(t *testing.T) {
	for _, c := range fetchRetryCases {
		idx, b := stepByName(t, c.job, c.step)
		if !strings.Contains(runScript(b), "kit_fetch_retry "+c.command) {
			t.Errorf("%s step %q does not run %q through kit_fetch_retry", c.job, c.step, c.command)
		}
		if strings.Contains(b, "KIT_TOKEN") {
			t.Errorf("%s step %q names KIT_TOKEN, but neither job mints a kit token", c.job, c.step)
		}
		if before, _ := stepByName(t, c.job, c.before); idx > before {
			t.Errorf("%s step %q must sit before %q", c.job, c.step, c.before)
		}
	}
}

// The publish job's blockverify compiles the block and runs when only the
// provenance record is missing, so the fetch step runs whatever the probe answers.
func TestWorkflow_ThePublishFetchRunsWhateverTheProbeAnswers(t *testing.T) {
	_, b := stepByName(t, "publish", "Fetch Go modules")
	if got := ifOf(b); got != "" {
		t.Errorf("the publish job's Fetch Go modules step is gated on %q; it must run on every publish", got)
	}
	blocks := jobStepBlocks(t, "publish")
	fetch := indexOfStepByName(blocks, "Fetch Go modules")
	for _, later := range []string{"Build and push image", "blockverify (standalone, for blockprov build's report)"} {
		if idx := indexOfStepByName(blocks, later); idx >= 0 && idx < fetch {
			t.Errorf("step %q runs before the publish job's Fetch Go modules step", later)
		}
	}
}

func TestWorkflow_BothJobsRetryWithTheSameFunction(t *testing.T) {
	var bodies []string
	for _, c := range fetchRetryCases {
		_, b := stepByName(t, c.job, c.step)
		s := runScript(b)
		i := strings.Index(s, "kit_fetch_retry() {")
		j := strings.Index(s, "\n}\n")
		if i < 0 || j < i {
			t.Fatalf("%s step %q does not define kit_fetch_retry()", c.job, c.step)
		}
		bodies = append(bodies, s[i:j])
	}
	if bodies[0] != bodies[1] {
		t.Errorf("the two jobs' kit_fetch_retry() bodies differ; keep them identical")
	}
}

// fetchStepResult is one execution of a retry step's run body.
type fetchStepResult struct {
	out        string
	code       int
	calls      int
	curlCalled bool
}

// runFetchStep executes a job's retry step body with the retried command
// replaced by a stub that fails with GitHub's not-found answer for its
// first failFirst calls (every call when negative), and a stub curl that
// records being called.
func runFetchStep(t *testing.T, job string, failFirst int, env map[string]string) fetchStepResult {
	t.Helper()
	var c = fetchRetryCases[0]
	for _, fc := range fetchRetryCases {
		if fc.job == job {
			c = fc
		}
	}
	_, b := stepByName(t, job, c.step)
	script := runScript(b)
	if strings.Contains(script, "${{") {
		t.Fatalf("%s step %q interpolates an expression into its script; pass values through env", job, c.step)
	}

	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(tmp, "calls")
	stub := "#!/usr/bin/env bash\n" +
		"n=$(( $(cat " + strconv.Quote(counter) + " 2>/dev/null || echo 0) + 1 ))\n" +
		"echo \"$n\" > " + strconv.Quote(counter) + "\n" +
		"if [ " + strconv.Itoa(failFirst) + " -lt 0 ] || [ \"$n\" -le " + strconv.Itoa(failFirst) + " ]; then\n" +
		"  echo \"remote: Repository not found.\" >&2\n  exit 128\nfi\necho fetched\n"
	// The retried command's first word is the binary the stub stands in for.
	writeExecutable(t, filepath.Join(bin, strings.Fields(c.command)[0]), stub)
	curlMarker := filepath.Join(tmp, "curl-called")
	writeExecutable(t, filepath.Join(bin, "curl"), "#!/usr/bin/env bash\ntouch "+strconv.Quote(curlMarker)+"\nexit 22\n")
	scriptPath := filepath.Join(tmp, "step.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	// A retry that never ends is killed here rather than hanging the suite.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "--noprofile", "--norc", "-e", "-o", "pipefail", scriptPath)
	cmd.Dir = tmp
	cmd.Env = []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + tmp,
		"KIT_FETCH_BACKOFF_SECONDS=0",
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running %s step: %v", job, err)
		}
		code = ee.ExitCode()
	}
	raw, _ := os.ReadFile(counter)
	calls, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	_, curlErr := os.Stat(curlMarker)
	return fetchStepResult{out: string(out), code: code, calls: calls, curlCalled: curlErr == nil}
}

func needBashAndJq(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH", tool)
		}
	}
}

func TestWorkflow_AFetchThatFailsTwiceSucceedsOnTheThirdAttempt(t *testing.T) {
	needBashAndJq(t)
	for _, job := range []string{"conformance", "publish"} {
		r := runFetchStep(t, job, 2, nil)
		if r.code != 0 {
			t.Fatalf("%s: exit %d, want 0:\n%s", job, r.code, r.out)
		}
		if r.calls != 3 {
			t.Errorf("%s: the fetch ran %d times, want 3", job, r.calls)
		}
		for _, want := range []string{
			"::warning::kit fetch attempt 1/5 failed (exit 128)",
			"Repository not found",
			"kit fetch succeeded on attempt 3/5",
			"fetched",
		} {
			if !strings.Contains(r.out, want) {
				t.Errorf("%s: output lacks %q:\n%s", job, want, r.out)
			}
		}
	}
}

func TestWorkflow_AFirstAttemptSuccessWarnsNothing(t *testing.T) {
	needBashAndJq(t)
	for _, job := range []string{"conformance", "publish"} {
		r := runFetchStep(t, job, 0, nil)
		if r.code != 0 || r.calls != 1 {
			t.Fatalf("%s: exit %d after %d calls, want 0 after 1:\n%s", job, r.code, r.calls, r.out)
		}
		if strings.Contains(r.out, "::warning::") {
			t.Errorf("%s: a first-attempt success printed a warning:\n%s", job, r.out)
		}
	}
}

func TestWorkflow_TheKitFetchRetryIsBounded(t *testing.T) {
	needBashAndJq(t)
	for _, job := range []string{"conformance", "publish"} {
		r := runFetchStep(t, job, -1, map[string]string{"KIT_FETCH_ATTEMPTS": "3"})
		if r.code == 0 {
			t.Fatalf("%s: exit 0 after every attempt failed:\n%s", job, r.out)
		}
		if r.calls != 3 {
			t.Errorf("%s: the fetch ran %d times, want exactly 3", job, r.calls)
		}
		if !strings.Contains(r.out, "::error::kit fetch failed after 3 attempts") {
			t.Errorf("%s: output lacks the final error line:\n%s", job, r.out)
		}
	}
}

// The kit is public, so a failed attempt asks no API about a kit token even when one is in the environment.
func TestWorkflow_TheKitFetchRetryReadsNoKitToken(t *testing.T) {
	needBashAndJq(t)
	for _, job := range []string{"conformance", "publish"} {
		r := runFetchStep(t, job, 1, map[string]string{"KIT_TOKEN": "tok", "KIT_TOKEN_MINTED_AT": "1"})
		if r.code != 0 {
			t.Fatalf("%s: exit %d, want 0:\n%s", job, r.code, r.out)
		}
		if r.curlCalled {
			t.Errorf("%s: a failed attempt called curl, want no kit token API read", job)
		}
		if strings.Contains(r.out, "kit token") {
			t.Errorf("%s: output names a kit token:\n%s", job, r.out)
		}
	}
}

func TestWorkflow_TheKitFetchRetryRefusesAnInvalidBound(t *testing.T) {
	needBashAndJq(t)
	for _, job := range []string{"conformance", "publish"} {
		for _, env := range []map[string]string{
			{"KIT_FETCH_ATTEMPTS": "abc"},
			{"KIT_FETCH_ATTEMPTS": "0"},
			{"KIT_FETCH_ATTEMPTS": "3", "KIT_FETCH_BACKOFF_SECONDS": "x"},
		} {
			// runFetchStep kills a step that runs past its deadline, which
			// then reads as a non-2 exit with calls made.
			r := runFetchStep(t, job, -1, env)
			if r.code != 2 || r.calls != 0 {
				t.Errorf("%s %v: exit %d after %d calls, want exit 2 with no call:\n%s", job, env, r.code, r.calls, r.out)
			}
		}
	}
}

// Conformance runs on every pull request, so it never assumes the publish role or reads its App key.
func TestWorkflow_ConformanceRunsWithoutThePublishRole(t *testing.T) {
	conformance := block(block(readWorkflow(t), "jobs"), "conformance")
	if conformance == "" {
		t.Fatal("no conformance job block found")
	}
	for _, banned := range []string{"id-token: write", "role-to-assume", "AWS_BLOCK_PUBLISH_ROLE_ARN", "MITSTACK_GITHUB_APP_PRIVATE_KEY", "kit_token", "KIT_TOKEN: ${{"} {
		if strings.Contains(conformance, banned) {
			t.Errorf("the conformance job names %q, so a pull request's run reaches the publish role or its App key", banned)
		}
	}
}
