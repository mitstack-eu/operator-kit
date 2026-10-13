package hostexec_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/hostexec"
)

func TestSSMRun_SendCommandArgvIsExactlyDocumentInstanceAndRegion(t *testing.T) {
	dir, argvRecord, _ := fakeSSM(t, "cmd-1", []string{"Success"}, 0)
	setPath(t, dir)

	if err := hostexec.Run(context.Background(), ssmHost(), "echo hi"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec, err := os.ReadFile(argvRecord)
	if err != nil {
		t.Fatalf("reading fake aws argv record: %v", err)
	}
	invocations := strings.Split(string(rec), "---\n")
	if len(invocations) < 1 {
		t.Fatalf("fake aws was never invoked")
	}
	want := "ssm\nsend-command\n--document-name\nAWS-RunShellScript\n--instance-ids\ni-0123456789abcdef0\n--region\neu-central-1\n--query\nCommand.CommandId\n--output\ntext\n--parameters\ncommands=[\"echo hi\"]\n"
	if invocations[0] != want {
		t.Errorf("send-command argv = %q, want exactly %q — the ssm driver must issue precisely the document, instance and region the task drives, in order", invocations[0], want)
	}
}

func TestSSMRun_SuccessPollReturnsNil(t *testing.T) {
	dir, _, _ := fakeSSM(t, "cmd-1", []string{"Success"}, 0)
	setPath(t, dir)

	if err := hostexec.Run(context.Background(), ssmHost(), "true"); err != nil {
		t.Errorf("Run() = %v, want nil once the invocation reaches Success", err)
	}
}

func TestSSMRun_EachTerminalFailureStatusNamesItselfInTheError(t *testing.T) {
	for _, status := range []string{"Failed", "Cancelled", "TimedOut", "Delivery Timed Out"} {
		t.Run(status, func(t *testing.T) {
			dir, _, _ := fakeSSM(t, "cmd-1", []string{status}, 0)
			setPath(t, dir)

			err := hostexec.Run(context.Background(), ssmHost(), "true")
			if err == nil {
				t.Fatalf("Run() succeeded on terminal status %q, want an error", status)
			}
			if !strings.Contains(err.Error(), status) {
				t.Errorf("error = %v, want it to name the terminal status %q", err, status)
			}
		})
	}
}

func TestSSMRun_EmptyCommandIdIsAnError(t *testing.T) {
	dir, _, _ := fakeSSM(t, "", []string{"Success"}, 0)
	setPath(t, dir)

	err := hostexec.Run(context.Background(), ssmHost(), "true")
	if err == nil {
		t.Fatal("Run must fail when send-command returns no CommandId — polling an unknown invocation is worse than failing fast")
	}
}

func TestSSMRun_APollErrorIsRetriedRatherThanTreatedAsFailure(t *testing.T) {
	dir, _, _ := fakeSSM(t, "cmd-1", []string{"Success"}, 2)
	setPath(t, dir)

	orig := hostexec.SSMPollInterval
	hostexec.SSMPollInterval = time.Millisecond
	t.Cleanup(func() { hostexec.SSMPollInterval = orig })

	if err := hostexec.Run(context.Background(), ssmHost(), "true"); err != nil {
		t.Errorf("Run() = %v, want nil — a poll error must be treated as still-pending, not a terminal failure", err)
	}
}

func TestSSMRun_ContextCancellationIsHonoured(t *testing.T) {
	dir, _, _ := fakeSSM(t, "cmd-1", []string{"InProgress"}, 0)
	setPath(t, dir)

	orig := hostexec.SSMPollInterval
	hostexec.SSMPollInterval = time.Minute
	t.Cleanup(func() { hostexec.SSMPollInterval = orig })

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	done := make(chan error, 1)
	go func() { done <- hostexec.Run(ctx, ssmHost(), "true") }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run() error = %v, want it to wrap context.Canceled rather than swallow the cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return promptly after ctx was cancelled — it is waiting out the poll interval instead of watching ctx.Done()")
	}
}

