package hostexec

// The ssh driver. It is the stdin-piped twin of the ssm driver's
// SecureString hand-off: the script (and, separately, the PEM identity)
// never touch argv, and the identity never touches disk for longer than
// the call needs it to.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runSSH writes h.Credential to a private temp file, then runs script on
// h.Target over ssh with the script delivered on the subprocess's stdin.
func runSSH(ctx context.Context, h Host, script string) (err error) {
	keyFile, err := writeIdentityFile(h.Credential)
	if err != nil {
		return fmt.Errorf("hostexec: %w", err)
	}
	// A failed removal leaves the PEM on disk, against this package's own
	// stated invariant, so it must not be silently dropped — but it must
	// also not mask a more important error the run itself already produced.
	defer func() {
		if rmErr := os.Remove(keyFile); rmErr != nil {
			if err == nil {
				err = fmt.Errorf("hostexec: ssh: remove identity file %s: %w", keyFile, rmErr)
			} else {
				fmt.Fprintf(os.Stderr, "hostexec: ssh: remove identity file %s: %v\n", keyFile, rmErr)
			}
		}
	}()

	cmd := exec.CommandContext(ctx, "ssh",
		"-i", keyFile,
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
		"-T",
		h.Target,
	)
	setCmdEnv(cmd, h.Env)
	cmd.Stdin = strings.NewReader(script)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	// A killed ssh may leave a grandchild it spawned holding the stderr
	// pipe's write end open; WaitDelay caps how long Wait keeps draining
	// that pipe after cancellation, so Run still returns promptly.
	cmd.WaitDelay = subprocessWaitDelay

	if err := cmd.Run(); err != nil {
		// A cancelled context killed the subprocess; that is why it failed,
		// not whatever exit status the kill produced.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("hostexec: ssh: %w", ctxErr)
		}
		// A leaked grandchild holding the stderr pipe open past WaitDelay
		// produces exec.ErrWaitDelay even when ssh itself exited zero; that
		// is not a failure of the run this call cares about.
		if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
			return nil
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("hostexec: ssh: %w: %s", err, msg)
		}
		return fmt.Errorf("hostexec: ssh: %w", err)
	}
	return nil
}

// writeIdentityFile writes pem to a new temp file with mode 0600 and
// returns its path. The caller is responsible for removing it once the ssh
// subprocess no longer needs it.
func writeIdentityFile(pem string) (string, error) {
	tf, err := os.CreateTemp("", "hostexec-ssh-key-*")
	if err != nil {
		return "", fmt.Errorf("create identity file: %w", err)
	}
	name := tf.Name()

	if _, err := tf.WriteString(pem); err != nil {
		tf.Close()
		os.Remove(name)
		return "", fmt.Errorf("write identity file: %w", err)
	}
	if err := tf.Close(); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("close identity file: %w", err)
	}
	// CreateTemp already requests 0600, but chmod pins it explicitly
	// rather than relying on the process's umask never being loosened.
	if err := os.Chmod(name, 0o600); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("chmod identity file: %w", err)
	}
	return name, nil
}
