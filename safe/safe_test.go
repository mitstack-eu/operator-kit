package safe_test

import (
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/safe"
)

func allow() safe.Allowlist { return safe.NewAllowlist("api.github.com", "ghe.corp.example") }

func TestTrustedBase_ReturnsTheAllowlistEntryNotTheInput(t *testing.T) {
	b, err := safe.TrustedBase("https://API.GitHub.com/", allow())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.String() != "https://api.github.com" {
		t.Errorf("Base = %q, want the canonical allowlist entry", b.String())
	}
}

func TestTrustedBase_RefusesAHostNotOnTheAllowlist(t *testing.T) {
	_, err := safe.TrustedBase("https://evil.example.com", allow())
	if err == nil {
		t.Fatal("a host the operator never allowlisted must be refused")
	}
	if !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("the refusal should say why: %v", err)
	}
}

func TestTrustedBase_RefusesALookalikeHost(t *testing.T) {
	for _, endpoint := range []string{
		"https://api.github.com.evil.example",
		"https://evil.example/api.github.com",
		"https://api.github.com@evil.example",
		"https://notapi.github.com",
	} {
		if _, err := safe.TrustedBase(endpoint, allow()); err == nil {
			t.Errorf("TrustedBase(%q) succeeded; a lookalike must be refused", endpoint)
		}
	}
}

func TestTrustedBase_RefusesPlainHTTP(t *testing.T) {
	if _, err := safe.TrustedBase("http://api.github.com", allow()); err == nil {
		t.Error("a non-https base must be refused — the token would travel in clear")
	}
}

func TestTrustedBase_RefusesGarbage(t *testing.T) {
	for _, endpoint := range []string{"", "   ", "not a url", "///", "https://"} {
		if _, err := safe.TrustedBase(endpoint, allow()); err == nil {
			t.Errorf("TrustedBase(%q) succeeded; want an error", endpoint)
		}
	}
}

func TestTrustedBase_AnEnterpriseHostOptsInViaTheAllowlistOnly(t *testing.T) {
	if _, err := safe.TrustedBase("https://ghe.corp.example", allow()); err != nil {
		t.Errorf("an allowlisted enterprise host must be accepted: %v", err)
	}
	if _, err := safe.TrustedBase("https://ghe.corp.example", safe.NewAllowlist("api.github.com")); err == nil {
		t.Error("the same host must be refused when the operator did not allowlist it")
	}
}

func TestBase_ZeroValueBuildsNoURL(t *testing.T) {
	var b safe.Base
	if !b.IsZero() {
		t.Error("the zero Base must report IsZero")
	}
	if b.URL("/repos/x/y") != "" {
		t.Errorf("the zero Base must build no URL, got %q", b.URL("/repos/x/y"))
	}
}

func TestBase_URLJoinsCleanly(t *testing.T) {
	b, _ := safe.TrustedBase("https://api.github.com", allow())
	for _, p := range []string{"repos/x/y", "/repos/x/y"} {
		if got := b.URL(p); got != "https://api.github.com/repos/x/y" {
			t.Errorf("URL(%q) = %q", p, got)
		}
	}
}

func TestNewAllowlist_IgnoresEmptyAndDuplicateEntries(t *testing.T) {
	a := safe.NewAllowlist("api.github.com", "", "  ", "API.GITHUB.COM", "ghe.corp.example")
	if len(a.Hosts()) != 2 {
		t.Errorf("Hosts() = %v, want the two distinct hosts", a.Hosts())
	}
}

func TestLogValue_RemovesCRAndLF(t *testing.T) {
	got := safe.LogValue("first line\ninjected: ERROR fake\r\nmore")
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("LogValue left a newline in %q — a forged log line is possible", got)
	}
}

