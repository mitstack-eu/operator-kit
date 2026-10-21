package hostexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func runSSM(ctx context.Context, h Host, script string, o sendOptions) error {
	commandID, err := sendSSMCommand(ctx, h, script, o)
	if err != nil {
		return err
	}
	bound := ssmPollBound(o)
	pollCtx, cancel := ssmPollContext(ctx, o, bound)
	defer cancel()
	return waitForSSMPoll(pollCtx, o, bound, h, commandID)
}

// runSSMOutput reads stdout with a second call only once the poll reached Success, which Run must never take, and on the poll's context so a detached poll's read also outlives cancellation.
func runSSMOutput(ctx context.Context, h Host, script string, o sendOptions) (string, error) {
	commandID, err := sendSSMCommand(ctx, h, script, o)
	if err != nil {
		return "", err
	}
	bound := ssmPollBound(o)
	pollCtx, cancel := ssmPollContext(ctx, o, bound)
	defer cancel()
	if err := waitForSSMPoll(pollCtx, o, bound, h, commandID); err != nil {
		return "", err
	}
	return readInvocation(pollCtx, h, commandID, "output", "StandardOutputContent", "text")
}

// ssmPollBound outlasts the time SSM itself may take to deliver and run the command, an unset timeout counting as zero, and reads the package variables at call time.
func ssmPollBound(o sendOptions) time.Duration {
	if !o.deliveryTimeoutSet && !o.executionTimeoutSet {
		return SSMPollTimeout
	}
	var sum time.Duration
	if o.deliveryTimeoutSet {
		sum += o.deliveryTimeout
	}
	if o.executionTimeoutSet {
		sum += o.executionTimeout
	}
	return max(SSMPollTimeout, sum+SSMPollInterval)
}

// ssmPollContext drops ctx's cancellation and deadline for a detached poll, since cancelling ctx does not stop the command already running on the host.
func ssmPollContext(ctx context.Context, o sendOptions, bound time.Duration) (context.Context, context.CancelFunc) {
	if !o.detachedPoll {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), bound)
}

// waitForSSMPoll reports the bound for a detached poll, whose context ends only when the bound runs out, rather than a bare context error.
func waitForSSMPoll(pollCtx context.Context, o sendOptions, bound time.Duration, h Host, commandID string) error {
	err := waitForSSMTerminalStatus(pollCtx, h, commandID, bound)
	if err != nil && o.detachedPoll {
		if ctxErr := pollCtx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			return fmt.Errorf("hostexec: ssm command %s timed out waiting for a terminal state after %s: %w", commandID, bound, ctxErr)
		}
	}
	return err
}

// sendSSMCommand keeps its argv order exact, since a caller depends on it: document name, instance id and region, then --comment and --timeout-seconds when set, then --parameters last with executionTimeout ahead of commands.
func sendSSMCommand(ctx context.Context, h Host, script string, o sendOptions) (string, error) {
	// The aws CLI's shorthand parser decodes only \" and \\, so the JSON must
	// carry <, > and & literally: json.Marshal's HTML escapes (\u003e and friends) would reach the host
	// as text and turn every redirect into an argument.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(strings.Split(script, "\n")); err != nil {
		return "", fmt.Errorf("hostexec: ssm: encoding script as commands JSON: %w", err)
	}
	commandsJSON := strings.TrimSuffix(buf.String(), "\n")

	args := []string{
		"ssm", "send-command",
		"--document-name", "AWS-RunShellScript",
		"--instance-ids", h.Target,
		"--region", h.Region,
		"--query", "Command.CommandId",
		"--output", "text",
	}
	if o.commentSet {
		args = append(args, "--comment", o.comment)
	}
	if o.deliveryTimeoutSet {
		args = append(args, "--timeout-seconds", seconds(o.deliveryTimeout))
	}
	params := "commands=" + commandsJSON
	if o.executionTimeoutSet {
		params = `executionTimeout=["` + seconds(o.executionTimeout) + `"],` + params
	}
	args = append(args, "--parameters", params)

	cmd := exec.CommandContext(ctx, "aws", args...)
	setCmdEnv(cmd, h.Env)
	cmd.WaitDelay = subprocessWaitDelay

	var stderr strings.Builder
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr.Write(ee.Stderr)
		}
		// A cancelled context killed the subprocess, which is why it failed.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("hostexec: ssm send-command: %w", ctxErr)
		}
		// A leaked grandchild holding stderr past WaitDelay yields exec.ErrWaitDelay even when aws exited zero, with stdout possibly truncated, which the empty-CommandId guard catches.
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

