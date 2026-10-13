package hostexec_test

// A caller passing no option gets the base send-command argv and a call that returns as soon as its context is cancelled; every option is validated before any subprocess is spawned and none is accepted on ssh.

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/hostexec"
)

const (
	fixtureInstance = "i-0fixture"
	fixtureRegion   = "eu-central-1"
	fixtureComment  = "mitstack app-runner install lane-a on i-0fixture"
	fixtureScript   = "echo hi"
	fixtureCommands = `commands=["echo hi"]`
)

func optionsHost() hostexec.Host {
	return hostexec.Host{Kind: hostexec.KindSSM, Target: fixtureInstance, Region: fixtureRegion}
}

func wantSendArgv(tail ...string) []string {
	argv := []string{
		"ssm", "send-command",
		"--document-name", "AWS-RunShellScript",
		"--instance-ids", fixtureInstance,
		"--region", fixtureRegion,
		"--query", "Command.CommandId",
		"--output", "text",
	}
	return append(argv, tail...)
}

type entryPoint struct {
	name string
	call func(ctx context.Context, h hostexec.Host, script string, opts ...hostexec.SendOption) (string, error)
}

var entryPoints = []entryPoint{
	{"Run", func(ctx context.Context, h hostexec.Host, script string, opts ...hostexec.SendOption) (string, error) {
		return "", hostexec.Run(ctx, h, script, opts...)
	}},
	{"RunOutput", hostexec.RunOutput},
}

func sendAndRecordArgv(t *testing.T, ep entryPoint, opts ...hostexec.SendOption) []string {
	t.Helper()
	shrinkSSMPolling(t)
	dir, argvRecord, _ := fakeSSMOutput(t, "cmd-fixture-1", []string{"Success"}, 0, "READY=0")
	setPath(t, dir)

	if _, err := ep.call(context.Background(), optionsHost(), fixtureScript, opts...); err != nil {
		t.Fatalf("%s() = %v, want nil", ep.name, err)
	}
	return sendCommandArgv(t, argvRecord)
}

func assertArgv(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("send-command argv =\n  %q\nwant exactly\n  %q", got, want)
	}
}

func TestSSMSendOptions_NoOptionsSendsTheBaseArgvExactly(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			got := sendAndRecordArgv(t, ep)
			assertArgv(t, got, wantSendArgv("--parameters", fixtureCommands))
		})
	}
}

func TestSSMSendOptions_AllOptionsAddCommentTimeoutAndExecutionTimeoutInOrder(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			got := sendAndRecordArgv(t, ep,
				hostexec.WithComment(fixtureComment),
				hostexec.WithDeliveryTimeout(600*time.Second),
				hostexec.WithExecutionTimeout(1800*time.Second),
				hostexec.WithDetachedPoll(),
			)
			assertArgv(t, got, wantSendArgv(
				"--comment", fixtureComment,
				"--timeout-seconds", "600",
				"--parameters", `executionTimeout=["1800"],`+fixtureCommands,
			))
		})
	}
}

func TestSSMSendOptions_ArgvOrderIsFixedWhateverOrderTheOptionsArePassedIn(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			got := sendAndRecordArgv(t, ep,
				hostexec.WithDetachedPoll(),
				hostexec.WithExecutionTimeout(1800*time.Second),
				hostexec.WithDeliveryTimeout(600*time.Second),
				hostexec.WithComment(fixtureComment),
			)
			assertArgv(t, got, wantSendArgv(
				"--comment", fixtureComment,
				"--timeout-seconds", "600",
				"--parameters", `executionTimeout=["1800"],`+fixtureCommands,
			))
		})
	}
}

