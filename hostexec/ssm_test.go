package hostexec_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/hostexec"
)

// The aws CLI shorthand parser decodes only \" and \\, so the commands JSON carries <, > and & literally, one array element per script line; the whole script reaches argv and CloudTrail, so it never carries a secret.
func TestSSM_SendCommandArgvIsExact(t *testing.T) {
	comment100 := strings.Repeat("c", 100)
	all := []string{"--comment", fixtureComment, "--timeout-seconds", "600", "--parameters", `executionTimeout=["1800"],` + fixtureCommands}
	cases := []struct {
		name   string
		host   func() hostexec.Host
		script string
		opts   []hostexec.SendOption
		want   []string
	}{
		{"no option sends the base document, instance and region exactly", ssmHost, fixtureScript, nil, wantSendArgv("--parameters", fixtureCommands)},
		{"every option adds comment, delivery and execution timeouts in order", ssmHost, fixtureScript, []hostexec.SendOption{hostexec.WithComment(fixtureComment),
			hostexec.WithDeliveryTimeout(600 * time.Second), hostexec.WithExecutionTimeout(1800 * time.Second), hostexec.WithDetachedPoll()}, wantSendArgv(all...)},
		{"the order is fixed whatever order the options come in", ssmHost, fixtureScript, []hostexec.SendOption{hostexec.WithDetachedPoll(),
			hostexec.WithExecutionTimeout(1800 * time.Second), hostexec.WithDeliveryTimeout(600 * time.Second), hostexec.WithComment(fixtureComment)}, wantSendArgv(all...)},
		{"a comment alone adds only itself", ssmHost, fixtureScript, []hostexec.SendOption{hostexec.WithComment(fixtureComment)},
			wantSendArgv("--comment", fixtureComment, "--parameters", fixtureCommands)},
		{"a delivery timeout alone adds only itself", ssmHost, fixtureScript, []hostexec.SendOption{hostexec.WithDeliveryTimeout(600 * time.Second)},
			wantSendArgv("--timeout-seconds", "600", "--parameters", fixtureCommands)},
		{"an execution timeout alone adds only itself", ssmHost, fixtureScript, []hostexec.SendOption{hostexec.WithExecutionTimeout(1800 * time.Second)},
			wantSendArgv("--parameters", `executionTimeout=["1800"],`+fixtureCommands)},
		{"a detached poll alone adds nothing", ssmHost, fixtureScript, []hostexec.SendOption{hostexec.WithDetachedPoll()}, wantSendArgv("--parameters", fixtureCommands)},
		{"a 30s delivery timeout is sent verbatim", ssmHost, fixtureScript, []hostexec.SendOption{hostexec.WithDeliveryTimeout(30 * time.Second)},
			wantSendArgv("--timeout-seconds", "30", "--parameters", fixtureCommands)},
		{"a 2592000s delivery timeout is sent verbatim", ssmHost, fixtureScript, []hostexec.SendOption{hostexec.WithDeliveryTimeout(2592000 * time.Second)},
			wantSendArgv("--timeout-seconds", "2592000", "--parameters", fixtureCommands)},
		{"a 1s execution timeout is sent verbatim", ssmHost, fixtureScript, []hostexec.SendOption{hostexec.WithExecutionTimeout(1 * time.Second)},
			wantSendArgv("--parameters", `executionTimeout=["1"],`+fixtureCommands)},
		{"a 172800s execution timeout is sent verbatim", ssmHost, fixtureScript, []hostexec.SendOption{hostexec.WithExecutionTimeout(172800 * time.Second)},
			wantSendArgv("--parameters", `executionTimeout=["172800"],`+fixtureCommands)},
		{"a comment of exactly 100 bytes is sent verbatim", ssmHost, fixtureScript, []hostexec.SendOption{hostexec.WithComment(comment100)},
			wantSendArgv("--comment", comment100, "--parameters", fixtureCommands)},
		{"file:// after a comment's start is sent verbatim", ssmHost, fixtureScript, []hostexec.SendOption{hostexec.WithComment("mitstack install notes file://comment-fixture")},
			wantSendArgv("--comment", "mitstack install notes file://comment-fixture", "--parameters", fixtureCommands)},
		{"a multi-line script is one commands element per line", ssmHost, "set -e\napt-get update\napt-get install -y postgresql", nil,
			wantSendArgv("--parameters", `commands=["set -e","apt-get update","apt-get install -y postgresql"]`)},
		{"a tab is JSON-escaped", ssmHost, "printf 'a\tb\n'", nil, wantSendArgv("--parameters", `commands=["printf 'a\tb","'"]`)},
		{"a double quote and a backslash keep the two escapes the CLI decodes", ssmHost, `echo "hello world"` + "\n" + `echo C:\Users\deploy`, nil,
			wantSendArgv("--parameters", `commands=["echo \"hello world\"","echo C:\\Users\\deploy"]`)},
		{"redirects and ampersands travel literally, never HTML-escaped", ssmHost, "docker exec x pg_isready >/tmp/o 2>&1 && echo ok\nline with < and &", nil,
			wantSendArgv("--parameters", `commands=["docker exec x pg_isready >/tmp/o 2>&1 && echo ok","line with < and &"]`)},
		{"the script reaches argv while a reused host's credential never does", func() hostexec.Host {
			h := ssmHost()
			h.Credential = "PEM-KEY-THAT-MUST-NEVER-SURFACE"
			return h
		}, "echo this-script-text-must-appear-verbatim-in-argv", nil, wantSendArgv("--parameters", `commands=["echo this-script-text-must-appear-verbatim-in-argv"]`)},
	}
	for _, ep := range entryPoints {
		for _, tc := range cases {
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				f := newFakes(t, awsFake{stdout: "READY=0"}, "")

				_, err := ep.call(context.Background(), tc.host(), tc.script, tc.opts...)

				if err != nil {
					t.Fatalf("%s() = %v, want nil", ep.name, err)
				}
				if got := f.sendArgv(t); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("send-command argv =\n  %q\nwant exactly\n  %q", got, tc.want)
				}
			})
		}
	}
}

