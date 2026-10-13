// Package ghapi_test pins the host-resolution contract of kit/ghapi:
// the GitHub API host is the OPERATOR's decision, never the caller's,
// extracted once from the six near-duplicate urlguard.go copies
// (examples/web-app-aws/app-scaffold/urlguard.go is the reference).
package ghapi_test

import (
	"os"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/ghapi"
)

// --- The legitimate case: resolves to the allowlist entry -------------------

func TestBase_ResolvesLegitimateEndpointToTheAllowlistEntry(t *testing.T) {
	b, err := ghapi.Base("https://api.github.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.String() != "https://api.github.com" {
		t.Errorf("Base() = %q, want the canonical allowlist entry", b.String())
	}
}

func TestBase_ReturnsTheAllowlistEntryNeverTheCallerHost(t *testing.T) {
	// The whole point: a request-supplied endpoint may
	// only be COMPARED, never propagated into the returned Base. Vary the
	// caller's casing/trailing-slash and prove the output is byte-identical
	// every time — it must be built from the constant, not from whatever the
	// caller happened to send.
	for _, endpoint := range []string{
		"https://api.github.com",
		"https://API.GitHub.com",
		"https://API.GITHUB.COM/",
		"https://api.github.com/",
	} {
		b, err := ghapi.Base(endpoint)
		if err != nil {
			t.Fatalf("Base(%q): %v", endpoint, err)
		}
		if b.String() != "https://api.github.com" {
			t.Errorf("Base(%q).String() = %q, want the canonical constant", endpoint, b.String())
		}
		if strings.Contains(b.String(), "API") {
			t.Errorf("Base(%q) leaked the caller's original casing: %q", endpoint, b.String())
		}
	}
}

// --- Hostile / lookalike endpoints -------------------------------------------

func TestBase_RefusesAHostileEndpoint(t *testing.T) {
	if _, err := ghapi.Base("https://evil.example.com"); err == nil {
		t.Fatal("a host not on the operator's allowlist must be refused")
	}
}

func TestBase_RefusesALookalikeHost(t *testing.T) {
	// A host that merely CONTAINS api.github.com as a substring or subdomain
	// must never be treated as the real thing.
	for _, endpoint := range []string{
		"https://api.github.com.evil.example",
		"https://evil.example/api.github.com",
		"https://api.github.com@evil.example",
		"https://notapi.github.com",
	} {
		if _, err := ghapi.Base(endpoint); err == nil {
			t.Errorf("Base(%q) succeeded; a lookalike host must be refused", endpoint)
		}
	}
}

func TestBase_RefusesNonHTTPS(t *testing.T) {
	if _, err := ghapi.Base("http://api.github.com"); err == nil {
		t.Error("a non-https base must be refused — a real installation token would travel in clear")
	}
}

func TestBase_RefusesGarbage(t *testing.T) {
	for _, endpoint := range []string{"", "   ", "not a url", "https://", "ftp://api.github.com"} {
		if _, err := ghapi.Base(endpoint); err == nil {
			t.Errorf("Base(%q) succeeded; want an error", endpoint)
		}
	}
}

// --- Operator-supplied extra hosts (GitHub Enterprise) -----------------------

func TestBase_AcceptsAnOperatorSuppliedExtraHost(t *testing.T) {
	// A GitHub Enterprise deployment opts in via operator CONFIG
	// (MITSTACK_GITHUB_API_HOSTS in the real operator), never via a payload.
	b, err := ghapi.Base("https://ghe.corp.example", "ghe.corp.example")
	if err != nil {
		t.Fatalf("an operator-allowlisted enterprise host must be accepted: %v", err)
	}
	if b.String() != "https://ghe.corp.example" {
		t.Errorf("Base() = %q, want %q", b.String(), "https://ghe.corp.example")
	}
}

func TestBase_RefusesAHostTheOperatorDidNotSupply(t *testing.T) {
	// The exact same host, but this call carries no extraHosts — must be
	// refused. A payload can never assert its own enterprise host.
	if _, err := ghapi.Base("https://ghe.corp.example"); err == nil {
		t.Error("a host the operator never configured must be refused")
	}
}

func TestBase_ExtraHostIsCaseInsensitiveAndOrderIndependent(t *testing.T) {
	b, err := ghapi.Base("https://GHE.Corp.Example", "other.example", "ghe.corp.example")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.String() != "https://ghe.corp.example" {
		t.Errorf("Base() = %q", b.String())
	}
}

// --- Private / loopback / link-local / metadata hosts ------------------------

func TestBase_RefusesPrivateLoopbackLinkLocalAndMetadataEndpoints(t *testing.T) {
	// None of these are on the default allowlist, so each must fail — this
	// pins the baseline before the stronger claim below.
	for _, endpoint := range []string{
		"https://10.0.0.5",
		"https://127.0.0.1",
		"https://169.254.169.254",
		"https://[::1]",
		"https://localhost",
		"https://metadata.google.internal",
		"https://192.168.1.1",
		"https://0.0.0.0",
	} {
		if _, err := ghapi.Base(endpoint); err == nil {
			t.Errorf("Base(%q) succeeded; want an error", endpoint)
		}
	}
}

