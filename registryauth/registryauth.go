// Package registryauth, an optional helper, logs a container-registry CLI in with the credential passed over stdin, never as an argument or in a log line.
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

// A grandchild can hold a pipe open after its parent is killed, so this bounds how long Wait keeps draining it.
const subprocessWaitDelay = 1 * time.Second

// Kind selects which login driver Login uses.
type Kind string

const (
	// KindECR authenticates against AWS ECR via the AWS CLI's minted password.
	KindECR Kind = "ecr"
	// KindOCIBasic authenticates against a plain OCI registry with a username and a password or token.
	KindOCIBasic Kind = "oci-basic"
)

// Registry describes one registry login.
type Registry struct {
	Kind Kind
	Host string
	// Username is used by KindOCIBasic only; KindECR always logs in as AWS.
	Username string
	// Credential is the KindOCIBasic secret and must be empty for KindECR.
	Credential string
	// Region is required for KindECR only.
	Region string
	// Env is the subprocess environment; nil or empty inherits the ambient one.
	Env []string
}

// Login authenticates the local docker CLI against r.Host, rejecting bad input before it spawns any subprocess.
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
	aws.WaitDelay = subprocessWaitDelay
	password, err := aws.Output()
	if err != nil {
		// A cancelled context killed the subprocess, so report that rather than the kill's exit status.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("registryauth: aws ecr get-login-password: %w", ctxErr)
		}
		// ErrWaitDelay with a zero exit is not a failure; the empty-password check below catches a truncated drain.
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

func loginOCIBasic(ctx context.Context, r Registry) error {
	if strings.TrimSpace(r.Username) == "" {
		return errors.New("registryauth: oci-basic Username is required")
	}
	if r.Credential == "" {
		return errors.New("registryauth: oci-basic Credential is required")
	}
	return dockerLogin(ctx, r.Env, r.Username, r.Host, r.Credential)
}

func dockerLogin(ctx context.Context, env []string, username, host, credential string) error {
	docker := exec.CommandContext(ctx, "docker", "login", "--username", username, "--password-stdin", host)
	applyEnv(docker, env)
	docker.Stdin = strings.NewReader(credential)
	var dockerErr bytes.Buffer
	docker.Stderr = &dockerErr
	docker.WaitDelay = subprocessWaitDelay
	if err := docker.Run(); err != nil {
		// A cancelled context killed the subprocess, so report that rather than the kill's exit status.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("registryauth: docker login: %w", ctxErr)
		}
		// ErrWaitDelay with a zero exit is not a failure.
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

func applyEnv(cmd *exec.Cmd, env []string) {
	if len(env) > 0 {
		cmd.Env = env
	}
}