func TestSSMRun_DeadlineProducesATimeoutError(t *testing.T) {
	dir, _, _ := fakeSSM(t, "cmd-1", []string{"InProgress"}, 0)
	setPath(t, dir)

	origInterval, origTimeout := hostexec.SSMPollInterval, hostexec.SSMPollTimeout
	hostexec.SSMPollInterval = time.Millisecond
	hostexec.SSMPollTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		hostexec.SSMPollInterval = origInterval
		hostexec.SSMPollTimeout = origTimeout
	})

	// A hard ceiling makes the test fail fast if the implementation ignores the shrunk deadline.
	done := make(chan error, 1)
	go func() { done <- hostexec.Run(context.Background(), ssmHost(), "true") }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run must return a timeout error once the poll deadline passes without a terminal status")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "timed out") && !strings.Contains(strings.ToLower(err.Error()), "timeout") {
			t.Errorf("error = %v, want it to say the poll timed out", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within the shrunk SSMPollTimeout — it must read the deadline from the variable, not a hardcoded ten minutes")
	}
}

func TestSSMRun_TimeoutErrorNamesTheLastPollFailure(t *testing.T) {
	// The last poll failure is the diagnostic a caller needs once the deadline is reached, such as ten minutes of AccessDenied.
	dir, _, _ := fakeSSM(t, "cmd-1", []string{"Success"}, 1_000_000)
	setPath(t, dir)

	origInterval, origTimeout := hostexec.SSMPollInterval, hostexec.SSMPollTimeout
	hostexec.SSMPollInterval = time.Millisecond
	hostexec.SSMPollTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		hostexec.SSMPollInterval = origInterval
		hostexec.SSMPollTimeout = origTimeout
	})

	err := hostexec.Run(context.Background(), ssmHost(), "true")
	if err == nil {
		t.Fatal("Run must return a timeout error once the poll deadline passes without a terminal status")
	}
	if !strings.Contains(err.Error(), "fake ssm: transient poll failure") {
		t.Errorf("timeout error = %v, want it to name the last poll failure rather than discarding it", err)
	}
}

func TestSSMRun_ContextCancelledDuringSendCommandIsAttributedToTheContext(t *testing.T) {
	// send-command is a single blocking subprocess call with no ctx check around it, so a cancellation while it runs must be attributed to ctx rather than surface as a bare "signal: killed".
	dir := t.TempDir()
	sleepyAWS := "#!/usr/bin/env bash\nsleep 5\n"
	if err := os.WriteFile(filepath.Join(dir, "aws"), []byte(sleepyAWS), 0o755); err != nil {
		t.Fatalf("write fake aws: %v", err)
	}
	setPath(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	done := make(chan error, 1)
	go func() { done <- hostexec.Run(ctx, ssmHost(), "true") }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run() error = %v, want it to wrap context.Canceled — a cancellation mid send-command must not surface as whatever exit status the kill produced", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return promptly after ctx was cancelled during send-command — the aws subprocess was not killed")
	}
}

// fakeSSMCapturingRawArgv records argv NUL-separated so an element holding an embedded newline, as a multi-line script does, is recovered exactly.
func fakeSSMCapturingRawArgv(t *testing.T, sendCommandID string) (dir, argvRecord string) {
	t.Helper()
	dir = t.TempDir()
	argvRecord = filepath.Join(dir, "argv")

	script := fmt.Sprintf(`#!/usr/bin/env bash
set -u
for a in "$@"; do printf '%%s\0' "$a" >> %[1]s; done
if [ "${2:-}" = "send-command" ]; then
  printf '%%s\n' %[2]s
  exit 0
fi
if [ "${2:-}" = "get-command-invocation" ]; then
  printf '%%s\n' Success
  exit 0
fi
echo "fake aws: unrecognised subcommand: $*" >&2
exit 2
`, argvRecord, shQuote(sendCommandID))

	bin := filepath.Join(dir, "aws")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake aws: %v", err)
	}
	return dir, argvRecord
}

func decodedCommandsParameter(t *testing.T, argvRecord string) []string {
	t.Helper()
	raw, err := os.ReadFile(argvRecord)
	if err != nil {
		t.Fatalf("reading fake aws argv record: %v", err)
	}
	elems := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	for _, e := range elems {
		if strings.HasPrefix(e, "commands=") {
			value := strings.TrimPrefix(e, "commands=")
			var commands []string
			if err := json.Unmarshal([]byte(value), &commands); err != nil {
				t.Fatalf("--parameters commands=... value is not valid JSON: %v\nvalue: %q", err, value)
			}
			return commands
		}
	}
	t.Fatal("fake aws argv never carried a commands= parameter")
	return nil
}