func TestBase_AnOperatorExtraHostCanNeverReopenAnInternalOrMetadataAddress(t *testing.T) {
	// The stronger claim, mirroring urlguard's "an allowlist entry can never
	// re-open an internal address": even when the operator's OWN config names
	// one of these as an extra host, Base must still refuse it. A typo or a
	// compromised env var must not be able to point a real installation
	// token at cloud metadata or an internal address.
	cases := []struct{ host, endpoint string }{
		{"10.0.0.5", "https://10.0.0.5"},
		{"127.0.0.1", "https://127.0.0.1"},
		{"169.254.169.254", "https://169.254.169.254"},
		{"[::1]", "https://[::1]"},
		{"localhost", "https://localhost"},
		{"metadata.google.internal", "https://metadata.google.internal"},
		{"0.0.0.0", "https://0.0.0.0"},
		// A bare IP literal is refused even when it is a perfectly public
		// address — only hostnames are an acceptable GitHub Enterprise shape.
		{"8.8.8.8", "https://8.8.8.8"},
	}
	for _, c := range cases {
		if _, err := ghapi.Base(c.endpoint, c.host); err == nil {
			t.Errorf("Base(%q, extraHosts=%q) succeeded; an operator config value must not reopen an internal/metadata/bare-IP host", c.endpoint, c.host)
		}
	}
}

// --- BaseFromEnv: extra hosts come from MITSTACK_GITHUB_API_HOSTS ------------

func TestBaseFromEnv_ResolvesAgainstTheOperatorsEnvAllowlist(t *testing.T) {
	const envVar = "MITSTACK_GITHUB_API_HOSTS"
	cases := []struct {
		name     string
		env      *string
		endpoint string
		want     string
	}{
		{name: "unset env accepts api.github.com", endpoint: "https://api.github.com", want: "https://api.github.com"},
		{name: "unset env refuses an enterprise host", endpoint: "https://ghe.example.com/api/v3"},
		{name: "listed enterprise host resolves with the path dropped", env: envOf("ghe.example.com"), endpoint: "https://ghe.example.com/api/v3", want: "https://ghe.example.com"},
		{name: "entries are trimmed and lower-cased", env: envOf(" GHE.Example.com , other.example "), endpoint: "https://ghe.example.com", want: "https://ghe.example.com"},
		{name: "second trimmed entry is honoured", env: envOf(" GHE.Example.com , other.example "), endpoint: "https://other.example", want: "https://other.example"},
		{name: "an extra host never removes api.github.com", env: envOf("ghe.example.com"), endpoint: "https://api.github.com", want: "https://api.github.com"},
		{name: "empty entries refuse an enterprise host", env: envOf(",, ,"), endpoint: "https://ghe.example.com/api/v3"},
		{name: "empty entries still accept api.github.com", env: envOf(",, ,"), endpoint: "https://api.github.com", want: "https://api.github.com"},
		{name: "an entry carrying a scheme is not a host", env: envOf("https://ghe.example.com"), endpoint: "https://ghe.example.com"},
		{name: "listed metadata IP is refused", env: envOf("169.254.169.254"), endpoint: "https://169.254.169.254"},
		{name: "listed public IP literal is refused", env: envOf("8.8.8.8"), endpoint: "https://8.8.8.8"},
		{name: "listed localhost is refused", env: envOf("localhost"), endpoint: "https://localhost"},
		{name: "listed metadata hostname is refused", env: envOf("metadata.google.internal"), endpoint: "https://metadata.google.internal"},
		{name: "listed host over http is refused", env: envOf("ghe.example.com"), endpoint: "http://ghe.example.com"},
		{name: "unset env has no loopback carve-out", endpoint: "http://127.0.0.1:8080"},
		{name: "listed loopback has no http carve-out", env: envOf("127.0.0.1:8080"), endpoint: "http://127.0.0.1:8080"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.env == nil {
				t.Setenv(envVar, "")
				if err := os.Unsetenv(envVar); err != nil {
					t.Fatalf("unset %s: %v", envVar, err)
				}
			} else {
				t.Setenv(envVar, *c.env)
			}
			b, err := ghapi.BaseFromEnv(c.endpoint)
			if c.want == "" {
				if err == nil {
					t.Fatalf("BaseFromEnv(%q) with %s=%s = %q, want an error", c.endpoint, envVar, describeEnv(c.env), b.String())
				}
				if !b.IsZero() {
					t.Errorf("BaseFromEnv(%q) returned a non-zero Base %q alongside its error", c.endpoint, b.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("BaseFromEnv(%q) with %s=%s: unexpected error: %v", c.endpoint, envVar, describeEnv(c.env), err)
			}
			if b.String() != c.want {
				t.Errorf("BaseFromEnv(%q).String() = %q, want %q", c.endpoint, b.String(), c.want)
			}
		})
	}
}

func envOf(v string) *string { return &v }

func describeEnv(v *string) string {
	if v == nil {
		return "<unset>"
	}
	return "\"" + *v + "\""
}
