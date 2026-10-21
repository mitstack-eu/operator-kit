package observe

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/mitstack-eu/operator-kit/v3/contract"
)

// A script that exits nonzero is an error, never output.
type HostCommander interface {
	RunOnInstance(ctx context.Context, d contract.Desired, instanceID, script string) (stdout string, err error)
}

// A name is interpolated into a script, so only names that need no quoting are run.
var containerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// The probe prints absent only when Docker answers that no such container exists.
const containerProbe = `out=$(docker inspect --format '{{.State.Status}}' %[1]s 2>&1) && { printf '%%s\n' "$out"; exit 0; }
case "$out" in *"No such object"*|*"No such container"*) echo absent ;; *) printf '%%s\n' "$out" >&2; exit 1 ;; esac`

// Running is present, absent is absent, anything else is unknown, and a failed probe is an error.
type EC2Container struct {
	API HostCommander
}

func (o EC2Container) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "instanceId", "container")
	if !ok {
		return obs, nil
	}
	if !containerName.MatchString(ids[1]) {
		return unknown("resource %q: container name %q is not one the observer can ask about", r.Name, ids[1]), nil
	}
	stdout, err := o.API.RunOnInstance(ctx, r.Desired, ids[0], fmt.Sprintf(containerProbe, ids[1]))
	if err != nil {
		return contract.Observation{}, err
	}
	switch state := strings.TrimSpace(stdout); state {
	case "running":
		return present(nil), nil
	case "absent":
		return absent(), nil
	case "":
		return unknown("resource %q: the probe of container %s printed nothing", r.Name, ids[1]), nil
	default:
		return unknown("resource %q: container %s is %s", r.Name, ids[1], state), nil
	}
}

// postgresProbe prints PRESENT=1, or 0 when Docker answers without the container, then READY=<pg_isready's exit status>.
const postgresProbe = `if docker inspect %[1]s >/dev/null 2>&1; then echo PRESENT=1; docker exec %[1]s pg_isready -U postgres >/dev/null 2>&1; echo READY=$?
elif docker version >/dev/null 2>&1; then echo PRESENT=0; else echo PRESENT=unknown; fi`

// EC2PostgresDatabase reports only presence, identity and dbName, and a block that provides it reports the type's other outputs.
type EC2PostgresDatabase struct {
	API HostCommander
}

func (o EC2PostgresDatabase) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "instanceId", "container")
	if !ok {
		return obs, nil
	}
	if !containerName.MatchString(ids[1]) {
		return unknown("resource %q: container name %q is not one the observer can ask about", r.Name, ids[1]), nil
	}
	stdout, err := o.API.RunOnInstance(ctx, r.Desired, ids[0], fmt.Sprintf(postgresProbe, ids[1]))
	if err != nil {
		return contract.Observation{}, err
	}
	markers := map[string]string{}
	for _, line := range strings.Split(stdout, "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			markers[key] = value
		}
	}
	switch present, ready := markers["PRESENT"], markers["READY"]; {
	case present == "0":
		return absent(), nil
	case present != "1":
		return unknown("resource %q: Docker could not say whether container %s exists", r.Name, ids[1]), nil
	case ready == "":
		return unknown("resource %q: container %s exists, but the probe gave no readiness answer", r.Name, ids[1]), nil
	case ready != "0":
		return unknown("resource %q: container %s exists but is not ready for connections (pg_isready exited %s)", r.Name, ids[1], ready), nil
	}
	var outputs map[string]string
	if db := r.Identity["database"]; db != "" {
		outputs = map[string]string{"dbName": db}
	}
	return contract.Observation{State: contract.Present, Outputs: outputs}, nil
}
