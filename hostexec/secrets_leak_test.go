package hostexec_test

// hostexec drives real subprocesses on a caller's behalf, and the script it
// runs may carry a secret the caller embedded in it (a database password, for
// instance). The invariant this file asserts is that neither the script's
// secret content nor the ssh driver's PEM credential ever appears on a
// recorded command line, in the package's own log output, or in a returned
// error — the only place either may legitimately travel is the child
// process's stdin, which is invisible to `ps` and to any log line.

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/hostexec"
	"github.com/mitstack-eu/operator-kit/kittest"
)

// Low-entropy, non-token-shaped sentinels, deliberately, so they read as
// placeholders rather than tripping a secret scanner on the test file itself.
const (
	leakScriptSecret = "PGPASSWORD-THAT-MUST-NEVER-SURFACE"
	leakPEM          = "PEM-KEY-THAT-MUST-NEVER-SURFACE"
)

func TestNoSecretLeak_SSHScriptSecretNeverReachesTheCommandLine(t *testing.T) {
	dir, argvRecord, stdinRecord, _, _ := fakeSSH(t, "exit 0")
	setPath(t, dir)

	script := "export PGPASSWORD=" + leakScriptSecret + "\npsql -c 'select 1'"
	if err := hostexec.Run(context.Background(), sshHost("irrelevant-pem"), script); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	argv, err := os.ReadFile(argvRecord)
	if err != nil {
		t.Fatalf("reading fake ssh argv record: %v", err)
	}
	kittest.AssertNoSecret(t, "ssh argv", string(argv), leakScriptSecret)

	stdin, err := os.ReadFile(stdinRecord)
	if err != nil {
		t.Fatalf("reading fake ssh stdin record: %v", err)
	}
	if !strings.Contains(string(stdin), leakScriptSecret) {
		t.Error("the script secret must arrive over the ssh subprocess's stdin — its absence there means Run never actually ran the script, which would make the argv assertion above vacuous")
	}
}

func TestNoSecretLeak_SSHScriptSecretNeverAppearsInLogs(t *testing.T) {
	dir, _, _, _, _ := fakeSSH(t, "exit 0")
	setPath(t, dir)

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	script := "export PGPASSWORD=" + leakScriptSecret
	if err := hostexec.Run(context.Background(), sshHost("irrelevant-pem"), script); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	kittest.AssertNoSecret(t, "log output", buf.String(), leakScriptSecret)
}

func TestNoSecretLeak_SSHPrivateKeyNeverReachesTheCommandLine(t *testing.T) {
	dir, argvRecord, _, _, idPathRecord := fakeSSH(t, "exit 0")
	setPath(t, dir)

	if err := hostexec.Run(context.Background(), sshHost(leakPEM), "true"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	argv, err := os.ReadFile(argvRecord)
	if err != nil {
		t.Fatalf("reading fake ssh argv record: %v", err)
	}
	kittest.AssertNoSecret(t, "ssh argv", string(argv), leakPEM)

	idPath, err := os.ReadFile(idPathRecord)
	if err != nil {
		t.Fatalf("fake ssh never saw a -i flag: %v", err)
	}
	if !filepath.IsAbs(string(idPath)) {
		t.Errorf("-i must point at a real path, got %q", idPath)
	}
	if _, err := os.Stat(string(idPath)); !os.IsNotExist(err) {
		t.Errorf("identity file %s still exists after Run returned — the PEM must not be left on disk", idPath)
	}
}

// TestSSMRun_ScriptIsRecordedInTheCommandLineSoItMustNeverCarryASecret pins the
// one place the two drivers genuinely differ, rather than letting the package
// doc's blanket "never as a command-line argument" claim go unchecked for the
// ssm driver. Unlike ssh's stdin hand-off, the ssm driver's send-command puts
// the WHOLE script into "--parameters commands=...", which lands in this
// process's own argv and in AWS CloudTrail's SendCommand request logging —
// that is not a bug this test exists to catch, it is a property of the
// AWS-RunShellScript API this driver has no way around, and it has to stay
// pinned and named honestly rather than silently assumed away.
//
// The caller's safe route when the box needs a secret is to put a parameter
// NAME in the script and fetch the value out of band once the script is
// already running there, never to embed the value itself — exactly what
// web-app-aws/postgres-db/provision.go's SSM SecureString parameter hand-off
// does (see ssmParamName / putSecureParam / getSecureParam there).
//
// This test also folds in the cheap insurance that Host.Credential — unused
// by this driver, but possibly populated by a caller reusing one Host across
// Kinds — never rides along in that same exposed argv, so the check has real
// teeth instead of asserting something the ssm code path could never do.
func TestSSMRun_ScriptIsRecordedInTheCommandLineSoItMustNeverCarryASecret(t *testing.T) {
	shrinkSSMPolling(t)
	dir, argvRecord, _ := fakeSSM(t, "cmd-1", []string{"Success"}, 0)
	setPath(t, dir)

	const scriptMarker = "echo this-script-text-must-appear-verbatim-in-argv"
	h := ssmHost()
	h.Credential = leakPEM
	if err := hostexec.Run(context.Background(), h, scriptMarker); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	argv, err := os.ReadFile(argvRecord)
	if err != nil {
		t.Fatalf("reading fake aws argv record: %v", err)
	}
	if !strings.Contains(string(argv), scriptMarker) {
		t.Errorf("ssm send-command argv = %q, want it to contain the script text %q — the ssm driver's script genuinely reaches argv, unlike the ssh driver's", string(argv), scriptMarker)
	}
	kittest.AssertNoSecret(t, "ssm argv", string(argv), leakPEM)
}