// rawCommandsParameterValue returns the undecoded --parameters commands=...
// value exactly as it appears in argv, so a test can inspect the JSON text
// itself — whether it carries a literal '>' or its HTML-escaped form
// (the six characters backslash, u, 0, 0, 3, e) — rather than only what
// json.Unmarshal reverses back out of either form.
func rawCommandsParameterValue(t *testing.T, argvRecord string) string {
	t.Helper()
	raw, err := os.ReadFile(argvRecord)
	if err != nil {
		t.Fatalf("reading fake aws argv record: %v", err)
	}
	elems := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	for _, e := range elems {
		if strings.HasPrefix(e, "commands=") {
			return strings.TrimPrefix(e, "commands=")
		}
	}
	t.Fatal("fake aws argv never carried a commands= parameter")
	return ""
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// shrinkSSMPolling is applied even by a test whose fake answers Success at once, so a harness bug that leaves the poll unanswered costs a fast failure, never a multi-minute hang.
func shrinkSSMPolling(t *testing.T) {
	t.Helper()
	origInterval, origTimeout := hostexec.SSMPollInterval, hostexec.SSMPollTimeout
	hostexec.SSMPollInterval = time.Millisecond
	hostexec.SSMPollTimeout = 2 * time.Second
	t.Cleanup(func() {
		hostexec.SSMPollInterval = origInterval
		hostexec.SSMPollTimeout = origTimeout
	})
}

func TestSSMRunOutput_SuccessReturnsCapturedStdout(t *testing.T) {
	shrinkSSMPolling(t)
	dir, _, _ := fakeSSMOutput(t, "cmd-1", []string{"Success"}, 0, "READY=0")
	setPath(t, dir)

	out, err := hostexec.RunOutput(context.Background(), ssmHost(), "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "READY=0" {
		t.Errorf("RunOutput() = %q, want %q", out, "READY=0")
	}
}

func TestSSMRunOutput_MultilineStdoutIsPreservedVerbatim(t *testing.T) {
	// A probe's stdout can span multiple lines, so trimming at the edges must not collapse the internal newline.
	shrinkSSMPolling(t)
	want := "READY=0\npg_isready output line"
	dir, _, _ := fakeSSMOutput(t, "cmd-1", []string{"Success"}, 0, want)
	setPath(t, dir)

	out, err := hostexec.RunOutput(context.Background(), ssmHost(), "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != want {
		t.Errorf("RunOutput() = %q, want %q — internal newlines must round-trip intact", out, want)
	}
}

func TestSSMRunOutput_OutputQueryFollowsTerminalStatus(t *testing.T) {
	// The output comes from a second get-command-invocation call issued only after a terminal status, never interleaved with the Status polls.
	shrinkSSMPolling(t)
	dir, argvRecord, _ := fakeSSMOutput(t, "cmd-1", []string{"InProgress", "InProgress", "Success"}, 0, "READY=0")
	setPath(t, dir)

	out, err := hostexec.RunOutput(context.Background(), ssmHost(), "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "READY=0" {
		t.Errorf("RunOutput() = %q, want %q", out, "READY=0")
	}

	queries := ssmQueryOrder(t, argvRecord)
	if len(queries) < 2 {
		t.Fatalf("get-command-invocation was called %d time(s), want at least 2 (a Status poll, then a StandardOutputContent read)", len(queries))
	}
	last := len(queries) - 1
	if queries[last] != "StandardOutputContent" {
		t.Errorf("last get-command-invocation query = %q, want %q — the output must be read only after the poll settles", queries[last], "StandardOutputContent")
	}
	for _, q := range queries[:last] {
		if q != "Status" {
			t.Errorf("query %q appears before the terminal StandardOutputContent read, want every earlier get-command-invocation call to be a Status poll", q)
		}
	}
}

func TestSSMRunOutput_FailedSendCommandReturnsErrorAndNoOutput(t *testing.T) {
	shrinkSSMPolling(t)
	dir, _, _ := fakeSSMOutput(t, "", []string{"Success"}, 0, "should never be read")
	setPath(t, dir)

	out, err := hostexec.RunOutput(context.Background(), ssmHost(), "true")
	if err == nil {
		t.Fatal("RunOutput must fail when send-command returns no CommandId — polling and reading an unknown invocation is worse than failing fast")
	}
	if out != "" {
		t.Errorf("RunOutput() output = %q on a failed send, want empty — an unreadable host must not be confused with one whose output happens to be the zero value", out)
	}
}

func TestSSMRunOutput_PollTimeoutReturnsErrorAndNoOutput(t *testing.T) {
	dir, _, _ := fakeSSMOutput(t, "cmd-1", []string{"InProgress"}, 0, "should never be read")
	setPath(t, dir)

	origInterval, origTimeout := hostexec.SSMPollInterval, hostexec.SSMPollTimeout
	hostexec.SSMPollInterval = time.Millisecond
	hostexec.SSMPollTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		hostexec.SSMPollInterval = origInterval
		hostexec.SSMPollTimeout = origTimeout
	})

	out, err := hostexec.RunOutput(context.Background(), ssmHost(), "true")
	if err == nil {
		t.Fatal("RunOutput must fail once the poll deadline passes without a terminal status")
	}
	if out != "" {
		t.Errorf("RunOutput() output = %q on a poll timeout, want empty", out)
	}
}

