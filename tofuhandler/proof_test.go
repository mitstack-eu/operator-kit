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
	"testing"
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

// TestProofOnDocker runs blockverify against the postgres-db and plain-block
// targets under testdata/blocks, whose declared resources run on the shared
// OpenTofu handlers against the runner's docker daemon, and fails unless the
// resources suite passes for each. It prints each report whole. It applies
// OpenTofu, so it runs only in CI, where CI is "true".
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
	for _, target := range []string{"postgres-db", "plain-block"} {
		t.Run(target, func(t *testing.T) {
			dir, _ := filepath.Abs(filepath.Join("testdata", "blocks", target))
			cmd := exec.Command("go", "run", "./blockverify", "--json", "--standalone", dir)
			cmd.Dir = kitRoot
			cmd.Env = append(os.Environ(), "TOFU_BIN="+bin, "TOFUHANDLER_PROOF=1", "TF_PLUGIN_CACHE_DIR="+cache,
				"TOFUHANDLER_STATE_PASSPHRASE=throwaway-proof-passphrase-3986")
			out, _ := cmd.CombinedOutput()
			t.Logf("blockverify report for %s:\n%s", target, out)

			// The timings live in the target's own go test output, which
			// blockverify keeps to one line, so run it again verbosely only
			// when the suite failed, to show why.
			var report struct {
				Verdict string `json:"verdict"`
				Suites  []struct{ Name, Status, Detail string }
			}
			if err := json.Unmarshal(lastJSONLine(out), &report); err != nil {
				t.Fatalf("blockverify printed no report: %v", err)
			}
			for _, s := range report.Suites {
				t.Logf("suite %s: %s: %s", s.Name, s.Status, s.Detail)
				if s.Name == "resources" && s.Status != "pass" {
					t.Errorf("the resources suite did not pass for %s: %s", target, s.Detail)
				}
			}
		})
	}
	// The same test, run verbosely, so the log carries each call's timing.
	for _, target := range []string{"postgres-db", "plain-block"} {
		dir, _ := filepath.Abs(filepath.Join("testdata", "blocks", target))
		cmd := exec.Command("go", "test", "-count=1", "-v", "-run", "TestDeclaredResources", "./...")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "TOFU_BIN="+bin, "TOFUHANDLER_PROOF=1", "TF_PLUGIN_CACHE_DIR="+cache,
			"TOFUHANDLER_STATE_PASSPHRASE=throwaway-proof-passphrase-3986")
		out, err := cmd.CombinedOutput()
		t.Logf("verbose TestDeclaredResources for %s (err=%v):\n%s", target, err, out)
		if err != nil {
			t.Errorf("the verbose run failed for %s", target)
		}
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
