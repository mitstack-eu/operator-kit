package hostexec_test

// Neither the script's secret content nor the ssh driver's PEM credential may appear on a command line, in the package's log output or in a returned error; only the child's stdin, invisible to `ps` and logs, may carry them.

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/hostexec"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// The sentinels are low-entropy and not token-shaped so they do not trip a secret scanner on this file.
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

// A captured output is something a caller may log or return to a user, so a secret piped on stdin must not reappear in RunOutput's returned string.
func TestNoSecretLeak_SSHScriptSecretNeverAppearsInReturnedOutput(t *testing.T) {
	dir, _, stdinRecord, _, _ := fakeSSH(t, "cat >/dev/null; printf 'ok\\n'")
	setPath(t, dir)

	script := "export PGPASSWORD=" + leakScriptSecret + "\npsql -c 'select 1'"
	out, err := hostexec.RunOutput(context.Background(), sshHost(leakPEM), script)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The fake never echoes either secret, so a leak here is a driver bug and the assertions check a real return value, not an empty string.
	if out != "ok" {
		t.Fatalf("RunOutput() = %q, want %q — the fixture's own captured output, so the no-leak assertions below are checking something real", out, "ok")
	}

	kittest.AssertNoSecret(t, "ssh RunOutput return value", out, leakScriptSecret)
	kittest.AssertNoSecret(t, "ssh RunOutput return value", out, leakPEM)

	stdin, err := os.ReadFile(stdinRecord)
	if err != nil {
		t.Fatalf("reading fake ssh stdin record: %v", err)
	}
	if !strings.Contains(string(stdin), leakScriptSecret) {
		t.Error("the script secret must arrive over the ssh subprocess's stdin — its absence there means RunOutput never actually ran the script, which would make the no-leak assertion above vacuous")
	}
}

// The ssm driver puts the whole script into "--parameters commands=...", which lands in argv and in CloudTrail, an AWS-RunShellScript property it cannot avoid; a caller must pass a parameter name and fetch the value on the host.
//
// It also checks that Host.Credential, which a caller may populate on a reused Host, never rides along in that exposed argv.
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
