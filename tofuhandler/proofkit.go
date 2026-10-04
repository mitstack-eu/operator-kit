package tofuhandler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/mitstack-eu/operator-kit/contract"
	"github.com/mitstack-eu/operator-kit/handler"
)

// EngineFromEnv builds an Engine from TOFU_BIN, TOFUHANDLER_ROOT,
// TF_PLUGIN_CACHE_DIR and TOFUHANDLER_STATE_PASSPHRASE. TOFU_BIN is
// required; the root defaults to a new temporary directory.
func EngineFromEnv() (*Engine, error) {
	bin := os.Getenv("TOFU_BIN")
	if bin == "" {
		return nil, errors.New("TOFU_BIN is unset, so there is no tofu binary to run")
	}
	root := os.Getenv("TOFUHANDLER_ROOT")
	if root == "" {
		var err error
		if root, err = os.MkdirTemp("", "tofuhandler-"); err != nil {
			return nil, err
		}
	}
	return &Engine{Bin: bin, Root: root, PluginCache: os.Getenv("TF_PLUGIN_CACHE_DIR"),
		StatePassphrase: os.Getenv("TOFUHANDLER_STATE_PASSPHRASE")}, nil
}

// Timed wraps a DeclaredResource and reports the wall-clock duration of each
// call it makes, in call order, through Log.
type Timed struct {
	Name     string
	Resource handler.DeclaredResource
	Log      func(format string, args ...any)
}

var _ handler.DeclaredResource = Timed{}

func (t Timed) Ensure(ctx context.Context, d contract.Desired) error {
	start := time.Now()
	err := t.Resource.Ensure(ctx, d)
	t.Log("timing resource=%s call=ensure seconds=%.2f err=%v", t.Name, time.Since(start).Seconds(), err)
	return err
}

func (t Timed) Observe(ctx context.Context, id contract.Identity) (contract.Observation, error) {
	start := time.Now()
	obs, err := t.Resource.Observe(ctx, id)
	t.Log("timing resource=%s call=observe seconds=%.2f state=%s detail=%q err=%v", t.Name, time.Since(start).Seconds(), obs.State, obs.StatusDetail, err)
	return obs, err
}

func (t Timed) Delete(ctx context.Context, id contract.Identity) error {
	start := time.Now()
	err := t.Resource.Delete(ctx, id)
	t.Log("timing resource=%s call=delete seconds=%.2f err=%v", t.Name, time.Since(start).Seconds(), err)
	return err
}

// DockerDaemon is a host supplied to a block: the docker daemon at Host,
// present when `docker version` answers from it.
type DockerDaemon struct {
	Host string
}

func (h DockerDaemon) Observe(ctx context.Context, _ contract.Identity) (contract.Observation, error) {
	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	if h.Host != "" {
		cmd.Env = append(os.Environ(), "DOCKER_HOST="+h.Host)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return contract.Observation{State: contract.Absent, StatusDetail: fmt.Sprintf("docker version: %v: %s", err, out)}, nil
	}
	return contract.Observation{State: contract.Present, StatusDetail: "docker server " + string(out)}, nil
}

// Directory is a block volume supplied to a block: a directory on the host,
// present when it exists.
type Directory struct {
	Path string
}

func (v Directory) Observe(_ context.Context, _ contract.Identity) (contract.Observation, error) {
	if fi, err := os.Stat(v.Path); err == nil && fi.IsDir() {
		return contract.Observation{State: contract.Present}, nil
	}
	return contract.Observation{State: contract.Absent, StatusDetail: v.Path + " is not a directory"}, nil
}
