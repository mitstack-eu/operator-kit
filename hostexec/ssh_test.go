package hostexec_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/hostexec"
)

func TestSSHRun_IdentityFileIsWrittenWithMode0600(t *testing.T) {
	dir, _, _, permRecord, idPathRecord := fakeSSH(t, "exit 0")
	setPath(t, dir)

	if err := hostexec.Run(context.Background(), sshHost("PEM-DATA"), "true"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	perm, err := os.ReadFile(permRecord)
	if err != nil {
		t.Fatalf("fake ssh never saw a -i flag: %v", err)
	}
	if got := strings.TrimSpace(string(perm)); got != "600" {
		t.Errorf("identity file mode = %s, want 600 — a group- or world-readable private key on disk is a leak even if the file is short-lived", got)
	}

	idPath, err := os.ReadFile(idPathRecord)
	if err != nil {
		t.Fatalf("could not read the recorded identity file path: %v", err)
	}
	if _, err := os.Stat(string(idPath)); !os.IsNotExist(err) {
		t.Errorf("identity file %s still exists after Run returned — the PEM must not outlive the call", idPath)
	}
}

func TestSSHRun_NonZeroExitReturnsAnErrorCarryingCapturedStderr(t *testing.T) {
	dir, _, _, _, _ := fakeSSH(t, `echo "Permission denied (publickey)" >&2; exit 255`)
	setPath(t, dir)

	err := hostexec.Run(context.Background(), sshHost("PEM-DATA"), "true")
	if err == nil {
		t.Fatal("expected an error when the ssh subprocess exits non-zero")
	}
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("error = %v, want it to carry the ssh subprocess's stderr so the failure is actionable", err)
	}
}

func TestSSHRunOutput_SuccessReturnsCapturedStdout(t *testing.T) {
	dir, _, _, _, _ := fakeSSH(t, `printf 'READY=0\n'`)
	setPath(t, dir)

	out, err := hostexec.RunOutput(context.Background(), sshHost("PEM-DATA"), "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "READY=0" {
		t.Errorf("RunOutput() = %q, want %q", out, "READY=0")
	}
}

func TestSSHRunOutput_NonZeroExitReturnsErrorAndNoOutput(t *testing.T) {
	dir, _, _, _, _ := fakeSSH(t, `printf 'partial output\n'; echo "Permission denied (publickey)" >&2; exit 255`)
	setPath(t, dir)

	out, err := hostexec.RunOutput(context.Background(), sshHost("PEM-DATA"), "true")
	if err == nil {
		t.Fatal("expected an error when the ssh subprocess exits non-zero")
	}
	if out != "" {
		t.Errorf("RunOutput() output = %q on a failed run, want empty — a caller must not mistake a probe's partial stdout before the failure for a completed read", out)
	}
}

func TestSSHRun_ContextCancellationIsHonoured(t *testing.T) {
	dir, _, _, _, _ := fakeSSH(t, "sleep 5")
	setPath(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	done := make(chan error, 1)
	go func() { done <- hostexec.Run(ctx, sshHost("PEM-DATA"), "true") }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run() error = %v, want it to wrap context.Canceled rather than swallow the cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return promptly after ctx was cancelled — the ssh subprocess was not killed")
	}
}
