// Package hostexec runs a shell script on a single host over one of two
// transports — an AWS Systems Manager (SSM) RunShellScript document, or an
// SSH connection authenticated by a supplied private key — behind ONE call,
// so an operator's provisioning code names a Kind and a Target and never
// shells out to an aws or ssh subprocess directly (MIT-2440).
//
// Both drivers exist because the reference operators that provision a box
// reach it two different ways depending on how it is fenced: an
// SSM-managed EC2 instance has no open inbound SSH port at all, while other
// hosts — and every future non-EC2 target — are reachable only over ssh.
// Each driver had already been hand-rolled once, inside
// web-app-aws/postgres-db/provision.go's runShellOnInstance /
// pollCommandInvocation poll loop; this package is that logic lifted out so
// a second operator does not re-derive the same send-command argv, or the
// same secret-handling decision, from a blank page.
//
// The two drivers give different guarantees about a secret embedded in the
// script, so they must not be treated as interchangeable on that point. The
// ssh driver carries the script on its subprocess's stdin, so a secret
// embedded in it never appears on a command line or in a log line. The ssm
// driver instead passes the script as a SendCommand parameter, which AWS
// itself records in CloudTrail — an inherent property of the
// AWS-RunShellScript document, not a shortcut this package took. A caller
// using the ssm driver must therefore never embed a secret in the script; it
// must instead pass the script a REFERENCE it can resolve on the host once
// it is already running there — an SSM SecureString parameter name, say —
// and let the host fetch the value itself, exactly as the original
// runShellOnInstance did (web-app-aws/postgres-db/provision.go:113) by
// handing the database password over through an SSM parameter rather than
// the script text.
//
// This is deliberately not a general remote-execution library. It runs one
// script to completion and reports success or failure; it does not stream
// output, hold a persistent connection, or retry anything beyond the SSM
// poll loop's own transient-error retry. An operator that needs a live
// shell or incremental output needs a different tool.
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
	// Kind selects the driver.
	Kind Kind
	// Target is the driver-specific address: an EC2 instance id for ssm,
	// or "user@hostname" for ssh.
	Target string
	// Credential is the ssh driver's PEM private key. Unused by ssm, but
	// must never leak even when a caller populates it on a reused Host.
	Credential string
	// Region is the AWS region the ssm driver operates in. Required for
	// ssm, and never defaulted — a package default that shifts silently
	// strands whatever already ran under the real one.
	Region string
	// Env is the driver subprocess's complete process environment. A nil or
	// empty Env means the subprocess inherits the ambient environment;
	// a non-empty Env is used VERBATIM, matching exec.Cmd.Env's own
	// semantics, so it must already include anything the subprocess needs
	// (PATH, credentials, ...) — cloudid.Env returns exactly such a
	// complete environment, ready to pass here.
	Env []string
}

// SSMPollInterval is how often the ssm driver polls get-command-invocation.
// A package variable so a test can shrink it rather than sleep for real.
var SSMPollInterval = 5 * time.Second

// SSMPollTimeout is how long the ssm driver waits for a terminal status
// before giving up. A package variable for the same reason as
// SSMPollInterval.
var SSMPollTimeout = 10 * time.Minute

// Run executes script on h and returns once it reaches a terminal state.
//
// Kind, Target and (for ssm) Region are validated before anything is
// spawned: an unrecognised or empty Kind, an empty Target, or an empty
// Region on the ssm driver are all rejected up front rather than falling
// back to a default or a partially-started subprocess.
func Run(ctx context.Context, h Host, script string) error {
	if strings.TrimSpace(h.Target) == "" {
		return fmt.Errorf("hostexec: Target is required (kind %q)", h.Kind)
	}
	if strings.HasPrefix(h.Target, "-") {
		return fmt.Errorf("hostexec: Target %q begins with \"-\" — neither ssh nor the aws CLI has an end-of-options marker, so this would be parsed as an option rather than a host", h.Target)
	}
	switch h.Kind {
	case KindSSM:
		if strings.TrimSpace(h.Region) == "" {
			return fmt.Errorf("hostexec: Region is required for the %q driver — it is never defaulted", KindSSM)
		}
		return runSSM(ctx, h, script)
	case KindSSH:
		return runSSH(ctx, h, script)
	default:
		return fmt.Errorf("hostexec: unrecognised host kind %q", h.Kind)
	}
}

// setCmdEnv applies Host.Env to cmd. A nil or empty env leaves cmd.Env unset
// so the subprocess inherits the ambient environment; a non-empty env is set
// verbatim, matching exec.Cmd.Env's own semantics.
func setCmdEnv(cmd *exec.Cmd, env []string) {
	if len(env) == 0 {
		return
	}
	cmd.Env = env
}

// subprocessWaitDelay bounds how long Wait keeps draining a killed
// subprocess's stdout/stderr pipes after the context that killed it is
// done. Without it, a grandchild the subprocess spawned (a script's own
// child command, say) can keep a pipe's write end open long after the
// direct child has been killed, and Wait blocks on that pipe rather than
// returning — the context cancellation would otherwise not be "honoured"
// promptly at all. Short enough to keep cancellation prompt, long enough
// that a well-behaved subprocess is not force-closed mid-flush.
const subprocessWaitDelay = 1 * time.Second
