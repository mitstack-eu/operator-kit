// Package hostexec, an optional helper, runs one shell script on a host over an SSM RunShellScript document or an SSH connection, without streaming output or holding a connection.
package hostexec

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Kind selects which transport Run uses to reach the host.
type Kind string

const (
	// KindSSM drives the host through AWS Systems Manager RunShellScript.
	KindSSM Kind = "aws-ssm"
	// KindSSH drives the host over an SSH connection.
	KindSSH Kind = "ssh"
)

// Host describes where a script runs and how to reach it.
type Host struct {
	Kind Kind
	// Target is an EC2 instance id for ssm, or "user@hostname" for ssh.
	Target string
	// Credential is the ssh driver's PEM private key, which must never leak even when set on a reused Host for ssm.
	Credential string
	// Region is never defaulted, since a default that shifts silently strands whatever already ran under the real one.
	Region string
	// Env is used verbatim as exec.Cmd.Env when non-empty, so it must already include anything the subprocess needs, as cloudid.Env does; an empty Env inherits the ambient environment.
	Env []string
}

// SSMPollInterval is a variable so a test can shrink it rather than sleep for real.
var SSMPollInterval = 5 * time.Second

// SSMPollTimeout is the poll bound when neither timeout option is set, and the floor of the bound otherwise.
var SSMPollTimeout = 10 * time.Minute

// Run executes script on h, and an ssm caller must never embed a secret in script because AWS records it in CloudTrail; pass a reference the host resolves itself, while the ssh driver carries the script on stdin only.
//
// Host and opts are validated before anything is spawned, and any option on the ssh driver is rejected rather than ignored. With WithDetachedPoll the call can outlive ctx's cancellation by up to the poll bound, SSMPollTimeout or the delivery plus execution timeouts plus one SSMPollInterval if longer.
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

// RunOutput runs script as Run does, with the same CloudTrail constraint on secrets, and returns its stdout with only the outer whitespace trimmed, never partial output alongside an error.
//
// With WithDetachedPoll the output read runs on the detached context too, so it can outlive ctx's cancellation by up to the same poll bound.
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