func TestSSMRunOutput_TerminalFailureStatusReturnsErrorAndNoOutput(t *testing.T) {
	for _, status := range []string{"Failed", "Cancelled", "TimedOut", "Delivery Timed Out"} {
		t.Run(status, func(t *testing.T) {
			shrinkSSMPolling(t)
			dir, _, _ := fakeSSMOutput(t, "cmd-1", []string{status}, 0, "should never be read")
			setPath(t, dir)

			out, err := hostexec.RunOutput(context.Background(), ssmHost(), "true")
			if err == nil {
				t.Fatalf("RunOutput() succeeded on terminal status %q, want an error", status)
			}
			if !strings.Contains(err.Error(), status) {
				t.Errorf("error = %v, want it to name the terminal status %q", err, status)
			}
			if out != "" {
				t.Errorf("RunOutput() output = %q on terminal status %q, want empty", out, status)
			}
		})
	}
}

func TestSSMRun_ScriptWithSpecialCharactersRoundTripsThroughTheCommandsJSONParameter(t *testing.T) {
	// AWS-RunShellScript documents commands as a list, so the wire form is one JSON array element per script line.
	cases := map[string]string{
		"multi-line script":              "set -e\napt-get update\napt-get install -y postgresql",
		"line containing a tab":          "printf 'a\tb\n'",
		"line containing a double quote": `echo "hello world"`,
		"line containing a backslash":    `echo C:\Users\deploy`,
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			shrinkSSMPolling(t)
			dir, argvRecord := fakeSSMCapturingRawArgv(t, "cmd-1")
			setPath(t, dir)

			if err := hostexec.Run(context.Background(), ssmHost(), script); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got := decodedCommandsParameter(t, argvRecord)
			want := strings.Split(script, "\n")
			if !stringSlicesEqual(got, want) {
				t.Errorf("decoded commands = %#v, want %#v — the script must round-trip through the commands JSON parameter one array element per line", got, want)
			}
		})
	}
}

func TestSSMRun_SingleLineScriptCommandsParameterIsUnchanged(t *testing.T) {
	// A script with no embedded newline is still exactly one JSON array element, as the exact-argv assertion depends on.
	shrinkSSMPolling(t)
	dir, argvRecord := fakeSSMCapturingRawArgv(t, "cmd-1")
	setPath(t, dir)

	if err := hostexec.Run(context.Background(), ssmHost(), "echo hi"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := decodedCommandsParameter(t, argvRecord)
	want := []string{"echo hi"}
	if !stringSlicesEqual(got, want) {
		t.Errorf("decoded commands = %#v, want %#v for a single-line script", got, want)
	}
}
