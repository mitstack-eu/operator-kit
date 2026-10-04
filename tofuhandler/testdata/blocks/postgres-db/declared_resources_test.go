package postgresdb

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/contract"
	"github.com/mitstack-eu/operator-kit/handler"
	"github.com/mitstack-eu/operator-kit/kittest"
	"github.com/mitstack-eu/operator-kit/tofuhandler"
)

// TestDeclaredResources drives postgres-db's one declared resource through
// the shared OpenTofu handler for host.postgres-database, on a throwaway
// app whose host is the local docker daemon and whose block volume is a
// temporary directory. It runs tofu apply, so it refuses to run unless
// TOFUHANDLER_PROOF is 1, which only the kit's CI proof run sets.
func TestDeclaredResources(t *testing.T) {
	if os.Getenv("TOFUHANDLER_PROOF") != "1" {
		t.Fatal("TOFUHANDLER_PROOF is not 1: this test applies OpenTofu, which only the kit's CI proof run may do")
	}
	e, err := tofuhandler.EngineFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Not t.TempDir: the Postgres container writes the data directory as
	// its own user, which the test's cleanup could not remove.
	volume, err := os.MkdirTemp("", "throwaway-block-volume-")
	if err != nil {
		t.Fatal(err)
	}

	sub := "spike-3986-pg-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	d := contract.Desired{
		Identity: contract.Identity{SubscriptionID: sub, BlockID: "postgres-db", OrgID: "throwaway-org", AppID: "throwaway-app"},
		Params:   map[string]string{},
		Bindings: map[string]map[string]string{
			"host":         {"execKind": "docker-local"},
			"block-volume": {"mountPath": volume},
		},
	}

	database := &tofuhandler.Resource{
		Engine: e,
		Spec:   tofuhandler.Specs["host.postgres-database"],
		Name:   "database",
		Inputs: func(d contract.Desired) (map[string]any, error) {
			host, err := tofuhandler.DockerHost(d.Bindings["host"]["execKind"], d.Bindings["host"]["execTarget"])
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"docker_host": host,
				"name":        "mitstack-postgres-" + d.SubscriptionID,
				"data_dir":    filepath.Join(d.Bindings["block-volume"]["mountPath"], "postgres", d.SubscriptionID),
				"db_name":     "app",
				"username":    "app",
			}, nil
		},
	}

	start := time.Now()
	kittest.AssertDeclaredResources(t, kittest.DeclaredResources{
		Manifest:  manifest,
		Resources: map[string]handler.DeclaredResource{"database": tofuhandler.Timed{Name: "database", Resource: database, Log: t.Logf}},
		Supplied: map[string]kittest.SuppliedResource{
			"host":         tofuhandler.DockerDaemon{},
			"block-volume": tofuhandler.Directory{Path: volume},
		},
		Desired: d,
	})
	t.Logf("timing check=total seconds=%.2f", time.Since(start).Seconds())
	for _, tm := range e.Timings() {
		t.Logf("timing tofu=%s seconds=%.2f dir=%s", tm.Command, tm.Duration.Seconds(), filepath.Base(tm.Dir))
	}
}
