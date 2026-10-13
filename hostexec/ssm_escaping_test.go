package hostexec_test

// The aws CLI shorthand parser decodes no unicode escape, so the ssm driver must send <, > and & literally, never HTML-escaped.

import (
	"context"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/hostexec"
)

func TestSSMRun_ScriptWithRedirectsAndAmpersandsRoundTripsThroughCommandsJSONLiterally(t *testing.T) {
	shrinkSSMPolling(t)
	dir, argvRecord := fakeSSMCapturingRawArgv(t, "cmd-1")
	setPath(t, dir)

	script := "docker exec x pg_isready >/tmp/o 2>&1 && echo ok\nline with < and &"

	if err := hostexec.Run(context.Background(), ssmHost(), script); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw := rawCommandsParameterValue(t, argvRecord)
	for _, want := range []string{">", "<", "&"} {
		if !strings.Contains(raw, want) {
			t.Errorf("--parameters commands=... value = %q, want it to contain the literal character %q", raw, want)
		}
	}
	for _, bad := range []string{`\u003e`, `\u003c`, `\u0026`} {
		if strings.Contains(raw, bad) {
			t.Errorf("--parameters commands=... value = %q, must not HTML-escape to %q — the aws CLI shorthand parser does not decode \\u escapes", raw, bad)
		}
	}

	got := decodedCommandsParameter(t, argvRecord)
	want := strings.Split(script, "\n")
	if !stringSlicesEqual(got, want) {
		t.Errorf("decoded commands = %#v, want %#v — the round trip must reproduce the script exactly, not just share a substring", got, want)
	}
}

func TestSSMRunOutput_ScriptWithRedirectsAndAmpersandsRoundTripsThroughCommandsJSONLiterally(t *testing.T) {
	// RunOutput shares sendSSMCommand with Run, so the same constraint applies.
	shrinkSSMPolling(t)
	dir, argvRecord := fakeSSMCapturingRawArgv(t, "cmd-1")
	setPath(t, dir)

	script := "docker exec x pg_isready >/tmp/o 2>&1 && echo ok\nline with < and &"

	if _, err := hostexec.RunOutput(context.Background(), ssmHost(), script); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw := rawCommandsParameterValue(t, argvRecord)
	for _, want := range []string{">", "<", "&"} {
		if !strings.Contains(raw, want) {
			t.Errorf("--parameters commands=... value = %q, want it to contain the literal character %q", raw, want)
		}
	}
	for _, bad := range []string{`\u003e`, `\u003c`, `\u0026`} {
		if strings.Contains(raw, bad) {
			t.Errorf("--parameters commands=... value = %q, must not HTML-escape to %q — the aws CLI shorthand parser does not decode \\u escapes", raw, bad)
		}
	}

	got := decodedCommandsParameter(t, argvRecord)
	want := strings.Split(script, "\n")
	if !stringSlicesEqual(got, want) {
		t.Errorf("decoded commands = %#v, want %#v — the round trip must reproduce the script exactly, not just share a substring", got, want)
	}
}

func TestSSMRun_QuoteAndBackslashStillEscapeCorrectlyAndMultilineStillSplitsOneElementPerLine(t *testing.T) {
	// The two escapes the CLI does understand still arrive JSON-escaped, and a multi-line script is one array element per line.
	shrinkSSMPolling(t)
	dir, argvRecord := fakeSSMCapturingRawArgv(t, "cmd-1")
	setPath(t, dir)

	script := `echo "hello world"` + "\n" + `echo C:\Users\deploy`

	if err := hostexec.Run(context.Background(), ssmHost(), script); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw := rawCommandsParameterValue(t, argvRecord)
	if !strings.Contains(raw, `\"`) {
		t.Errorf(`--parameters commands=... value = %q, want it to still carry an escaped double quote (\") for the line %q`, raw, `echo "hello world"`)
	}
	if !strings.Contains(raw, `\\`) {
		t.Errorf(`--parameters commands=... value = %q, want it to still carry an escaped backslash (\\) for the line %q`, raw, `echo C:\Users\deploy`)
	}

	got := decodedCommandsParameter(t, argvRecord)
	if len(got) != 2 {
		t.Fatalf("decoded commands = %#v, want exactly 2 elements — one per line of the multi-line script", got)
	}
	want := strings.Split(script, "\n")
	if !stringSlicesEqual(got, want) {
		t.Errorf("decoded commands = %#v, want %#v", got, want)
	}
}
