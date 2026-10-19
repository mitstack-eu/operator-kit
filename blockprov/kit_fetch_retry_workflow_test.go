package main

// kit_fetch_retry_workflow_test.go guards the bounded kit fetch retry in publish-block.yml and the publish job's mint log.

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

// fetchRetryCases names each job's retry step and its mint step, empty where the job mints no token.
var fetchRetryCases = []struct {
	job, step, command, mint, before string
}{
	{"conformance", "Fetch Go modules", "go mod download", "", "go vet"},
	{"publish", "Fetch Go modules", "go mod download", "Mint kit token", "Build and push image"},
}

func TestWorkflow_EachJobRetriesTheKitFetchBeforeUsingIt(t *testing.T) {
	for _, c := range fetchRetryCases {
		idx, b := stepByName(t, c.job, c.step)
		if !strings.Contains(runScript(b), "kit_fetch_retry "+c.command) {
			t.Errorf("%s step %q does not run %q through kit_fetch_retry", c.job, c.step, c.command)
		}
		hasToken := strings.Contains(b, "KIT_TOKEN: ${{ steps.kit_token.outputs.token }}")
		if c.mint == "" && hasToken {
			t.Errorf("%s step %q receives a kit token, but the job mints none", c.job, c.step)
		}
		if c.mint != "" {
			if !hasToken {
				t.Errorf("%s step %q does not receive the kit token through env", c.job, c.step)
			}
			if mint, _ := stepByName(t, c.job, c.mint); mint > idx {
				t.Errorf("%s step %q must sit after %q", c.job, c.step, c.mint)
			}
		}
		if before, _ := stepByName(t, c.job, c.before); idx > before {
			t.Errorf("%s step %q must sit before %q", c.job, c.step, c.before)
		}
	}
}

// The publish job's blockverify run compiles the block with `go test`, and
// it runs when only the provenance record is missing, so the fetch step runs
// whatever the probe answers.
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

func TestWorkflow_MintStepsLogTheTokenAnswerWithoutTheToken(t *testing.T) {
	for _, job := range []string{"publish"} {
		_, b := stepByName(t, job, "Mint kit token")
		s := runScript(b)
		for _, want := range []string{"expires_at", ".repositories", "KIT_TOKEN_MINTED_AT=", `"repositories":["operator-kit"]`} {
			if !strings.Contains(s, want) {
				t.Errorf("%s Mint kit token step lacks %q", job, want)
			}
		}
		var logLine string
		for _, l := range strings.Split(s, "\n") {
			if strings.Contains(l, "kit token minted:") {
				logLine = l
			}
		}
		if logLine == "" {
			t.Errorf("%s Mint kit token step has no mint log line", job)
		} else if _, err := exec.LookPath("jq"); err == nil {
			const sentinel = "ghs_SENTINELmintTOKEN0123456789"
			cmd := exec.Command("bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "-c", logLine)
			cmd.Env = append(os.Environ(), `RESPONSE={"token":"`+sentinel+`","expires_at":"2026-09-26T08:00:00Z","repositories":[{"name":"operator-kit"}]}`)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("%s mint log line failed: %v\n%s", job, err, out)
			}
			if strings.Contains(string(out), sentinel) {
				t.Errorf("%s mint log line prints the token:\n%s", job, out)
			}
			if want := "kit token minted: expires_at=2026-09-26T08:00:00Z repositories=operator-kit"; !strings.Contains(string(out), want) {
				t.Errorf("%s mint log line printed %q, want %q", job, out, want)
			}
		}
		mask := strings.Index(s, `::add-mask::$TOKEN`)
		logged := strings.Index(s, "kit token minted:")
		if mask < 0 || logged < 0 || logged < mask {
			t.Errorf("%s Mint kit token step must mask the token before it logs the mint answer", job)
		}
	}
}

// fetchStepResult is one execution of a retry step's run body.
type fetchStepResult struct {
	out   string
	code  int
	calls int
}

// runFetchStep executes a job's retry step body with the retried command
// replaced by a stub that fails with GitHub's not-found answer for its
// first failFirst calls (every call when negative), and a stub curl that
// answers the installation repositories request, or fails when curlOK is
// false.
func runFetchStep(t *testing.T, job string, failFirst int, curlOK bool, env map[string]string) fetchStepResult {
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
		"  echo \"remote: Repository not found. (token ${KIT_TOKEN:-none})\" >&2\n  exit 128\nfi\necho fetched\n"
	// The retried command's first word is the binary the stub stands in for.
	writeExecutable(t, filepath.Join(bin, strings.Fields(c.command)[0]), stub)
	// The failing curl echoes its own arguments, the Authorization header
	// among them, so printing its error text unredacted leaks the token.
	curl := "#!/usr/bin/env bash\necho \"curl: (22) The requested URL returned error: 401 ($*)\" >&2\nexit 22\n"
	if curlOK {
		curl = "#!/usr/bin/env bash\necho '{\"repositories\":[{\"name\":\"operator-kit\"}]}'\n"
	}
	writeExecutable(t, filepath.Join(bin, "curl"), curl)
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
	return fetchStepResult{out: string(out), code: code, calls: calls}
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
		minted := strconv.FormatInt(time.Now().Unix()-3, 10)
		r := runFetchStep(t, job, 2, true, map[string]string{"KIT_TOKEN": "tok", "KIT_TOKEN_MINTED_AT": minted})
		if r.code != 0 {
			t.Fatalf("%s: exit %d, want 0:\n%s", job, r.code, r.out)
		}
		if r.calls != 3 {
			t.Errorf("%s: the fetch ran %d times, want 3", job, r.calls)
		}
		for _, want := range []string{
			"::warning::kit fetch attempt 1/5 failed",
			"exit 128",
			"s after the kit token was minted",
			"Repository not found",
			"kit token repository view: operator-kit",
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
		r := runFetchStep(t, job, 0, true, nil)
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
		r := runFetchStep(t, job, -1, false, map[string]string{"KIT_TOKEN": "tok", "KIT_FETCH_ATTEMPTS": "3"})
		if r.code == 0 {
			t.Fatalf("%s: exit 0 after every attempt failed:\n%s", job, r.out)
		}
		if r.calls != 3 {
			t.Errorf("%s: the fetch ran %d times, want exactly 3", job, r.calls)
		}
		for _, want := range []string{"kit token repository view: unavailable", "::error::kit fetch failed after 3 attempts"} {
			if !strings.Contains(r.out, want) {
				t.Errorf("%s: output lacks %q:\n%s", job, want, r.out)
			}
		}
	}
}

func TestWorkflow_TheKitFetchRetryNeverPrintsTheToken(t *testing.T) {
	needBashAndJq(t)
	const token = "ghs_SENTINELtokenVALUE0123456789"
	for _, job := range []string{"conformance", "publish"} {
		for _, curlOK := range []bool{true, false} {
			r := runFetchStep(t, job, 1, curlOK, map[string]string{"KIT_TOKEN": token})
			if strings.Contains(r.out, token) {
				t.Errorf("%s (curl ok=%v): output leaks the token:\n%s", job, curlOK, r.out)
			}
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
			r := runFetchStep(t, job, -1, true, env)
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
