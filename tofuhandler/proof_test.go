package tofuhandler

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	tofuVersion = "1.12.3"
	tofuZipURL  = "https://github.com/opentofu/opentofu/releases/download/v" + tofuVersion + "/tofu_" + tofuVersion + "_linux_amd64.zip"
	// tofuZipSHA256 is the line for tofu_1.12.3_linux_amd64.zip in the
	// release's tofu_1.12.3_SHA256SUMS, whose signature by OpenTofu's key
	// E3E6 E43D 84CB 852E ADB0 051D 0C0A F313 E5FD 9F80 was checked when it
	// was copied here.
	tofuZipSHA256 = "46b48c3438c65cf479fc076c9281422ffa2f493548d1e813d154c835c5986a08"
)

// TestProofOnDocker runs, on the CI runner's docker daemon:
//
//   - blockverify against the postgres-db and plain-block targets under
//     testdata/blocks, whose declared resources run on the shared OpenTofu
//     handlers, failing unless the resources suite passes for each;
//   - the same TestDeclaredResources verbosely, so the log carries each
//     call's timing;
//   - the box scripts postgres-db and plain-block send over SSM today, timed
//     the same way, as the baseline.
//
// Everything is logged on this test itself, because go test drops a passing
// subtest's log. It applies OpenTofu, so it runs only in CI, where CI is
// "true".
func TestProofOnDocker(t *testing.T) {
	if os.Getenv("CI") != "true" {
		t.Skip("runs only in CI: it applies OpenTofu, which this repository's contributors may not do from their own machines")
	}
	bin := installTofu(t)
	cache := t.TempDir()
	kitRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "TOFU_BIN="+bin, "TOFUHANDLER_PROOF=1", "TF_PLUGIN_CACHE_DIR="+cache,
		"TOFUHANDLER_STATE_PASSPHRASE=throwaway-proof-passphrase")

	for _, target := range []string{"postgres-db", "plain-block"} {
		dir, _ := filepath.Abs(filepath.Join("testdata", "blocks", target))
		cmd := exec.Command("go", "run", "./blockverify", "--json", "--standalone", dir)
		cmd.Dir = kitRoot
		cmd.Env = env
		start := time.Now()
		out, _ := cmd.CombinedOutput()
		t.Logf("blockverify report for %s, %.1f s:\n%s", target, time.Since(start).Seconds(), lastJSONLine(out))
		var report struct {
			Verdict string `json:"verdict"`
			Suites  []struct{ Name, Status, Detail string }
		}
		if err := json.Unmarshal(lastJSONLine(out), &report); err != nil {
			t.Errorf("blockverify printed no report for %s: %v\n%s", target, err, out)
			continue
		}
		for _, s := range report.Suites {
			t.Logf("%s suite %s: %s: %s", target, s.Name, s.Status, s.Detail)
			if s.Name == "resources" && s.Status != "pass" {
				t.Errorf("the resources suite did not pass for %s: %s", target, s.Detail)
			}
		}
	}

	for _, target := range []string{"postgres-db", "plain-block"} {
		dir, _ := filepath.Abs(filepath.Join("testdata", "blocks", target))
		cmd := exec.Command("go", "test", "-count=1", "-v", "-run", "TestDeclaredResources", "./...")
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		t.Logf("verbose TestDeclaredResources for %s (err=%v):\n%s", target, err, out)
		if err != nil {
			t.Errorf("the verbose run failed for %s", target)
		}
	}

	baseline(t)

	// go test prints a passing test's log only with -v, which kit CI does not
	// pass, so the proof's report would never reach the job log.
	if !t.Failed() {
		t.Error("REPORT-ONLY FAILURE, not a finding: every assertion above passed, and this line fails the test only so go test prints the log")
	}
}