func TestSSM_PollOutcome(t *testing.T) {
	type outcome struct {
		name        string
		entry       string
		fake        awsFake
		pollTimeout time.Duration
		wantOut     string
		errHas      string
		wantQueries []string
	}
	cases := []outcome{
		{"Run returns nil once the invocation reaches Success", "Run", awsFake{}, 0, "", "", []string{"Status"}},
		{"RunOutput returns the captured stdout", "RunOutput", awsFake{stdout: "READY=0"}, 0, "READY=0", "", []string{"Status", "StandardOutputContent"}},
		{"RunOutput keeps a multi-line stdout's internal newline", "RunOutput", awsFake{stdout: "READY=0\npg_isready output line"}, 0, "READY=0\npg_isready output line", "", nil},
		{"RunOutput reads the output only after the Status polls settle", "RunOutput", awsFake{statuses: []string{"InProgress", "InProgress", "Success"}, stdout: "READY=0"}, 0, "READY=0", "",
			[]string{"Status", "Status", "Status", "StandardOutputContent"}},
		{"a poll error is retried as still pending", "Run", awsFake{pollFailures: 2}, 0, "", "", []string{"Status", "Status", "Status"}},
		{"a successful send and poll are not failed by a grandchild holding stderr open", "Run", awsFake{leakGrandchild: true}, 0, "", "", nil},
		{"Run fails fast when send-command returns no CommandId", "Run", awsFake{noCommandID: true}, 0, "", "returned no CommandId", []string{}},
		{"RunOutput fails with no output when send-command returns no CommandId", "RunOutput", awsFake{noCommandID: true, stdout: "should never be read"}, 0, "", "returned no CommandId", []string{}},
		{"Run times out once the poll bound passes with no terminal status", "Run", awsFake{statuses: []string{"InProgress"}}, 20 * time.Millisecond, "", "timed out", nil},
		{"RunOutput times out with no output", "RunOutput", awsFake{statuses: []string{"InProgress"}, stdout: "should never be read"}, 20 * time.Millisecond, "", "timed out", nil},
		{"a timeout names the last poll failure, the diagnostic a caller needs", "Run", awsFake{pollFailures: 1_000_000}, 20 * time.Millisecond, "", "fake ssm: transient poll failure", nil},
	}
	for _, status := range []string{"Failed", "Cancelled", "TimedOut", "Delivery Timed Out"} {
		for _, entry := range []string{"Run", "RunOutput"} {
			cases = append(cases, outcome{entry + " names the terminal status " + status + " with no output", entry, awsFake{statuses: []string{status}, stdout: "should never be read"}, 0, "", status, []string{"Status", failureQuery}})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakes(t, tc.fake, "")
			if tc.pollTimeout != 0 {
				setSSMPolling(t, time.Millisecond, tc.pollTimeout)
			}
			ep := entryPoints[map[string]int{"Run": 0, "RunOutput": 1}[tc.entry]]

			out, err := ep.call(context.Background(), ssmHost(), "true")

			if out != tc.wantOut || (tc.errHas == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.errHas)) {
				t.Errorf("%s() = %q, %v; want %q with an error saying %q", tc.entry, out, err, tc.wantOut, tc.errHas)
			}
			if got := f.queries(); tc.wantQueries != nil && !slices.Equal(got, tc.wantQueries) {
				t.Errorf("get-command-invocation queries = %q, want %q", got, tc.wantQueries)
			}
		})
	}
}