func TestLogValue_RemovesResidualControlCharacters(t *testing.T) {
	got := safe.LogValue("text\x00with\x1bcontrol\x7fchars")
	for _, r := range got {
		if r < 0x20 && r != ' ' || r == 0x7f {
			t.Errorf("LogValue left control char %q in %q", r, got)
		}
	}
}

// A newline becomes a space while a control character is deleted, which pins CR/LF removal as its own step.
func TestLogValue_StripsCRLFBeforeControlCharacters(t *testing.T) {
	got := safe.LogValue("a\nb\x00c")
	if got != "a b"+"c" {
		t.Errorf("LogValue(%q) = %q, want %q — newline replaced by a space, control char deleted",
			"a\\nb\\x00c", got, "a bc")
	}
}

func TestLogValue_LeavesOrdinaryTextAlone(t *testing.T) {
	const s = "provisioning instance i-abc123 in eu-central-1"
	if got := safe.LogValue(s); got != s {
		t.Errorf("LogValue mangled ordinary text: %q", got)
	}
}

func TestScrub_RedactsEverySecretOccurrence(t *testing.T) {
	got := safe.Scrub("token=ghp_secretvalue and again ghp_secretvalue", "ghp_secretvalue")
	if strings.Contains(got, "ghp_secretvalue") {
		t.Errorf("Scrub left a secret in %q", got)
	}
	if strings.Count(got, "[REDACTED]") != 2 {
		t.Errorf("Scrub = %q, want both occurrences redacted", got)
	}
}

func TestScrub_DoesNotSanitiseNewlines(t *testing.T) {
	got := safe.Scrub("secret\nvalue", "secret")
	if !strings.Contains(got, "\n") {
		t.Error("Scrub must NOT remove newlines — that is LogValue's job, and conflating them leaves one hole open")
	}
}

func TestLogValue_DoesNotScrubSecrets(t *testing.T) {
	got := safe.LogValue("token=ghp_secretvalue")
	if !strings.Contains(got, "ghp_secretvalue") {
		t.Error("LogValue must NOT remove secrets — that is Scrub's job")
	}
}

func TestScrub_IgnoresSecretsTooShortToRedactSafely(t *testing.T) {
	if got := safe.Scrub("the instance is up", "up"); got != "the instance is up" {
		t.Errorf("Scrub = %q, want ordinary text untouched", got)
	}
}

func TestLogSafe_ComposesBothInTheRightOrder(t *testing.T) {
	got := safe.LogSafe("auth failed: token=ghp_secretvalue\nERROR forged", "ghp_secretvalue")
	if strings.Contains(got, "ghp_secretvalue") {
		t.Errorf("LogSafe leaked the secret: %q", got)
	}
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("LogSafe left a newline: %q", got)
	}
}

func TestScrub_RedactsAWebIdentityTokenInAnSTSError(t *testing.T) {
	token := "eyJhbGciOiJSUzI1NiIsImtpZCI6ImZpeHR1cmUifQ" + "." +
		"eyJzdWIiOiJzeXN0ZW06c2VydmljZWFjY291bnQ6bWl0c3RhY2s6Zml4dHVyZSIsImF1ZCI6InN0cy5hbWF6b25hd3MuY29tIn0" + "." +
		"Zml4dHVyZS1zaWduYXR1cmU"
	msg := "operation error STS: AssumeRoleWithWebIdentity, https response error StatusCode: 400, RequestID: 0000, " +
		"InvalidIdentityToken: Incorrect token audience in " + token + " (WebIdentityToken=" + token + ")"
	want := "operation error STS: AssumeRoleWithWebIdentity, https response error StatusCode: 400, RequestID: 0000, " +
		"InvalidIdentityToken: Incorrect token audience in [REDACTED] (WebIdentityToken=[REDACTED])"

	got := safe.Scrub(msg, token)
	if strings.Contains(got, token) {
		t.Fatalf("Scrub left the web-identity token in %q", got)
	}
	if got != want {
		t.Errorf("Scrub = %q, want %q", got, want)
	}
}