func TestSSMSendOptions_EachOptionAloneAddsOnlyItsOwnArgument(t *testing.T) {
	cases := []struct {
		name string
		opt  hostexec.SendOption
		want []string
	}{
		{"comment", hostexec.WithComment(fixtureComment),
			wantSendArgv("--comment", fixtureComment, "--parameters", fixtureCommands)},
		{"delivery timeout", hostexec.WithDeliveryTimeout(600 * time.Second),
			wantSendArgv("--timeout-seconds", "600", "--parameters", fixtureCommands)},
		{"execution timeout", hostexec.WithExecutionTimeout(1800 * time.Second),
			wantSendArgv("--parameters", `executionTimeout=["1800"],`+fixtureCommands)},
		{"detached poll", hostexec.WithDetachedPoll(),
			wantSendArgv("--parameters", fixtureCommands)},
	}
	for _, ep := range entryPoints {
		for _, c := range cases {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				assertArgv(t, sendAndRecordArgv(t, ep, c.opt), c.want)
			})
		}
	}
}

func TestSSMSendOptions_BoundaryValuesAreAcceptedAndSentVerbatim(t *testing.T) {
	comment100 := strings.Repeat("c", 100)
	cases := []struct {
		name string
		opt  hostexec.SendOption
		want []string
	}{
		{"delivery timeout 30s", hostexec.WithDeliveryTimeout(30 * time.Second),
			wantSendArgv("--timeout-seconds", "30", "--parameters", fixtureCommands)},
		{"delivery timeout 2592000s", hostexec.WithDeliveryTimeout(2592000 * time.Second),
			wantSendArgv("--timeout-seconds", "2592000", "--parameters", fixtureCommands)},
		{"execution timeout 1s", hostexec.WithExecutionTimeout(1 * time.Second),
			wantSendArgv("--parameters", `executionTimeout=["1"],`+fixtureCommands)},
		{"execution timeout 172800s", hostexec.WithExecutionTimeout(172800 * time.Second),
			wantSendArgv("--parameters", `executionTimeout=["172800"],`+fixtureCommands)},
		{"comment of exactly 100 bytes", hostexec.WithComment(comment100),
			wantSendArgv("--comment", comment100, "--parameters", fixtureCommands)},
		// A comment is refused only when it begins with file://, fileb://,
		// http:// or https://; the same text later in the string is sent
		// verbatim.
		{"comment containing file:// after its start", hostexec.WithComment("mitstack install notes file://comment-fixture"),
			wantSendArgv("--comment", "mitstack install notes file://comment-fixture", "--parameters", fixtureCommands)},
	}
	for _, ep := range entryPoints {
		for _, c := range cases {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				assertArgv(t, sendAndRecordArgv(t, ep, c.opt), c.want)
			})
		}
	}
}

