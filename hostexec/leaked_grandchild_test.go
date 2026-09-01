package hostexec_test

// subprocessWaitDelay bounds how long Wait keeps draining a killed
// subprocess's pipes after a context that killed it is done — but it also
// governs a case that has nothing to do with cancellation at all: a
// subprocess that exits zero on its own, having backgrounded a grandchild
// that keeps holding the stderr pipe's write end open. exec.Cmd.Run returns
// exec.ErrWaitDelay once WaitDelay elapses in that case, even though the
// direct child already succeeded — a provisioning script that backgrounds a
// daemon does exactly this, so treating that as a failure would be a false
// negative on realistic scripts.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mitstack-eu/operator-kit/hostexec"
)

func TestSSHRun_SuccessfulExitIsNotFailedByALeakedGrandchildHoldingTheStderrPipeOpen(t *testing.T) {
	// The backgrounded subshell inherits the script's own stderr file
	// descriptor — the pipe hostexec's Stderr capture reads from — and keeps
	// it open for three seconds, well past the package's one-second
	// WaitDelay, while the direct child (this script) exits zero immediately.
	dir, _, _, _, _ := fakeSSH(t, "( sleep 3 & ); exit 0")
	setPath(t, dir)

	if err := hostexec.Run(context.Background(), sshHost("PEM-DATA"), "true"); err != nil {
		t.Errorf("Run() = %v, want nil — a leaked grandchild holding the stderr pipe open past WaitDelay must not turn a successful ssh exit into a failure", err)
	}
}

func TestSSMRun_SuccessfulSendCommandIsNotFailedByALeakedGrandchildHoldingStderrOpen(t *testing.T) {
	shrinkSSMPolling(t)
	dir := t.TempDir()
	script := `#!/usr/bin/env bash
( sleep 3 & )
if [ "${2:-}" = "send-command" ]; then
  echo cmd-1
  exit 0
fi
if [ "${2:-}" = "get-command-invocation" ]; then
  echo Success
  exit 0
fi
echo "fake aws: unrecognised subcommand: $*" >&2
exit 2
`
	if err := os.WriteFile(filepath.Join(dir, "aws"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake aws: %v", err)
	}
	setPath(t, dir)

	if err := hostexec.Run(context.Background(), ssmHost(), "true"); err != nil {
		t.Errorf("Run() = %v, want nil — a leaked grandchild holding the aws subprocess's stderr pipe open past WaitDelay must not turn a successful send-command/poll into a failure", err)
	}
}
