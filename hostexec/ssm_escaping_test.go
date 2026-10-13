package hostexec_test

// The aws CLI's `--parameters` shorthand parser decodes exactly two escape
// sequences inside a quoted value, \" and \\; it has no notion of a \u
// escape. json.Marshal's default HTML-escaping rewrites '<', '>' and '&' into
// \u003c, \u003e and \u0026, so the CLI passes those six literal characters
// straight through to the host's shell instead of the sign they meant, and
// any script line that redirects output or chains commands with "&&" breaks.
// These tests pin the ssm driver's --parameters commands=... value to carry
// '<', '>' and '&' literally, never HTML-escaped, alongside a decode round
// trip proving the JSON itself still parses back to exactly the original
// script lines — and a regression pin that the two escapes the CLI DOES
// understand, \" and \\, still work, and that a multi-line script still
// becomes one JSON array element per line.

import (
	"context"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/hostexec"
)

func TestSSMRun_ScriptWithRedirectsAndAmpersandsRoundTripsThroughCommandsJSONLiterally(t *testing.T) {
	// The aws CLI shorthand parser decodes only \" and \\, so '>', '<' and
	// '&' must reach --parameters as the literal character, never as
	// \u003e / \u003c / \u0026 — those escapes would otherwise land on the
	// host's bash command line unresolved, breaking the redirect and the
	// "&&" this script depends on.
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
	// RunOutput shares sendSSMCommand with Run, so the same shorthand-parser
	// constraint applies to its send-command call: '>' , '<' and '&' must
	// survive literally, and \u003e / \u003c / \u0026 must never appear.
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
	// Regression pin: disabling HTML-escaping must not touch the two escapes
	// the aws CLI shorthand parser DOES understand. A literal '"' or '\'
	// inside a script line must still arrive JSON-escaped (\" and \\ in the
	// wire text) — that was already correct before this fix and must stay
	// so — and a multi-line script must still become one JSON array element
	// per line.
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
