// Package registryauth logs a container-registry CLI in without ever putting
// the credential where a process listing or a log line could catch it
// (MIT-2444).
//
// mitstack's reference operators pull their own images from a container
// registry before they can run, and two shapes cover every registry they need:
// AWS ECR, authenticated through the AWS CLI's minted password, and a plain
// username/password (or token-as-password) OCI registry such as ghcr.io. Both
// end at `docker login`, and both share the one property that matters: the
// credential travels over the child process's stdin, never as a `--password`
// argument, because argv is visible to anything running `ps` and a log line
// outlives the process that wrote it.
//
// The package is deliberately narrow. It does not manage a docker config file,
// does not cache a login across calls, and does not know about any registry
// shape beyond these two — a caller with a third kind gets a named error, not
// a guess.
package registryauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// subprocessWaitDelay bounds how long Wait keeps draining a killed
// subprocess's stdout/stderr pipes after the context that killed it is done.
// Without it, a grandchild the subprocess spawned can keep a pipe's write end
// open long after the subprocess itself has exited, and Wait blocks on it
// rather than returning promptly. Short enough to keep cancellation prompt,
// long enough that a well-behaved subprocess is not force-closed mid-flush.
const subprocessWaitDelay = 1 * time.Second

// Kind selects which login driver Login uses.
type Kind string

const (
	// KindECR authenticates against AWS ECR via the AWS CLI's minted password.
	KindECR Kind = "ecr"
	// KindOCIBasic authenticates against a plain OCI registry with a
	// username and a password or token.
	KindOCIBasic Kind = "oci-basic"
)

// Registry describes one registry login.
type Registry struct {
	// Kind selects the login driver.
	Kind Kind
	// Host is the registry host docker login authenticates against, e.g.
	// <acct>.dkr.ecr.eu-central-1.amazonaws.com or ghcr.io.
	Host string
	// Username is used by KindOCIBasic only. KindECR always logs in as the
	// literal AWS.
	Username string
	// Credential is the password or token for KindOCIBasic. For KindECR it
	// must be empty — an ecr login takes no caller-supplied credential.
	Credential string
	// Region is the AWS region get-login-password runs against. Required
	// for KindECR only.
	Region string
	// Env is the process environment for the drivers' subprocesses. A nil or
	// empty Env means the subprocesses inherit the ambient environment.
	Env []string
}

// Login authenticates the local docker CLI against r.Host, dispatching to the
// driver named by r.Kind. Every rejection below is checked before any
// subprocess is spawned, so a caller can rely on a failed Login having run
// nothing.
func Login(ctx context.Context, r Registry) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("registryauth: context already done: %w", err)
	}
	if strings.TrimSpace(r.Host) == "" {
		return errors.New("registryauth: Host is required")
	}
	if strings.HasPrefix(r.Host, "-") {
		return fmt.Errorf("registryauth: Host %q begins with \"-\" — docker login has no end-of-options marker, so this would be parsed as an option rather than a host", r.Host)
	}

	switch r.Kind {
	case KindECR:
		return loginECR(ctx, r)
	case KindOCIBasic:
		return loginOCIBasic(ctx, r)
	default:
		return fmt.Errorf("registryauth: unrecognised Kind %q", string(r.Kind))
	}
}

// loginECR runs `aws ecr get-login-password` and pipes its stdout into
// `docker login`'s stdin, so the minted password never appears in an argument
// list or a returned error.
func loginECR(ctx context.Context, r Registry) error {
	if strings.TrimSpace(r.Region) == "" {
		return errors.New("registryauth: ecr Region is required")
	}
	if r.Credential != "" {
		return errors.New("registryauth: ecr takes no caller Credential — the instance role supplies it")
	}

	aws := exec.CommandContext(ctx, "aws", "ecr", "get-login-password", "--region", r.Region)
	applyEnv(aws, r.Env)
	var awsErr bytes.Buffer
	aws.Stderr = &awsErr
	// A killed aws may leave a grandchild it spawned holding the stderr
	// pipe's write end open; WaitDelay caps how long Wait keeps draining
	// that pipe after cancellation, so this call still returns promptly.
	aws.WaitDelay = subprocessWaitDelay
	password, err := aws.Output()
	if err != nil {
		// A cancelled context killed the subprocess; that is why it failed,
		// not whatever exit status the kill produced.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("registryauth: aws ecr get-login-password: %w", ctxErr)
		}
		// A leaked grandchild holding the stderr pipe open past WaitDelay
		// produces exec.ErrWaitDelay even when aws itself exited zero; that
		// is not a failure of the login this call cares about. Output() may
		// still be truncated by the cut-short drain, so the empty-password
		// check below is what catches a truncation that matters.
		waitDelayOnly := errors.Is(err, exec.ErrWaitDelay) && aws.ProcessState != nil && aws.ProcessState.Success()
		if !waitDelayOnly {
			stderr := strings.TrimSpace(awsErr.String())
			if stderr != "" {
				return fmt.Errorf("registryauth: aws ecr get-login-password: %w: %s", err, stderr)
			}
			return fmt.Errorf("registryauth: aws ecr get-login-password: %w", err)
		}
	}
	if strings.TrimSpace(string(password)) == "" {
		return errors.New("registryauth: aws ecr get-login-password minted an empty password")
	}

	return dockerLogin(ctx, r.Env, "AWS", r.Host, string(password))
}

// loginOCIBasic runs `docker login` with the configured username, writing
// Credential to stdin so it never appears in an argument list.
func loginOCIBasic(ctx context.Context, r Registry) error {
	if strings.TrimSpace(r.Username) == "" {
		return errors.New("registryauth: oci-basic Username is required")
	}
	if r.Credential == "" {
		return errors.New("registryauth: oci-basic Credential is required")
	}
	return dockerLogin(ctx, r.Env, r.Username, r.Host, r.Credential)
}

// dockerLogin runs `docker login --username <username> --password-stdin
// <host>`, writing credential to the child's stdin.
func dockerLogin(ctx context.Context, env []string, username, host, credential string) error {
	docker := exec.CommandContext(ctx, "docker", "login", "--username", username, "--password-stdin", host)
	applyEnv(docker, env)
	docker.Stdin = strings.NewReader(credential)
	var dockerErr bytes.Buffer
	docker.Stderr = &dockerErr
	// A killed docker may leave a grandchild it spawned holding the stderr
	// pipe's write end open; WaitDelay caps how long Wait keeps draining
	// that pipe after cancellation, so this call still returns promptly.
	docker.WaitDelay = subprocessWaitDelay
	if err := docker.Run(); err != nil {
		// A cancelled context killed the subprocess; that is why it failed,
		// not whatever exit status the kill produced.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("registryauth: docker login: %w", ctxErr)
		}
		// A leaked grandchild holding the stderr pipe open past WaitDelay
		// produces exec.ErrWaitDelay even when docker itself exited zero;
		// that is not a failure of the login this call cares about.
		if errors.Is(err, exec.ErrWaitDelay) && docker.ProcessState != nil && docker.ProcessState.Success() {
			return nil
		}
		stderr := strings.TrimSpace(dockerErr.String())
		if stderr != "" {
			return fmt.Errorf("registryauth: docker login: %w: %s", err, stderr)
		}
		return fmt.Errorf("registryauth: docker login: %w", err)
	}
	return nil
}

// applyEnv sets cmd's environment to env when non-empty, leaving the ambient
// environment (cmd.Env == nil, which os/exec reads as os.Environ()) in place
// otherwise.
func applyEnv(cmd *exec.Cmd, env []string) {
	if len(env) > 0 {
		cmd.Env = env
	}
}
