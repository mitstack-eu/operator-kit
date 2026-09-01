package hostexec

// The ssm driver, lifted from web-app-aws/postgres-db/provision.go's
// runShellOnInstance and pollCommandInvocation.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// runSSM sends script to h.Target as an AWS-RunShellScript document and
// waits for it to reach a terminal state.
func runSSM(ctx context.Context, h Host, script string) error {
	commandID, err := sendSSMCommand(ctx, h, script)
	if err != nil {
		return err
	}
	return waitForSSMTerminalStatus(ctx, h, commandID)
}

// sendSSMCommand issues the send-command call and returns the resulting
// CommandId. The argv order is exact — a caller depends on the document
// name, instance id and region arriving in precisely this sequence.
func sendSSMCommand(ctx context.Context, h Host, script string) (string, error) {
	commandsJSON, err := json.Marshal(strings.Split(script, "\n"))
	if err != nil {
		return "", fmt.Errorf("hostexec: ssm: encoding script as commands JSON: %w", err)
	}

	cmd := exec.CommandContext(ctx, "aws",
		"ssm", "send-command",
		"--document-name", "AWS-RunShellScript",
		"--instance-ids", h.Target,
		"--region", h.Region,
		"--query", "Command.CommandId",
		"--output", "text",
		"--parameters", "commands="+string(commandsJSON),
	)
	setCmdEnv(cmd, h.Env)
	cmd.WaitDelay = subprocessWaitDelay

	var stderr strings.Builder
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr.Write(ee.Stderr)
		}
		// A cancelled context killed the subprocess; that is why it failed,
		// not whatever exit status the kill produced.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("hostexec: ssm send-command: %w", ctxErr)
		}
		// A leaked grandchild holding the subprocess's stderr pipe open past
		// WaitDelay produces exec.ErrWaitDelay even when aws itself exited
		// zero; that is not a failure of the send this call cares about, but
		// cmd.Output may then return truncated stdout — the empty-CommandId
		// guard below still catches that case.
		if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
			commandID := strings.TrimSpace(string(out))
			if commandID == "" {
				return "", errors.New("hostexec: ssm send-command returned no CommandId")
			}
			return commandID, nil
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("hostexec: ssm send-command: %w: %s", err, msg)
		}
		return "", fmt.Errorf("hostexec: ssm send-command: %w", err)
	}
	commandID := strings.TrimSpace(string(out))
	if commandID == "" {
		return "", errors.New("hostexec: ssm send-command returned no CommandId")
	}
	return commandID, nil
}

// waitForSSMTerminalStatus polls get-command-invocation until commandID
// reaches a terminal state, returning nil only on Success. A poll
// subprocess error (the invocation not visible yet, a transient CLI
// failure) is treated as still-pending and retried, never as terminal.
func waitForSSMTerminalStatus(ctx context.Context, h Host, commandID string) error {
	var lastPollErr error
	deadline := time.Now().Add(SSMPollTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("hostexec: ssm poll: %w", ctx.Err())
		default:
		}

		status, pollErr := ssmCommandStatus(ctx, h, commandID)
		if pollErr == nil {
			switch status {
			case "Success":
				return nil
			case "Failed", "Cancelled", "TimedOut", "Delivery Timed Out":
				return fmt.Errorf("hostexec: ssm command %s reached terminal state %s", commandID, status)
			}
		} else {
			lastPollErr = pollErr
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("hostexec: ssm poll: %w", ctx.Err())
		case <-time.After(SSMPollInterval):
		}
	}
	if lastPollErr != nil {
		return fmt.Errorf("hostexec: ssm command %s timed out waiting for a terminal state after %s — last poll failure: %v", commandID, SSMPollTimeout, lastPollErr)
	}
	return fmt.Errorf("hostexec: ssm command %s timed out waiting for a terminal state after %s", commandID, SSMPollTimeout)
}

// ssmCommandStatus reads the current status of commandID. Any error from
// running the CLI — including a not-yet-visible invocation — is returned
// as-is so the caller retries rather than treats it as terminal.
func ssmCommandStatus(ctx context.Context, h Host, commandID string) (string, error) {
	cmd := exec.CommandContext(ctx, "aws",
		"ssm", "get-command-invocation",
		"--command-id", commandID,
		"--instance-id", h.Target,
		"--region", h.Region,
		"--query", "Status",
		"--output", "text",
	)
	setCmdEnv(cmd, h.Env)
	cmd.WaitDelay = subprocessWaitDelay

	out, err := cmd.Output()
	if err != nil {
		// A leaked grandchild holding this call's own stderr pipe open past
		// WaitDelay must not turn an otherwise-successful poll into a
		// spurious retry.
		if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
			return strings.TrimSpace(string(out)), nil
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if msg := strings.TrimSpace(string(ee.Stderr)); msg != "" {
				return "", fmt.Errorf("%w: %s", err, msg)
			}
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
