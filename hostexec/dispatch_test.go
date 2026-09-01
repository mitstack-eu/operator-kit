package hostexec_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/hostexec"
)

// --- Dispatch ----------------------------------------------------------

func TestRun_UnknownKindNamesTheKindInTheError(t *testing.T) {
	h := hostexec.Host{Kind: hostexec.Kind("telnet"), Target: "somehost", Region: "eu-central-1"}
	err := hostexec.Run(context.Background(), h, "true")
	if err == nil {
		t.Fatal("Run must reject a Kind it does not implement rather than silently falling back to a default driver")
	}
	if !strings.Contains(err.Error(), "telnet") {
		t.Errorf("error = %v, want it to name the unrecognised kind so a caller can see what it mistyped", err)
	}
}

func TestRun_EmptyKindNamesTheKindInTheError(t *testing.T) {
	h := hostexec.Host{Target: "somehost", Region: "eu-central-1"}
	err := hostexec.Run(context.Background(), h, "true")
	if err == nil {
		t.Fatal("Run must reject an empty Kind rather than defaulting to one driver")
	}
	if !strings.Contains(err.Error(), `""`) {
		t.Errorf(`error = %v, want it to name the empty kind explicitly (a quoted "") rather than a generic failure`, err)
	}
}

func TestRun_RejectsAnEmptyTarget(t *testing.T) {
	cases := []hostexec.Host{
		{Kind: hostexec.KindSSM, Region: "eu-central-1"},
		{Kind: hostexec.KindSSH},
	}
	for _, h := range cases {
		if err := hostexec.Run(context.Background(), h, "true"); err == nil {
			t.Errorf("Run(%+v) with an empty Target must fail rather than exec against nothing", h)
		}
	}
}

func TestRun_SSMRejectsAnEmptyRegion(t *testing.T) {
	// The kit refuses to default a region: a package default that shifts
	// silently strands whatever already ran under the real one.
	h := hostexec.Host{Kind: hostexec.KindSSM, Target: "i-0123456789abcdef0"}
	if err := hostexec.Run(context.Background(), h, "true"); err == nil {
		t.Error("Run with no Region for the ssm driver must fail rather than guess a default")
	}
}

func TestRun_RejectsATargetBeginningWithADashBeforeSpawningAnySubprocess(t *testing.T) {
	// Neither ssh nor the aws CLI has an end-of-options marker, so a Target
	// starting with "-" is parsed as an option rather than a hostname or
	// instance id — up-front validation is the only defence. Target values
	// originate in block manifests and subscription inputs, so this is
	// caller-controlled, not merely theoretical.
	sshDir, sshArgv, _, _, _ := fakeSSH(t, "exit 0")
	ssmDir, ssmArgv, _ := fakeSSM(t, "cmd-1", []string{"Success"}, 0)

	cases := []struct {
		name string
		dir  string
		argv string
		h    hostexec.Host
	}{
		{"ssh", sshDir, sshArgv, hostexec.Host{Kind: hostexec.KindSSH, Target: "-oProxyCommand=touch /tmp/pwned", Credential: "PEM-DATA"}},
		{"ssm", ssmDir, ssmArgv, hostexec.Host{Kind: hostexec.KindSSM, Target: "-oProxyCommand=touch /tmp/pwned", Region: "eu-central-1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setPath(t, c.dir)
			if err := hostexec.Run(context.Background(), c.h, "true"); err == nil {
				t.Errorf("Run(%+v) with a Target beginning with %q must fail before any subprocess is spawned", c.h, "-")
			}
			if _, statErr := os.Stat(c.argv); !os.IsNotExist(statErr) {
				t.Errorf("the %s stub was invoked despite a Target beginning with %q — validation must happen before any subprocess is spawned", c.name, "-")
			}
		})
	}
}
