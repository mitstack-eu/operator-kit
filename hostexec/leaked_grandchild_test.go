package hostexec_test

// A subprocess that exits zero having backgrounded a grandchild holding the stderr pipe open makes exec.Cmd.Run return exec.ErrWaitDelay, which must not be treated as a failure since a script backgrounding a daemon does exactly this.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/hostexec"
)

func TestSSHRun_SuccessfulExitIsNotFailedByALeakedGrandchildHoldingTheStderrPipeOpen(t *testing.T) {
	// The backgrounded subshell holds the stderr pipe open three seconds, past the one-second WaitDelay, while the script exits zero at once.
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