func TestSSM_ATerminalFailureIsACommandFailedCarryingWhatTheBoxReported(t *testing.T) {
	const daemonDown = "docker: Cannot connect to the Docker daemon at unix:///var/run/docker.sock"
	cases := []struct {
		fake awsFake
		want hostexec.CommandFailed
	}{
		{awsFake{statuses: []string{"Failed"}, exitCode: 125, stderr: daemonDown, stdout: "pulling"},
			hostexec.CommandFailed{CommandID: "cmd-1", Status: "Failed", ExitCode: 125, Stderr: daemonDown, Stdout: "pulling"}},
		{awsFake{statuses: []string{"Cancelled"}, exitCode: -1}, hostexec.CommandFailed{CommandID: "cmd-1", Status: "Cancelled", ExitCode: -1}},
		{awsFake{statuses: []string{"TimedOut"}, exitCode: 124, stderr: "slow"}, hostexec.CommandFailed{CommandID: "cmd-1", Status: "TimedOut", ExitCode: 124, Stderr: "slow"}},
		{awsFake{statuses: []string{"Delivery Timed Out"}, exitCode: -1}, hostexec.CommandFailed{CommandID: "cmd-1", Status: "Delivery Timed Out", ExitCode: -1}},
	}
	for _, ep := range entryPoints {
		for _, tc := range cases {
			t.Run(ep.name+"/"+tc.want.Status, func(t *testing.T) {
				newFakes(t, tc.fake, "")

				out, err := ep.call(context.Background(), ssmHost(), "true")

				var failed *hostexec.CommandFailed
				if out != "" || !errors.As(err, &failed) || *failed != tc.want {
					t.Fatalf("%s() = %q, %#v; want no output and the error %#v", ep.name, out, err, tc.want)
				}
				for _, part := range []string{"cmd-1", tc.want.Status, tc.want.Stderr} {
					if !strings.Contains(err.Error(), part) {
						t.Errorf("Error() = %q, want it to carry %q", err.Error(), part)
					}
				}
			})
		}
	}
}

// An unreadable failure still names the command and its status, and says why its output is missing.
func TestSSM_AFailureWhoseDetailsCannotBeReadStillNamesItsStatus(t *testing.T) {
	newFakes(t, awsFake{statuses: []string{"Failed"}, failureUnreadable: true}, "")

	err := hostexec.Run(context.Background(), ssmHost(), "true")

	var failed *hostexec.CommandFailed
	if !errors.As(err, &failed) || failed.Status != "Failed" || failed.ReadErr == nil || !strings.Contains(err.Error(), "invocation unreadable") {
		t.Errorf("Run() = %v; want a CommandFailed naming Failed whose ReadErr carries the read failure", err)
	}
}

// A cancellation kills the subprocess, so the error is attributed to the context rather than to the exit status the kill produced.
func TestRun_HonoursContextCancellation(t *testing.T) {
	cases := []struct {
		name    string
		fake    awsFake
		sshBody string
		host    hostexec.Host
	}{
		{"an ssm poll watches ctx rather than waiting out its interval", awsFake{statuses: []string{"InProgress"}}, "", ssmHost()},
		{"an ssm send-command in flight is killed", awsFake{sendHangs: true}, "", ssmHost()},
		{"an ssh subprocess is killed", awsFake{}, "sleep 5", sshHost("PEM-DATA")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newFakes(t, tc.fake, tc.sshBody)
			setSSMPolling(t, time.Minute, 10*time.Minute)
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(50*time.Millisecond, cancel)
			done := make(chan error, 1)

			go func() { done <- hostexec.Run(ctx, tc.host, "true") }()

			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("Run() error = %v, want it to wrap context.Canceled", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Run did not return promptly after ctx was cancelled")
			}
		})
	}
}
