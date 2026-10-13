package hostexec_test

// Both drivers run against a PATH shim that records argv and stdin separately, so a test can prove a value arrived over stdin rather than only that it is absent from argv.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/hostexec"
)

func setPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fakeSSM's get-command-invocation fails pollFailures times first, proving a transient poll error is retried, then serves statuses in order and holds the last.
func fakeSSM(t *testing.T, sendCommandID string, statuses []string, pollFailures int) (dir, argvRecord, stdinRecord string) {
	t.Helper()
	dir = t.TempDir()
	argvRecord = filepath.Join(dir, "argv")
	stdinRecord = filepath.Join(dir, "stdin")
	countFile := filepath.Join(dir, "count")

	quoted := make([]string, len(statuses))
	for i, s := range statuses {
		quoted[i] = shQuote(s)
	}

	script := fmt.Sprintf(`#!/usr/bin/env bash
set -u
for a in "$@"; do printf '%%s\n' "$a" >> %[1]s; done
printf -- '---\n' >> %[1]s
cat >> %[2]s
printf -- '---\n' >> %[2]s

if [ "${2:-}" = "send-command" ]; then
  printf '%%s\n' %[3]s
  exit 0
fi

if [ "${2:-}" = "get-command-invocation" ]; then
  n=0
  [ -f %[4]s ] && n=$(cat %[4]s)
  echo $((n+1)) > %[4]s
  if [ "$n" -lt %[5]d ]; then
    echo "fake ssm: transient poll failure" >&2
    exit 1
  fi
  idx=$((n - %[5]d))
  STATUSES=(%[6]s)
  last=$((${#STATUSES[@]} - 1))
  if [ "$idx" -gt "$last" ]; then idx=$last; fi
  printf '%%s\n' "${STATUSES[$idx]}"
  exit 0
fi

echo "fake aws: unrecognised subcommand: $*" >&2
exit 2
`, argvRecord, stdinRecord, shQuote(sendCommandID), countFile, pollFailures, strings.Join(quoted, " "))

	bin := filepath.Join(dir, "aws")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake aws: %v", err)
	}
	return dir, argvRecord, stdinRecord
}

func ssmHost() hostexec.Host {
	return hostexec.Host{Kind: hostexec.KindSSM, Target: "i-0123456789abcdef0", Region: "eu-central-1"}
}

// fakeSSH records the identity file's permission bits and path while it still exists, so a test can assert on both before Run cleans it up.
func fakeSSH(t *testing.T, body string) (dir, argvRecord, stdinRecord, permRecord, idPathRecord string) {
	t.Helper()
	dir = t.TempDir()
	argvRecord = filepath.Join(dir, "argv")
	stdinRecord = filepath.Join(dir, "stdin")
	permRecord = filepath.Join(dir, "perm")
	idPathRecord = filepath.Join(dir, "idpath")

	script := fmt.Sprintf(`#!/usr/bin/env bash
set -u
for a in "$@"; do printf '%%s\n' "$a" >> %[1]s; done
printf -- '---\n' >> %[1]s
cat >> %[2]s
printf -- '---\n' >> %[2]s
prev=""
for a in "$@"; do
  if [ "$prev" = "-i" ]; then
    stat -c '%%a' "$a" > %[3]s 2>/dev/null
    printf '%%s' "$a" > %[4]s
  fi
  prev="$a"
done
%[5]s
`, argvRecord, stdinRecord, permRecord, idPathRecord, body)

	bin := filepath.Join(dir, "ssh")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	return dir, argvRecord, stdinRecord, permRecord, idPathRecord
}

func sshHost(credential string) hostexec.Host {
	return hostexec.Host{Kind: hostexec.KindSSH, Target: "deploy@10.0.0.5", Credential: credential}
}

// fakeSSMOutput inspects the --query value because fakeSSM's argv-position dispatch cannot tell a Status call from a StandardOutputContent call; the latter always serves stdout without consuming a poll slot.
func fakeSSMOutput(t *testing.T, sendCommandID string, statuses []string, pollFailures int, stdout string) (dir, argvRecord, stdinRecord string) {
	t.Helper()
	dir = t.TempDir()
	argvRecord = filepath.Join(dir, "argv")
	stdinRecord = filepath.Join(dir, "stdin")
	countFile := filepath.Join(dir, "count")

	quoted := make([]string, len(statuses))
	for i, s := range statuses {
		quoted[i] = shQuote(s)
	}

	script := fmt.Sprintf(`#!/usr/bin/env bash
set -u
for a in "$@"; do printf '%%s\n' "$a" >> %[1]s; done
printf -- '---\n' >> %[1]s
cat >> %[2]s
printf -- '---\n' >> %[2]s

if [ "${2:-}" = "send-command" ]; then
  printf '%%s\n' %[3]s
  exit 0
fi

if [ "${2:-}" = "get-command-invocation" ]; then
  query=""
  prev=""
  for a in "$@"; do
    if [ "$prev" = "--query" ]; then query="$a"; fi
    prev="$a"
  done
  if [ "$query" = "StandardOutputContent" ]; then
    printf '%%s\n' %[7]s
    exit 0
  fi
  n=0
  [ -f %[4]s ] && n=$(cat %[4]s)
  echo $((n+1)) > %[4]s
  if [ "$n" -lt %[5]d ]; then
    echo "fake ssm: transient poll failure" >&2
    exit 1
  fi
  idx=$((n - %[5]d))
  STATUSES=(%[6]s)
  last=$((${#STATUSES[@]} - 1))
  if [ "$idx" -gt "$last" ]; then idx=$last; fi
  printf '%%s\n' "${STATUSES[$idx]}"
  exit 0
fi

echo "fake aws: unrecognised subcommand: $*" >&2
exit 2
`, argvRecord, stdinRecord, shQuote(sendCommandID), countFile, pollFailures, strings.Join(quoted, " "), shQuote(stdout))

	bin := filepath.Join(dir, "aws")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake aws: %v", err)
	}
	return dir, argvRecord, stdinRecord
}