// baseline times the box scripts postgres-db 1.4.0 and plain-block 2.2.0
// send over SSM today, run directly on the runner. Each script is the one
// their provisionCommands, removal and probe build, with these changes only:
// the password is set in the script where today it is read from an SSM
// parameter, and plain's image, port and health URL are the stand-in's.
func baseline(t *testing.T) {
	volume, err := os.MkdirTemp("", "baseline-block-volume-")
	if err != nil {
		t.Fatal(err)
	}
	pg := "mitstack-postgres-baseline"
	pgDir := filepath.Join(volume, "postgres", "baseline")
	pgProvision := strings.Join([]string{
		"set -e",
		"install -d -m 0700 " + pgDir,
		"docker rm -f " + pg + " 2>/dev/null || true",
		"PGPASSWORD=baseline-password-0123456789",
		"docker run -d --name " + pg + " --restart unless-stopped -e POSTGRES_PASSWORD=\"$PGPASSWORD\" -e POSTGRES_DB=app -e POSTGRES_USER=app " +
			"-v " + pgDir + ":/var/lib/postgresql/data -p 5432:5432 postgres:16-alpine " +
			"-c max_wal_size=512MB -c archive_timeout=300 -c archive_mode=on -c archive_command='true'",
		"for i in $(seq 1 60); do docker exec " + pg + " pg_isready -U app >/dev/null 2>&1 && break; sleep 2; done",
		"printf '%s\\n' \"ALTER ROLE app WITH PASSWORD :'pw'\" | docker exec -i " + pg + " psql -v ON_ERROR_STOP=1 -U app -d postgres -v pw=\"$PGPASSWORD\" >/dev/null",
		"docker run --rm --network host -e PGPASSWORD=\"$PGPASSWORD\" postgres:16-alpine psql -h 127.0.0.1 -p 5432 -U app -d app -c 'select 1' >/dev/null",
	}, "\n")
	pgInspect := strings.Join([]string{
		"if docker inspect " + pg + " >/dev/null 2>&1; then echo PRESENT=1; elif docker version >/dev/null 2>&1; then echo PRESENT=0; else echo PRESENT=unknown; fi",
		"PROBE=$(docker exec " + pg + " pg_isready -U postgres 2>&1); echo \"READY=$?\"",
	}, "\n")
	pgRemove := "docker rm -f " + pg + " 2>/dev/null || true"

	plain := "mitstack-plain-tracker-baseline"
	plainProvision := strings.Join([]string{
		"set -e",
		"docker rm -f " + plain + " 2>/dev/null || true",
		"TRACKER_DB_PASSWORD=baseline-password-0123456789",
		"docker run -d --name " + plain + " --restart unless-stopped -e TRACKER_DB_HOST=127.0.0.1 -e TRACKER_DB_PORT=5432 " +
			"-e TRACKER_DB_NAME=app -e TRACKER_DB_USER=app -e TRACKER_DB_PASSWORD=\"$TRACKER_DB_PASSWORD\" -p 8080:80 nginx:1.27-alpine",
		"for i in $(seq 1 60); do docker exec " + plain + " wget -qO- -T 2 http://127.0.0.1:80/ >/dev/null 2>&1 && break; sleep 2; done",
	}, "\n")
	plainProbe := "if docker ps -a --format '{{.Names}}' >/tmp/plain_ps.out 2>&1; then if grep -qx " + plain + " /tmp/plain_ps.out; then " +
		"docker exec " + plain + " wget -qO- -T 5 http://127.0.0.1:80/ >/tmp/plain_health.out 2>&1; echo \"READY=$?\"; " +
		"else echo \"CONTAINER=absent\"; fi; else echo \"CONTAINER=unknown\"; cat /tmp/plain_ps.out; fi"
	plainRemove := "docker rm -f " + plain + " 2>/dev/null || true"

	// The readiness loop's exit when the server never answers: the probe
	// fails every time, and the loop ends on its last sleep.
	neverReady := "set -e\nfor i in $(seq 1 3); do false && break; sleep 0; done\necho LOOP-EXIT=$?"

	for _, s := range []struct{ name, script string }{
		{"postgres-db provision, first", pgProvision},
		{"postgres-db provision, repeated", pgProvision},
		{"postgres-db inspect", pgInspect},
		{"postgres-db remove", pgRemove},
		{"postgres-db inspect after remove", pgInspect},
		{"plain provision, first", plainProvision},
		{"plain provision, repeated", plainProvision},
		{"plain health probe", plainProbe},
		{"plain remove", plainRemove},
		{"plain health probe after remove", plainProbe},
		{"readiness loop that never succeeds", neverReady},
	} {
		start := time.Now()
		out, err := exec.Command("sh", "-c", s.script).CombinedOutput()
		t.Logf("timing baseline=%q seconds=%.2f err=%v output=%q", s.name, time.Since(start).Seconds(), err, strings.TrimSpace(string(out)))
	}
}

// installTofu downloads the pinned OpenTofu release, refuses it unless its
// sha256 matches the pinned sum, and returns the unpacked binary's path.
func installTofu(t *testing.T) string {
	t.Helper()
	resp, err := http.Get(tofuZipURL)
	if err != nil {
		t.Fatalf("downloading OpenTofu: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("downloading OpenTofu: status %d: %v", resp.StatusCode, err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != tofuZipSHA256 {
		t.Fatalf("OpenTofu %s has sha256 %s, want %s", tofuVersion, got, tofuZipSHA256)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, f := range zr.File {
		if f.Name != "tofu" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		bin, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "tofu")
		if err := os.WriteFile(path, bin, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Logf("OpenTofu %s installed, sha256 %s verified", tofuVersion, tofuZipSHA256)
		return path
	}
	t.Fatal("the OpenTofu archive holds no tofu binary")
	return ""
}

// lastJSONLine returns the last line of out that starts with an opening
// brace, where blockverify --json prints its report.
func lastJSONLine(out []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if bytes.HasPrefix(bytes.TrimSpace(lines[i]), []byte("{")) {
			return lines[i]
		}
	}
	return nil
}
