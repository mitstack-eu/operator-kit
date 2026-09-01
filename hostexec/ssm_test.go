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

	"github.com/mitstack-eu/operator-kit/hostexec"
)

// --- SSM driver ----------------------------------------------------------

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
	// Two transient get-command-invocation failures before the invocation is
	// even visible, then Success: the whole call must still succeed.
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
	// A poll that never reaches a terminal state must not hang forever — the
	// deadline is shrunk here purely so the test does not take ten minutes.
	dir, _, _ := fakeSSM(t, "cmd-1", []string{"InProgress"}, 0)
	setPath(t, dir)

	origInterval, origTimeout := hostexec.SSMPollInterval, hostexec.SSMPollTimeout
	hostexec.SSMPollInterval = time.Millisecond
	hostexec.SSMPollTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		hostexec.SSMPollInterval = origInterval
		hostexec.SSMPollTimeout = origTimeout
	})

	// Run in a goroutine with a hard ceiling: if the implementation ignores
	// the shrunk deadline and falls back to the real ten minutes, this test
	// must fail fast rather than hang the suite for ten minutes.
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
	// The poll loop is right to treat a transient poll error as still-pending
	// rather than terminal (TestSSMRun_APollErrorIsRetriedRatherThanTreatedAsFailure
	// pins that), but it currently discards that error entirely rather than
	// keeping the last one around — so an operator who hit ten straight
	// minutes of AccessDenied learns nothing beyond "timed out waiting for a
	// terminal state". The last poll failure is exactly the diagnostic a
	// caller needs once the deadline is reached.
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
	// TestSSMRun_ContextCancellationIsHonoured above only cancels during the
	// poll loop, which already checks ctx.Done() itself. send-command is a
	// single blocking subprocess call with no such check around it, so a
	// cancellation while IT is running must still be attributed to ctx —
	// otherwise a killed send-command surfaces as a bare "signal: killed"
	// rather than as context.Canceled.
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

// --- commands= JSON wire format --------------------------------------------

// fakeSSMCapturingRawArgv writes a stub aws that records its argv NUL-
// separated rather than newline-separated, so a test can recover the exact
// bytes of an argv element even when that element itself contains an
// embedded newline — which is exactly what a multi-line script inside the
// --parameters commands=... value does, and what the newline-per-line
// records the other fakes in this package use cannot represent unambiguously.
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

// decodedCommandsParameter reads argvRecord, recovers the exact value of the
// --parameters commands=... argv element, and JSON-decodes it — so the
// assertion is about the wire format actually being valid, well-formed JSON
// that round-trips, rather than about matching one particular escaping
// style.
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

// shrinkSSMPolling bounds how long a test that drives Run() through a real
// poll cycle can possibly take, restoring both package variables on cleanup.
// Every test that reaches the poll loop applies this defensively, even one
// whose fake answers Success on the very first poll: a harness bug that
// leaves the poll unanswered must cost that test a fast failure, never a
// multi-minute hang of the whole suite.
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

func TestSSMRun_ScriptWithSpecialCharactersRoundTripsThroughTheCommandsJSONParameter(t *testing.T) {
	// commands is documented by AWS-RunShellScript as a list of commands —
	// the shape the original runShellOnInstance received — so the wire form
	// this driver must produce is one JSON array element per line of the
	// script, not the whole (possibly multi-line) script crammed into a
	// single JSON string, which is not valid JSON the moment it contains a
	// literal newline.
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
	// The wire-shape fix above must not disturb the single-line case the
	// exact-argv assertion in TestSSMRun_SendCommandArgvIsExactlyDocumentInstanceAndRegion
	// depends on: a script with no embedded newline is still exactly one
	// JSON array element.
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