func TestSSMSendOptions_InvalidOptionIsRejectedBeforeAnySubprocessIsSpawned(t *testing.T) {
	cases := []struct {
		name string
		opt  hostexec.SendOption
	}{
		{"nil option", nil},
		{"empty comment", hostexec.WithComment("")},
		// SSM caps a comment at 100 bytes; truncating it would break a caller
		// that later finds its command by the exact comment.
		{"comment of 101 bytes", hostexec.WithComment(strings.Repeat("c", 101))},
		{"comment of 51 runes but 102 bytes", hostexec.WithComment(strings.Repeat("é", 51))},
		// The aws CLI has no end-of-options marker.
		{"comment beginning with a dash", hostexec.WithComment("-mitstack app-runner install lane-a")},
		// The aws CLI can load a parameter value beginning with file:// or
		// fileb:// from that file, and some versions fetch an http:// or
		// https:// URL; the kit refuses every letter case whether or not a
		// given CLI version honours it.
		{"comment beginning with file://", hostexec.WithComment("file://comment-fixture")},
		{"comment beginning with fileb://", hostexec.WithComment("fileb://comment-fixture")},
		{"comment beginning with FILEB://", hostexec.WithComment("FILEB://comment-fixture")},
		{"comment beginning with http://", hostexec.WithComment("http://comment-fixture")},
		{"comment beginning with https://", hostexec.WithComment("https://comment-fixture")},
		{"comment beginning with HTTPS://", hostexec.WithComment("HTTPS://comment-fixture")},
		{"delivery timeout of zero", hostexec.WithDeliveryTimeout(0)},
		{"negative delivery timeout", hostexec.WithDeliveryTimeout(-600 * time.Second)},
		{"delivery timeout below 30s", hostexec.WithDeliveryTimeout(29 * time.Second)},
		{"delivery timeout above 2592000s", hostexec.WithDeliveryTimeout(2592001 * time.Second)},
		{"delivery timeout not whole seconds", hostexec.WithDeliveryTimeout(30500 * time.Millisecond)},
		{"execution timeout of zero", hostexec.WithExecutionTimeout(0)},
		{"negative execution timeout", hostexec.WithExecutionTimeout(-1 * time.Second)},
		{"execution timeout below 1s", hostexec.WithExecutionTimeout(500 * time.Millisecond)},
		{"execution timeout above 172800s", hostexec.WithExecutionTimeout(172801 * time.Second)},
		{"execution timeout not whole seconds", hostexec.WithExecutionTimeout(1500 * time.Millisecond)},
	}
	for _, ep := range entryPoints {
		for _, c := range cases {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				shrinkSSMPolling(t)
				dir, argvRecord, _ := fakeSSMOutput(t, "cmd-fixture-1", []string{"Success"}, 0, "READY=0")
				setPath(t, dir)

				out, err := ep.call(context.Background(), optionsHost(), fixtureScript, c.opt)
				if err == nil {
					t.Errorf("%s() with %s = nil error, want it rejected", ep.name, c.name)
				}
				if out != "" {
					t.Errorf("%s() output = %q alongside a rejected option, want empty", ep.name, out)
				}
				if _, statErr := os.Stat(argvRecord); !os.IsNotExist(statErr) {
					t.Errorf("the aws stub was invoked despite %s; options must be validated before any subprocess is spawned", c.name)
				}
			})
		}
	}
}

func TestSSHSendOptions_AnySendOptionIsRejectedBeforeSSHIsSpawned(t *testing.T) {
	cases := []struct {
		name string
		opt  hostexec.SendOption
	}{
		{"comment", hostexec.WithComment(fixtureComment)},
		{"delivery timeout", hostexec.WithDeliveryTimeout(600 * time.Second)},
		{"execution timeout", hostexec.WithExecutionTimeout(1800 * time.Second)},
		{"detached poll", hostexec.WithDetachedPoll()},
	}
	for _, ep := range entryPoints {
		for _, c := range cases {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				dir, argvRecord, _, _, _ := fakeSSH(t, `printf 'READY=0\n'`)
				setPath(t, dir)

				out, err := ep.call(context.Background(), sshHost("PEM-DATA"), fixtureScript, c.opt)
				if err == nil {
					t.Errorf("%s() on the ssh driver with the %s option = nil error, want it rejected rather than silently ignored", ep.name, c.name)
				}
				if out != "" {
					t.Errorf("%s() output = %q alongside a rejected option, want empty", ep.name, out)
				}
				if _, statErr := os.Stat(argvRecord); !os.IsNotExist(statErr) {
					t.Errorf("the ssh stub was invoked despite the %s option; it must be rejected before any subprocess is spawned", c.name)
				}
			})
		}
	}
}

// heldWindow is how long a call with the detached poll must keep waiting on a
// held command after its context is cancelled.
const heldWindow = 1200 * time.Millisecond

