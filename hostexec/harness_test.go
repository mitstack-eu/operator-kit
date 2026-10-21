package hostexec_test

// Both drivers run against PATH stubs that record each call's argv and stdin separately, so a test can prove a value arrived over stdin rather than only that it is absent from argv.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	failureQuery    = "{ResponseCode: ResponseCode, StandardErrorContent: StandardErrorContent, StandardOutputContent: StandardOutputContent}"
)

func ssmHost() hostexec.Host {
	return hostexec.Host{Kind: hostexec.KindSSM, Target: fixtureInstance, Region: fixtureRegion}
}

func sshHost(credential string) hostexec.Host {
	return hostexec.Host{Kind: hostexec.KindSSH, Target: "deploy@10.0.0.5", Credential: credential}
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

// awsFake configures the aws stub: each Status poll fails pollFailures times, then serves statuses holding the last, or InProgress while held; hang and sendHangs sleep until killed, leakGrandchild holds stderr open past WaitDelay, and failureUnreadable fails the read of a failed command's details.
type awsFake struct {
	noCommandID, held, hang, sendHangs, leakGrandchild, failureUnreadable bool
	statuses                                                              []string
	pollFailures, exitCode                                                int
	stdout, stderr                                                        string
}

type fakes struct{ dir string }

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// record writes each argument NUL-terminated and ends a call with an RS, so an argument holding a newline is recovered exactly.
const record = `for a in "$@"; do printf '%%s\0' "$a" >> %[1]s-argv; done
printf '\036\0' >> %[1]s-argv
{ cat; printf '\036'; } >> %[1]s-stdin
`

// newFakes installs both stubs on PATH and shrinks the poll so a harness bug costs a fast failure, never a multi-minute hang.
func newFakes(t *testing.T, aws awsFake, sshBody string) fakes {
	t.Helper()
	setSSMPolling(t, time.Millisecond, 2*time.Second)
	f := fakes{dir: t.TempDir()}
	statuses := aws.statuses
	if len(statuses) == 0 {
		statuses = []string{"Success"}
	}
	quoted := make([]string, len(statuses))
	for i, s := range statuses {
		quoted[i] = shQuote(s)
	}
	commandID := "cmd-1"
	if aws.noCommandID {
		commandID = ""
	}
	optional := func(on bool, line string) string {
		if on {
			return line
		}
		return ""
	}
	failure, _ := json.Marshal(map[string]any{"ResponseCode": aws.exitCode, "StandardErrorContent": aws.stderr + "\n", "StandardOutputContent": aws.stdout + "\n"})
	d := shQuote(f.dir)
	awsScript := "#!/usr/bin/env bash\nset -u\n" + fmt.Sprintf(record, d+"/aws") +
		optional(aws.leakGrandchild, "( sleep 3 & )\n") + fmt.Sprintf(`case "${2:-}" in
send-command)
  %[1]s
  printf '%%s\n' %[2]s; exit 0;;
get-command-invocation)
  : > %[3]s/poll-started
  query=""; output=""; prev=""
  for a in "$@"; do [ "$prev" = "--query" ] && query="$a"; [ "$prev" = "--output" ] && output="$a"; prev="$a"; done
  if [ "$query" = "StandardOutputContent" ]; then printf '%%s\n' %[4]s; exit 0; fi
  if [ "$query" = %[8]s ] && [ "$output" = json ]; then %[9]s printf '%%s\n' %[10]s; exit 0; fi
  %[5]s
  if [ -f %[3]s/hold ]; then echo InProgress; exit 0; fi
  n=0; [ -f %[3]s/count ] && n=$(cat %[3]s/count); echo $((n+1)) > %[3]s/count
  if [ "$n" -lt %[6]d ]; then echo "fake ssm: transient poll failure" >&2; exit 1; fi
  STATUSES=(%[7]s); idx=$((n - %[6]d)); last=$((${#STATUSES[@]} - 1))
  [ "$idx" -gt "$last" ] && idx=$last
  printf '%%s\n' "${STATUSES[$idx]}"; exit 0;;
esac
echo "fake aws: unrecognised subcommand: $*" >&2
exit 2
`, optional(aws.sendHangs, "sleep 5"), shQuote(commandID), d, shQuote(aws.stdout), optional(aws.hang, "exec sleep 10"), aws.pollFailures, strings.Join(quoted, " "), shQuote(failureQuery), optional(aws.failureUnreadable, `echo "fake ssm: invocation unreadable" >&2; exit 1;`), shQuote(string(failure)))
	sshScript := "#!/usr/bin/env bash\nset -u\n" + fmt.Sprintf(record, d+"/ssh") + fmt.Sprintf(`prev=""
for a in "$@"; do
  if [ "$prev" = "-i" ]; then stat -c '%%a' "$a" > %[1]s/perm 2>/dev/null; printf '%%s' "$a" > %[1]s/idpath; fi
  prev="$a"
done
`, d) + sshBody + "\n"
	for name, script := range map[string]string{"aws": awsScript, "ssh": sshScript} {
		if err := os.WriteFile(filepath.Join(f.dir, name), []byte(script), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	if aws.held {
		if err := os.WriteFile(filepath.Join(f.dir, "hold"), nil, 0o644); err != nil {
			t.Fatalf("arming the in-progress hold: %v", err)
		}
	}
	t.Setenv("PATH", f.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

func (f fakes) path(name string) string { return filepath.Join(f.dir, name) }

// read returns a file the stubs wrote, empty when they never wrote it.
func (f fakes) read(name string) string {
	raw, _ := os.ReadFile(f.path(name))
	return string(raw)
}

// calls returns the argv of every call to tool, in order.
func (f fakes) calls(tool string) [][]string {
	var out [][]string
	var cur []string
	for _, a := range strings.Split(f.read(tool+"-argv"), "\x00") {
		switch a {
		case "\x1e":
			out, cur = append(out, cur), nil
		case "":
		default:
			cur = append(cur, a)
		}
	}
	return out
}

func (f fakes) sendArgv(t *testing.T) []string {
	t.Helper()
	for _, c := range f.calls("aws") {
		if len(c) >= 2 && c[1] == "send-command" {
			return c
		}
	}
	t.Fatal("the aws stub never recorded a send-command call")
	return nil
}

// queries returns the --query value of every get-command-invocation call, in order.
func (f fakes) queries() []string {
	var out []string
	for _, c := range f.calls("aws") {
		for i := 0; len(c) >= 2 && c[1] == "get-command-invocation" && i+1 < len(c); i++ {
			if c[i] == "--query" {
				out = append(out, c[i+1])
			}
		}
	}
	return out
}

func wantSendArgv(tail ...string) []string {
	return append([]string{"ssm", "send-command", "--document-name", "AWS-RunShellScript", "--instance-ids", fixtureInstance,
		"--region", fixtureRegion, "--query", "Command.CommandId", "--output", "text"}, tail...)
}

// setSSMPolling must not run in parallel with a test that reads the poll variables.
func setSSMPolling(t *testing.T, interval, timeout time.Duration) {
	t.Helper()
	origInterval, origTimeout := hostexec.SSMPollInterval, hostexec.SSMPollTimeout
	hostexec.SSMPollInterval, hostexec.SSMPollTimeout = interval, timeout
	t.Cleanup(func() { hostexec.SSMPollInterval, hostexec.SSMPollTimeout = origInterval, origTimeout })
}

// runInBackground waits on cleanup for call to return, so a failed test never leaves a call reading the variables setSSMPolling restores; call newFakes and setSSMPolling first so that wait runs before their cleanups.
func runInBackground(t *testing.T, f fakes, call func() error) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- call()
	}()
	t.Cleanup(func() {
		_ = os.Remove(f.path("hold"))
		select {
		case <-finished:
		case <-time.After(30 * time.Second):
			t.Error("the call under test was still running 30s after its hold was released")
		}
	})
	return done
}

// waitForPoll returns once the aws stub has answered a get-command-invocation call.
func waitForPoll(t *testing.T, f fakes, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(f.path("poll-started")); err == nil {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("the call returned (err=%v) before the poll began", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the poll to begin")
		}
		time.Sleep(2 * time.Millisecond)
	}
}