// waitForSSMTerminalStatus returns nil only on Success and retries a poll subprocess error, such as an invocation not visible yet, never treating it as terminal.
func waitForSSMTerminalStatus(ctx context.Context, h Host, commandID string, bound time.Duration) error {
	var lastPollErr error
	deadline := time.Now().Add(bound)
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
				return commandFailed(ctx, h, commandID, status)
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
		return fmt.Errorf("hostexec: ssm command %s timed out waiting for a terminal state after %s — last poll failure: %v", commandID, bound, lastPollErr)
	}
	return fmt.Errorf("hostexec: ssm command %s timed out waiting for a terminal state after %s", commandID, bound)
}

// ssmCommandStatus returns any CLI error as-is so the caller retries rather than treats it as terminal.
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
		// A leaked grandchild holding stderr past WaitDelay must not turn a successful poll into a spurious retry.
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

// failureQuery reads the exit code and both streams in one call, since each read of the invocation is another aws subprocess.
const failureQuery = "{ResponseCode: ResponseCode, StandardErrorContent: StandardErrorContent, StandardOutputContent: StandardOutputContent}"

// commandFailed reads what the box reported for a command that ended in a terminal failure, keeping the status even when that read fails.
func commandFailed(ctx context.Context, h Host, commandID, status string) error {
	failed := &CommandFailed{CommandID: commandID, Status: status}
	out, err := readInvocation(ctx, h, commandID, "failure", failureQuery, "json")
	var got struct {
		ResponseCode                                int
		StandardErrorContent, StandardOutputContent string
	}
	if err == nil {
		err = json.Unmarshal([]byte(out), &got)
	}
	if err != nil {
		failed.ReadErr = err
		return failed
	}
	failed.ExitCode = got.ResponseCode
	failed.Stderr = strings.TrimSpace(got.StandardErrorContent)
	failed.Stdout = strings.TrimSpace(got.StandardOutputContent)
	return failed
}

// readInvocation is issued only once the command reached a terminal status, never before or interleaved with the status polls.
func readInvocation(ctx context.Context, h Host, commandID, label, query, format string) (string, error) {
	cmd := exec.CommandContext(ctx, "aws",
		"ssm", "get-command-invocation",
		"--command-id", commandID,
		"--instance-id", h.Target,
		"--region", h.Region,
		"--query", query,
		"--output", format,
	)
	setCmdEnv(cmd, h.Env)
	cmd.WaitDelay = subprocessWaitDelay

	out, err := cmd.Output()
	if err != nil {
		// A leaked grandchild holding stderr past WaitDelay must not turn a successful read into a spurious failure.
		if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
			return strings.TrimSpace(string(out)), nil
		}
		// A cancelled context killed the subprocess, which is why it failed.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("hostexec: ssm get-command-invocation (%s): %w", label, ctxErr)
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if msg := strings.TrimSpace(string(ee.Stderr)); msg != "" {
				return "", fmt.Errorf("hostexec: ssm get-command-invocation (%s): %w: %s", label, err, msg)
			}
		}
		return "", fmt.Errorf("hostexec: ssm get-command-invocation (%s): %w", label, err)
	}
	return strings.TrimSpace(string(out)), nil
}