func TestSSMRun_DetachedPollOutlivesCancellationUntilTheCommandSucceeds(t *testing.T) {
	setSSMPolling(t, 20*time.Millisecond, 20*time.Second)
	dir, _, pollStarted, holdFile := fakeSSMHeld(t, "cmd-fixture-1", "Success", "")
	setPath(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runInBackground(t, holdFile, func() error {
		return hostexec.Run(ctx, optionsHost(), fixtureScript, hostexec.WithDetachedPoll())
	})

	waitForFile(t, pollStarted, done)
	cancel()

	select {
	case err := <-done:
		t.Fatalf("Run returned (err=%v) right after the cancel, want it to keep polling while the sent command is still InProgress on the box", err)
	case <-time.After(heldWindow):
	}

	if err := os.Remove(holdFile); err != nil {
		t.Fatalf("releasing the in-progress hold: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() = %v once the command reached Success, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return once the held command reached Success")
	}
}

func TestSSMRun_DetachedPollReportsTheTerminalStateTheCommandEndsIn(t *testing.T) {
	setSSMPolling(t, 20*time.Millisecond, 20*time.Second)
	dir, _, pollStarted, holdFile := fakeSSMHeld(t, "cmd-fixture-1", "Failed", "")
	setPath(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runInBackground(t, holdFile, func() error {
		return hostexec.Run(ctx, optionsHost(), fixtureScript, hostexec.WithDetachedPoll())
	})

	waitForFile(t, pollStarted, done)
	cancel()

	select {
	case err := <-done:
		t.Fatalf("Run returned (err=%v) right after the cancel, want it to keep polling while the sent command is still InProgress on the box", err)
	case <-time.After(heldWindow):
	}

	if err := os.Remove(holdFile); err != nil {
		t.Fatalf("releasing the in-progress hold: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run() = nil once the command ended Failed, want an error")
		}
		if !strings.Contains(err.Error(), "Failed") {
			t.Errorf("Run() error = %v, want it to name the terminal state Failed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return once the held command ended Failed")
	}
}

func TestSSMRunOutput_DetachedPollReadsTheOutputAfterCancellation(t *testing.T) {
	setSSMPolling(t, 20*time.Millisecond, 20*time.Second)
	dir, argvRecord, pollStarted, holdFile := fakeSSMHeld(t, "cmd-fixture-1", "Success", "READY=0")
	setPath(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out string
	done := runInBackground(t, holdFile, func() error {
		var err error
		out, err = hostexec.RunOutput(ctx, optionsHost(), fixtureScript, hostexec.WithDetachedPoll())
		return err
	})

	waitForFile(t, pollStarted, done)
	cancel()

	select {
	case err := <-done:
		t.Fatalf("RunOutput returned (err=%v) right after the cancel, want it to keep polling while the sent command is still InProgress on the box", err)
	case <-time.After(heldWindow):
	}

	if err := os.Remove(holdFile); err != nil {
		t.Fatalf("releasing the in-progress hold: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunOutput() = %v once the command reached Success, want nil; the output read must run on the detached context too", err)
		}
		if out != "READY=0" {
			t.Errorf("RunOutput() = %q, want %q", out, "READY=0")
		}
		queries := ssmQueryOrder(t, argvRecord)
		if len(queries) == 0 || queries[len(queries)-1] != "StandardOutputContent" {
			t.Errorf("get-command-invocation queries = %q, want the last to be the StandardOutputContent read", queries)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunOutput did not return once the held command reached Success")
	}
}

func TestSSMRun_WithoutDetachedPollCancellationReturnsPromptly(t *testing.T) {
	optionSets := []struct {
		name string
		opts []hostexec.SendOption
	}{
		{"no options", nil},
		{"every option but the detached poll", []hostexec.SendOption{
			hostexec.WithComment(fixtureComment),
			hostexec.WithDeliveryTimeout(600 * time.Second),
			hostexec.WithExecutionTimeout(1800 * time.Second),
		}},
	}
	for _, ep := range entryPoints {
		for _, set := range optionSets {
			t.Run(ep.name+"/"+set.name, func(t *testing.T) {
				setSSMPolling(t, 20*time.Millisecond, 20*time.Second)
				dir, _, pollStarted, holdFile := fakeSSMHeld(t, "cmd-fixture-1", "Success", "READY=0")
				setPath(t, dir)

				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := runInBackground(t, holdFile, func() error {
					_, err := ep.call(ctx, optionsHost(), fixtureScript, set.opts...)
					return err
				})

				waitForFile(t, pollStarted, done)
				cancel()

				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Errorf("%s() error = %v, want it to wrap context.Canceled", ep.name, err)
					}
				case <-time.After(1 * time.Second):
					t.Fatalf("%s did not return promptly after the cancel; without WithDetachedPoll the poll must stop on the caller's cancellation", ep.name)
				}
			})
		}
	}
}

func TestSSMRun_DetachedPollIsBoundedBySSMPollTimeout(t *testing.T) {
	const pollTimeout = 1 * time.Second
	setSSMPolling(t, 20*time.Millisecond, pollTimeout)
	dir, _, pollStarted, holdFile := fakeSSMHeld(t, "cmd-fixture-1", "Success", "")
	setPath(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runInBackground(t, holdFile, func() error {
		return hostexec.Run(ctx, optionsHost(), fixtureScript, hostexec.WithDetachedPoll())
	})

	waitForFile(t, pollStarted, done)
	cancel()

	// The hold is never released: only the SSMPollTimeout bound can end the
	// call, and it cannot have run out this soon after the poll started.
	select {
	case err := <-done:
		t.Fatalf("Run returned (err=%v) well before SSMPollTimeout, want the detached poll to wait for the command until the bound runs out", err)
	case <-time.After(pollTimeout * 6 / 10):
	}

	select {
	case err := <-done:
		assertPollBoundError(t, "Run", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run with the detached poll did not return within 5s of a 1s SSMPollTimeout; the detached poll must stay bounded")
	}
}

// pollBoundPhrase is what a poll's error says once the poll bound ends it,
// attached or detached, whether the poll loop's own deadline or the poll
// context's deadline ran out first.
const pollBoundPhrase = "timed out waiting for a terminal state after"

// assertPollBoundError asserts err reports the poll bound that ended a poll,
// attached or detached, rather than a bare context error or the caller's
// cancellation.
func assertPollBoundError(t *testing.T, name string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s() = nil for a command that never reached a terminal state, want a timeout error", name)
	}
	if !strings.Contains(err.Error(), pollBoundPhrase) {
		t.Errorf("%s() error = %v, want it to contain %q", name, err, pollBoundPhrase)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("%s() error = %v wraps context.Canceled, want it to report the poll bound that ended the poll", name, err)
	}
}

func TestSSMRun_DetachedPollEndsAHungStatusCallAtSSMPollTimeout(t *testing.T) {
	// The Status call itself hangs, so the poll loop never gets back to its
	// own deadline check: only the poll context's SSMPollTimeout bound can
	// kill that call and end the wait.
	const pollTimeout = 1 * time.Second
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			setSSMPolling(t, 20*time.Millisecond, pollTimeout)
			dir, _, pollStarted, holdFile := fakeSSMHeld(t, "cmd-fixture-1", "Success", "READY=0")
			hangStatusPoll(t, dir)
			setPath(t, dir)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var out string
			done := runInBackground(t, holdFile, func() error {
				var err error
				out, err = ep.call(ctx, optionsHost(), fixtureScript, hostexec.WithDetachedPoll())
				return err
			})

			waitForFile(t, pollStarted, done)
			cancel()

			select {
			case err := <-done:
				assertPollBoundError(t, ep.name, err)
				if out != "" {
					t.Errorf("%s() output = %q alongside a timeout, want empty", ep.name, out)
				}
			case <-time.After(4 * time.Second):
				t.Fatalf("%s with the detached poll did not return within 4s of a 1s SSMPollTimeout while the Status call hung; the poll context must carry the bound", ep.name)
			}
		})
	}
}
