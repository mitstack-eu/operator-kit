package safe_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/safe"
)

func allow() safe.Allowlist { return safe.NewAllowlist("api.github.com", "ghe.corp.example") }

func TestTrustedBase(t *testing.T) {
	cases := []struct {
		name, endpoint string
		allow          safe.Allowlist
		want           string
	}{
		{"it returns the canonical allowlist entry, not the input", "https://API.GitHub.com/", allow(), "https://api.github.com"},
		{"an allowlisted enterprise host is accepted", "https://ghe.corp.example", allow(), "https://ghe.corp.example"},
		{"a host the operator never allowlisted is refused", "https://evil.example.com", allow(), ""},
		{"the enterprise host is refused when the operator did not allowlist it", "https://ghe.corp.example", safe.NewAllowlist("api.github.com"), ""},
		{"a lookalike suffix is refused", "https://api.github.com.evil.example", allow(), ""},
		{"a lookalike path is refused", "https://evil.example/api.github.com", allow(), ""},
		{"a lookalike userinfo is refused", "https://api.github.com@evil.example", allow(), ""},
		{"a lookalike prefix is refused", "https://notapi.github.com", allow(), ""},
		{"plain http is refused, since the token would travel in clear", "http://api.github.com", allow(), ""},
		{"an empty endpoint is refused", "", allow(), ""},
		{"a blank endpoint is refused", "   ", allow(), ""},
		{"a non-URL is refused", "not a url", allow(), ""},
		{"slashes alone are refused", "///", allow(), ""},
		{"a scheme with no host is refused", "https://", allow(), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := safe.TrustedBase(tc.endpoint, tc.allow)

			switch {
			case tc.want != "" && (err != nil || b.String() != tc.want):
				t.Errorf("TrustedBase(%q) = %q, %v; want %q", tc.endpoint, b, err, tc.want)
			case tc.want == "" && err == nil:
				t.Errorf("TrustedBase(%q) = %q; want a refusal", tc.endpoint, b)
			}
		})
	}
}

func TestTrustedBase_TheRefusalSaysTheHostIsNotAllowlisted(t *testing.T) {
	_, err := safe.TrustedBase("https://evil.example.com", allow())

	if err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("TrustedBase(evil) error = %v, want it to name the allowlist", err)
	}
}

func TestBase_URL(t *testing.T) {
	var zero safe.Base
	b, err := safe.TrustedBase("https://api.github.com", allow())
	if err != nil {
		t.Fatalf("TrustedBase: %v", err)
	}

	if !zero.IsZero() || zero.URL("/repos/x/y") != "" {
		t.Errorf("zero Base: IsZero %v, URL %q; want true and no URL", zero.IsZero(), zero.URL("/repos/x/y"))
	}
	for _, p := range []string{"repos/x/y", "/repos/x/y"} {
		if got := b.URL(p); got != "https://api.github.com/repos/x/y" {
			t.Errorf("URL(%q) = %q, want https://api.github.com/repos/x/y", p, got)
		}
	}
}

func TestNewAllowlist_IgnoresEmptyAndDuplicateEntries(t *testing.T) {
	a := safe.NewAllowlist("api.github.com", "", "  ", "API.GITHUB.COM", "ghe.corp.example")

	if got, want := a.Hosts(), []string{"api.github.com", "ghe.corp.example"}; !slices.Equal(got, want) {
		t.Errorf("Hosts() = %q, want %q", got, want)
	}
}

func TestSanitising(t *testing.T) {
	stsToken := "eyJhbGciOiJSUzI1NiIsImtpZCI6ImZpeHR1cmUifQ" + "." +
		"eyJzdWIiOiJzeXN0ZW06c2VydmljZWFjY291bnQ6bWl0c3RhY2s6Zml4dHVyZSIsImF1ZCI6InN0cy5hbWF6b25hd3MuY29tIn0" + "." +
		"Zml4dHVyZS1zaWduYXR1cmU"
	stsPrefix := "operation error STS: AssumeRoleWithWebIdentity, https response error StatusCode: 400, RequestID: 0000, "
	cases := []struct {
		name    string
		fn      func(string, ...string) string
		in      string
		secrets []string
		want    string
	}{
		{"LogValue turns CR and LF into spaces so no log line can be forged", logValue, "first line\ninjected: ERROR fake\r\nmore", nil, "first line injected: ERROR fake  more"},
		{"LogValue deletes residual control characters", logValue, "text\x00with\x1bcontrol\x7fchars", nil, "textwithcontrolchars"},
		{"LogValue replaces a newline before it deletes a control character", logValue, "a\nb\x00c", nil, "a bc"},
		{"LogValue leaves ordinary text alone", logValue, "provisioning instance i-abc123 in eu-central-1", nil, "provisioning instance i-abc123 in eu-central-1"},
		{"LogValue does not scrub secrets, which is Scrub's job", logValue, "token=ghp_secretvalue", nil, "token=ghp_secretvalue"},
		{"Scrub redacts every occurrence of a secret", safe.Scrub, "token=ghp_secretvalue and again ghp_secretvalue", []string{"ghp_secretvalue"}, "token=[REDACTED] and again [REDACTED]"},
		{"Scrub keeps newlines, which is LogValue's job", safe.Scrub, "secret\nvalue", []string{"secret"}, "[REDACTED]\nvalue"},
		{"Scrub ignores a secret too short to redact safely", safe.Scrub, "the instance is up", []string{"up"}, "the instance is up"},
		{"Scrub redacts a web-identity token inside an STS error", safe.Scrub,
			stsPrefix + "InvalidIdentityToken: Incorrect token audience in " + stsToken + " (WebIdentityToken=" + stsToken + ")", []string{stsToken},
			stsPrefix + "InvalidIdentityToken: Incorrect token audience in [REDACTED] (WebIdentityToken=[REDACTED])"},
		{"LogSafe scrubs the secret and then removes the newline", safe.LogSafe, "auth failed: token=ghp_secretvalue\nERROR forged", []string{"ghp_secretvalue"}, "auth failed: token=[REDACTED] ERROR forged"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.fn(tc.in, tc.secrets...)

			if got != tc.want {
				t.Errorf("(%q, %q) = %q, want %q", tc.in, tc.secrets, got, tc.want)
			}
		})
	}
}

func logValue(s string, _ ...string) string { return safe.LogValue(s) }
