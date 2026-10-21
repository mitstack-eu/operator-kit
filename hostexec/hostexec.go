// Package hostexec, an optional helper, runs one shell script on a host over an SSM RunShellScript document or an SSH connection, without streaming output or holding a connection.
package hostexec

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Kind selects which transport Run uses to reach the host: KindSSM through an AWS Systems Manager RunShellScript document, KindSSH over an SSH connection.
type Kind string

const (
	KindSSM Kind = "aws-ssm"
	KindSSH Kind = "ssh"
)

// Host describes where a script runs: Target is an EC2 instance id for ssm or "user@hostname" for ssh, Credential is the ssh driver's PEM private key and must never leak even on a Host reused for ssm, Region is never defaulted because a default that shifts silently strands whatever already ran under the real one, and a non-empty Env replaces the subprocess environment verbatim, so it carries everything the subprocess needs, as cloudid.Env does.
type Host struct {
	Kind       Kind
	Target     string
	Credential string
	Region     string
	Env        []string
}

// SSMPollInterval and SSMPollTimeout are variables so a test can shrink them rather than sleep, and the timeout bounds the poll when neither timeout option is set and floors the bound otherwise.
var (
	SSMPollInterval = 5 * time.Second
	SSMPollTimeout  = 10 * time.Minute
)

// Run executes script on h after validating h and opts, answering a failed ssm command with a *CommandFailed; an ssm script never carries a secret because AWS records it in CloudTrail, an ssh option is refused rather than ignored, and WithDetachedPoll lets the call outlive ctx's cancellation by up to the poll bound, SSMPollTimeout or the delivery plus execution timeouts plus one SSMPollInterval if longer.
func Run(ctx context.Context, h Host, script string, opts ...SendOption) error {
	if err := validateHost(h); err != nil {
		return err
	}
	switch h.Kind {
	case KindSSM:
		o, err := resolveSendOptions(opts)
		if err != nil {
			return err
		}
		return runSSM(ctx, h, script, o)
	case KindSSH:
		if err := refuseSendOptions(h.Kind, opts); err != nil {
			return err
		}
		return runSSH(ctx, h, script)
	default:
		return fmt.Errorf("hostexec: unrecognised host kind %q", h.Kind)
	}
}

// CommandFailed is what Run and RunOutput return when an ssm command ends Failed, Cancelled, TimedOut or Delivery Timed Out, carrying the invocation's ResponseCode and its trimmed, unscrubbed output, or in ReadErr why those could not be read.
type CommandFailed struct {
	CommandID, Status string
	ExitCode          int
	Stderr, Stdout    string
	ReadErr           error
}

func (e *CommandFailed) Error() string {
	msg := fmt.Sprintf("hostexec: ssm command %s reached terminal state %s", e.CommandID, e.Status)
	if e.ReadErr != nil {
		return msg + ", and its output could not be read: " + e.ReadErr.Error()
	}
	msg += fmt.Sprintf(" with exit code %d", e.ExitCode)
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

func (e *CommandFailed) Unwrap() error { return e.ReadErr }

// RunOutput runs script as Run does, under the same CloudTrail constraint on secrets, and returns its trimmed stdout, never beside an error: a failed ssm command's output arrives in a *CommandFailed, which a caller matches with errors.As before scrubbing, and a detached poll's read outlives ctx's cancellation by up to the same bound.
func RunOutput(ctx context.Context, h Host, script string, opts ...SendOption) (string, error) {
	if err := validateHost(h); err != nil {
		return "", err
	}
	switch h.Kind {
	case KindSSM:
		o, err := resolveSendOptions(opts)
		if err != nil {
			return "", err
		}
		return runSSMOutput(ctx, h, script, o)
	case KindSSH:
		if err := refuseSendOptions(h.Kind, opts); err != nil {
			return "", err
		}
		return runSSHOutput(ctx, h, script)
	default:
		return "", fmt.Errorf("hostexec: unrecognised host kind %q", h.Kind)
	}
}

func validateHost(h Host) error {
	if strings.TrimSpace(h.Target) == "" {
		return fmt.Errorf("hostexec: Target is required (kind %q)", h.Kind)
	}
	if strings.HasPrefix(h.Target, "-") {
		return fmt.Errorf("hostexec: Target %q begins with \"-\" — neither ssh nor the aws CLI has an end-of-options marker, so this would be parsed as an option rather than a host", h.Target)
	}
	if h.Kind == KindSSM && strings.TrimSpace(h.Region) == "" {
		return fmt.Errorf("hostexec: Region is required for the %q driver — it is never defaulted", KindSSM)
	}
	return nil
}

func setCmdEnv(cmd *exec.Cmd, env []string) {
	if len(env) == 0 {
		return
	}
	cmd.Env = env
}

// subprocessWaitDelay stops a grandchild holding a pipe's write end open from blocking Wait after its killed parent's context is done, while leaving a well-behaved subprocess time to flush.
const subprocessWaitDelay = 1 * time.Second
