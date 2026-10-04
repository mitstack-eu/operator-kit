package plainblock

import (
	"context"
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

// standInImage is the image the tracker runs in this proof. Plain's own
// image sits in a private registry the CI runner cannot pull from, so a
// public image that answers HTTP stands in for it: the run proves the
// host.container type's lifecycle and the database binding, not Plain's own
// start-up.
const standInImage = "nginx:1.27-alpine"

// TestDeclaredResources drives plain's one declared resource through the
// shared OpenTofu handler for host.container, on a throwaway app whose host
// is the local docker daemon. The app's database is created first through
// the shared host.postgres-database handler and supplied to the block, as
// postgres-db would offer it. It runs tofu apply, so it refuses to run
// unless TOFUHANDLER_PROOF is 1, which only the kit's CI proof run sets.
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
	volume, err := os.MkdirTemp("", "throwaway-block-volume-")
	if err != nil {
		t.Fatal(err)
	}
	sub := "spike-3986-plain-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	app := contract.Identity{SubscriptionID: sub, BlockID: "plain", OrgID: "throwaway-org", AppID: "throwaway-app"}
	ctx := context.Background()

	// The app's database, which postgres-db would create and offer.
	db := &tofuhandler.Resource{
		Engine: e, Spec: tofuhandler.Specs["host.postgres-database"], Name: "supplied-database",
		Inputs: func(d contract.Desired) (map[string]any, error) {
			return map[string]any{"docker_host": "unix:///var/run/docker.sock", "name": "mitstack-postgres-" + d.SubscriptionID,
				"data_dir": filepath.Join(volume, "postgres", d.SubscriptionID), "db_name": "plain", "username": "plain"}, nil
		},
	}
	dbStart := time.Now()
	if err := db.Ensure(ctx, contract.Desired{Identity: app}); err != nil {
		t.Fatalf("creating the supplied database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Delete(context.Background(), app); err != nil {
			t.Errorf("deleting the supplied database: %v", err)
		}
	})
	dbObs, err := db.Observe(ctx, app)
	if err != nil || dbObs.State != contract.Present {
		t.Fatalf("the supplied database is not present: %v %s %s", err, dbObs.State, dbObs.StatusDetail)
	}
	t.Logf("timing resource=supplied-database call=ensure+observe seconds=%.2f", time.Since(dbStart).Seconds())

	d := contract.Desired{
		Identity: app,
		Params:   map[string]string{"image": standInImage},
		Bindings: map[string]map[string]string{
			"host":              {"execKind": "docker-local"},
			"postgres-database": dbObs.Outputs,
		},
		Secrets: map[string]map[string]string{"postgres-database": dbObs.SecretOutputs},
	}

	tracker := &tofuhandler.Resource{
		Engine: e, Spec: tofuhandler.Specs["host.container"], Name: "tracker",
		Inputs: func(d contract.Desired) (map[string]any, error) {
			host, err := tofuhandler.DockerHost(d.Bindings["host"]["execKind"], d.Bindings["host"]["execTarget"])
			if err != nil {
				return nil, err
			}
			pg := d.Bindings["postgres-database"]
			return map[string]any{
				"docker_host": host,
				"name":        "mitstack-plain-" + d.SubscriptionID,
				"image":       d.Params["image"],
				"port":        80,
				"env": map[string]any{
					"TRACKER_DB_HOST":     pg["host"],
					"TRACKER_DB_PORT":     pg["port"],
					"TRACKER_DB_NAME":     pg["dbName"],
					"TRACKER_DB_USER":     pg["username"],
					"TRACKER_DB_PASSWORD": d.Secrets["postgres-database"]["password"],
				},
				"health_command": "wget -qO- -T 2 http://127.0.0.1:80/ >/dev/null",
			}, nil
		},
	}

	start := time.Now()
	kittest.AssertDeclaredResources(t, kittest.DeclaredResources{
		Manifest:  manifest,
		Resources: map[string]handler.DeclaredResource{"tracker": tofuhandler.Timed{Name: "tracker", Resource: tracker, Log: t.Logf}},
		Supplied: map[string]kittest.SuppliedResource{
			"host":              tofuhandler.DockerDaemon{},
			"postgres-database": db,
		},
		Desired: d,
	})
	t.Logf("timing check=total seconds=%.2f", time.Since(start).Seconds())
	for _, tm := range e.Timings() {
		t.Logf("timing tofu=%s seconds=%.2f dir=%s", tm.Command, tm.Duration.Seconds(), filepath.Base(tm.Dir))
	}
}