func ssmQueryOrder(t *testing.T, argvRecord string) []string {
	t.Helper()
	raw, err := os.ReadFile(argvRecord)
	if err != nil {
		t.Fatalf("reading fake aws argv record: %v", err)
	}
	var queries []string
	for _, inv := range strings.Split(string(raw), "---\n") {
		lines := strings.Split(strings.TrimRight(inv, "\n"), "\n")
		if len(lines) < 2 || lines[0] != "ssm" || lines[1] != "get-command-invocation" {
			continue
		}
		for i, l := range lines {
			if l == "--query" && i+1 < len(lines) {
				queries = append(queries, lines[i+1])
				break
			}
		}
	}
	return queries
}

// fakeSSMHeld answers InProgress while holdFile exists and finalStatus once the test removes it, and creates pollStarted on every call so a test can wait until polling began.
func fakeSSMHeld(t *testing.T, sendCommandID, finalStatus, stdout string) (dir, argvRecord, pollStarted, holdFile string) {
	t.Helper()
	dir = t.TempDir()
	argvRecord = filepath.Join(dir, "argv")
	pollStarted = filepath.Join(dir, "poll-started")
	holdFile = filepath.Join(dir, "hold-in-progress")

	script := fmt.Sprintf(`#!/usr/bin/env bash
set -u
for a in "$@"; do printf '%%s\n' "$a" >> %[1]s; done
printf -- '---\n' >> %[1]s

if [ "${2:-}" = "send-command" ]; then
  printf '%%s\n' %[2]s
  exit 0
fi

if [ "${2:-}" = "get-command-invocation" ]; then
  : > %[3]s
  query=""
  prev=""
  for a in "$@"; do
    if [ "$prev" = "--query" ]; then query="$a"; fi
    prev="$a"
  done
  if [ "$query" = "StandardOutputContent" ]; then
    printf '%%s\n' %[6]s
    exit 0
  fi
  if [ -f %[7]s ]; then
    exec sleep %[8]d
  fi
  if [ -f %[4]s ]; then
    printf '%%s\n' InProgress
    exit 0
  fi
  printf '%%s\n' %[5]s
  exit 0
fi

echo "fake aws: unrecognised subcommand: $*" >&2
exit 2
`, argvRecord, shQuote(sendCommandID), pollStarted, holdFile, shQuote(finalStatus), shQuote(stdout),
		filepath.Join(dir, hangStatusMarker), int(hungStatusSleep/time.Second))

	bin := filepath.Join(dir, "aws")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake aws: %v", err)
	}
	if err := os.WriteFile(holdFile, nil, 0o644); err != nil {
		t.Fatalf("arming the in-progress hold: %v", err)
	}
	return dir, argvRecord, pollStarted, holdFile
}

// The stub execs sleep, so killing the Status call kills the sleep itself and leaves no process holding its pipes, and an unkilled call still ends after hungStatusSleep.
const (
	hangStatusMarker = "hang-status-poll"
	hungStatusSleep  = 10 * time.Second
)

func hangStatusPoll(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, hangStatusMarker), nil, 0o644); err != nil {
		t.Fatalf("arming the Status call hang: %v", err)
	}
}

// setSSMPolling must not run in parallel with a test that reads the poll variables.
func setSSMPolling(t *testing.T, interval, timeout time.Duration) {
	t.Helper()
	origInterval, origTimeout := hostexec.SSMPollInterval, hostexec.SSMPollTimeout
	hostexec.SSMPollInterval = interval
	hostexec.SSMPollTimeout = timeout
	t.Cleanup(func() {
		hostexec.SSMPollInterval = origInterval
		hostexec.SSMPollTimeout = origTimeout
	})
}

// runInBackground waits on cleanup for call to return, so a failed test never leaves a call reading the variables setSSMPolling restores; call setSSMPolling and fakeSSMHeld first so that wait runs before their cleanups.
func runInBackground(t *testing.T, holdFile string, call func() error) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- call()
	}()
	t.Cleanup(func() {
		_ = os.Remove(holdFile)
		select {
		case <-finished:
		case <-time.After(30 * time.Second):
			t.Error("the call under test was still running 30s after its hold was released")
		}
	})
	return done
}

func waitForFile(t *testing.T, path string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("the call returned (err=%v) before %s was created", err, filepath.Base(path))
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func sendCommandArgv(t *testing.T, argvRecord string) []string {
	t.Helper()
	raw, err := os.ReadFile(argvRecord)
	if err != nil {
		t.Fatalf("reading fake aws argv record: %v", err)
	}
	for _, inv := range strings.Split(string(raw), "---\n") {
		lines := strings.Split(strings.TrimRight(inv, "\n"), "\n")
		if len(lines) >= 2 && lines[0] == "ssm" && lines[1] == "send-command" {
			return lines
		}
	}
	t.Fatal("fake aws never recorded a send-command call")
	return nil
}
