// Package tofuhandler is a spike: one shared handler per resource type that
// drives OpenTofu, behind the kit's handler.DeclaredResource interface. A
// type is data (its providers, its module and its outputs), and the same
// Resource code ensures, observes and deletes every type.
//
// Each declared resource of a subscription gets a working directory of its
// own under Engine.Root, holding its rendered root module, the inputs it was
// last ensured with, its OpenTofu state and a record of the provider objects
// it created. The record is what lets an absence proof ask the provider about
// objects the state no longer lists.
package tofuhandler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Engine runs the tofu binary for every Resource that shares it.
type Engine struct {
	// Bin is the path of the tofu binary.
	Bin string
	// Root is the directory each resource's working directory lives under.
	Root string
	// PluginCache is a provider cache shared by every working directory, so
	// a provider is downloaded once per machine. Optional.
	PluginCache string
	// StatePassphrase, when set, turns on OpenTofu's state and plan
	// encryption with a PBKDF2 key derived from it. It must be at least 16
	// characters.
	StatePassphrase string
	// Offline renders every AWS provider with fake credentials, every
	// account lookup skipped and every endpoint pointed at a closed local
	// port, so a plan can make no AWS call.
	Offline bool

	mu      sync.Mutex
	timings []Timing
}

// Timing is the wall-clock duration of one tofu command.
type Timing struct {
	Dir      string
	Command  string
	Duration time.Duration
}

// Timings returns every tofu command run so far, in order.
func (e *Engine) Timings() []Timing {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Timing(nil), e.timings...)
}

// errExit carries a non-zero tofu exit with its stderr.
type errExit struct {
	code   int
	stderr string
}

func (e *errExit) Error() string {
	return fmt.Sprintf("tofu exited %d: %s", e.code, strings.TrimSpace(lastLines(e.stderr, 8)))
}

// run runs tofu with args in dir and returns its stdout. A non-zero exit is
// an *errExit carrying stderr.
func (e *Engine) run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1")
	if e.PluginCache != "" {
		cmd.Env = append(cmd.Env, "TF_PLUGIN_CACHE_DIR="+e.PluginCache)
	}
	if e.Offline {
		// No profile, shared credential or instance metadata of this
		// machine reaches the provider.
		cmd.Env = append(cmd.Env, "AWS_CONFIG_FILE=/dev/null", "AWS_SHARED_CREDENTIALS_FILE=/dev/null",
			"AWS_EC2_METADATA_DISABLED=true", "AWS_PROFILE=")
	}
	if e.StatePassphrase != "" {
		cmd.Env = append(cmd.Env, "TF_ENCRYPTION="+encryptionConfig(e.StatePassphrase))
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	e.mu.Lock()
	e.timings = append(e.timings, Timing{Dir: dir, Command: strings.Join(args[:1], " "), Duration: time.Since(start)})
	e.mu.Unlock()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return stdout.String(), &errExit{code: ee.ExitCode(), stderr: stderr.String() + stdout.String()}
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}

// encryptionConfig is the TF_ENCRYPTION value that encrypts state and plans
// with AES-GCM under a key derived from passphrase.
func encryptionConfig(passphrase string) string {
	return fmt.Sprintf(`key_provider "pbkdf2" "k" {
  passphrase = %q
}
method "aes_gcm" "m" {
  keys = key_provider.pbkdf2.k
}
state {
  method   = method.aes_gcm.m
  enforced = true
}
plan {
  method   = method.aes_gcm.m
  enforced = true
}`, passphrase)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
