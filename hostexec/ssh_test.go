package hostexec_test

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// The sentinels are low-entropy and not token-shaped so they do not trip a secret scanner on this file.
const (
	leakScriptSecret = "PGPASSWORD-THAT-MUST-NEVER-SURFACE"
	leakPEM          = "PEM-KEY-THAT-MUST-NEVER-SURFACE"
)

// The ssh driver carries the script on stdin and the PEM in a 0600 file that outlives no call, so neither reaches argv, a log or a returned value.
func TestSSH(t *testing.T) {
	secretScript := "export PGPASSWORD=" + leakScriptSecret + "\npsql -c 'select 1'"
	cases := []struct {
		name    string
		output  bool
		body    string
		pem     string
		script  string
		wantOut string
		errHas  string
		check   func(t *testing.T, f fakes, out string)
	}{
		{"the identity file is written 0600 and removed once Run returns", false, "exit 0", "PEM-DATA", "true", "", "", func(t *testing.T, f fakes, _ string) {
			if perm := strings.TrimSpace(f.read("perm")); perm != "600" {
				t.Errorf("identity file mode = %s, want 600: a readable private key on disk is a leak however short-lived", perm)
			}
			assertIdentityFileGone(t, f)
		}},
		{"a non-zero exit carries the captured stderr", false, `echo "Permission denied (publickey)" >&2; exit 255`, "PEM-DATA", "true", "", "Permission denied", nil},
		{"RunOutput returns the captured stdout", true, `printf 'READY=0\n'`, "PEM-DATA", "true", "READY=0", "", nil},
		{"RunOutput returns no partial output from a failed run", true, `printf 'partial output\n'; echo "Permission denied (publickey)" >&2; exit 255`, "PEM-DATA", "true", "", "Permission denied", nil},
		{"a successful exit is not failed by a grandchild holding stderr open past WaitDelay", false, "( sleep 3 & ); exit 0", "PEM-DATA", "true", "", "", nil},
		{"the script's secret arrives over stdin and never on the command line", false, "exit 0", "irrelevant-pem", secretScript, "", "", func(t *testing.T, f fakes, _ string) {
			assertOnlyOnStdin(t, f, secretScript, leakScriptSecret)
		}},
		{"the private key never reaches the command line, only a file that is gone afterwards", false, "exit 0", leakPEM, "true", "", "", func(t *testing.T, f fakes, _ string) {
			kittest.AssertNoSecret(t, "ssh argv", strings.Join(f.calls("ssh")[0], "\n"), leakPEM)
			assertIdentityFileGone(t, f)
		}},
		{"RunOutput's return value carries neither secret", true, "cat >/dev/null; printf 'ok\\n'", leakPEM, secretScript, "ok", "", func(t *testing.T, f fakes, out string) {
			kittest.AssertNoSecret(t, "ssh RunOutput return value", out, leakScriptSecret, leakPEM)
			assertOnlyOnStdin(t, f, secretScript, leakScriptSecret)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakes(t, awsFake{}, tc.body)
			var logs bytes.Buffer
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })
			ep := entryPoints[0]
			if tc.output {
				ep = entryPoints[1]
			}

			out, err := ep.call(context.Background(), sshHost(tc.pem), tc.script)

			if out != tc.wantOut || (tc.errHas == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.errHas)) {
				t.Errorf("%s() = %q, %v; want %q with an error saying %q", ep.name, out, err, tc.wantOut, tc.errHas)
			}
			kittest.AssertNoSecret(t, "log output", logs.String(), leakScriptSecret, leakPEM)
			if tc.check != nil {
				tc.check(t, f, out)
			}
		})
	}
}

func assertIdentityFileGone(t *testing.T, f fakes) {
	t.Helper()
	idPath := f.read("idpath")
	if !filepath.IsAbs(idPath) {
		t.Fatalf("-i pointed at %q, want an absolute path", idPath)
	}
	if _, err := os.Stat(idPath); !os.IsNotExist(err) {
		t.Errorf("identity file %s still exists after the call returned: the PEM must not outlive it", idPath)
	}
}

// assertOnlyOnStdin also proves the script ran, which keeps the argv assertion from passing vacuously.
func assertOnlyOnStdin(t *testing.T, f fakes, script, secret string) {
	t.Helper()
	kittest.AssertNoSecret(t, "ssh argv", strings.Join(f.calls("ssh")[0], "\n"), secret)
	if stdin := f.read("ssh-stdin"); stdin != script+"\x1e" {
		t.Errorf("ssh stdin = %q, want exactly the script %q", stdin, script)
	}
}
