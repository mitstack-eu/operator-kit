// Package ghapi_test pins that the GitHub API host is the operator's decision, never the caller's.
package ghapi_test

import (
	"os"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/ghapi"
)

func TestBase(t *testing.T) {
	type baseCase struct {
		name, endpoint string
		extra          []string
		want           string
	}
	cases := []baseCase{
		{"the legitimate endpoint resolves to the allowlist entry", "https://api.github.com", nil, "https://api.github.com"},
		{"an upper-case endpoint yields the canonical entry, never the caller's casing", "https://API.GitHub.com", nil, "https://api.github.com"},
		{"an all-caps endpoint with a slash yields the canonical entry", "https://API.GITHUB.COM/", nil, "https://api.github.com"},
		{"a trailing slash yields the canonical entry", "https://api.github.com/", nil, "https://api.github.com"},
		{"an operator-supplied enterprise host is accepted", "https://ghe.corp.example", []string{"ghe.corp.example"}, "https://ghe.corp.example"},
		{"an extra host matches whatever its case and position", "https://GHE.Corp.Example", []string{"other.example", "ghe.corp.example"}, "https://ghe.corp.example"},
		{"a host not on the allowlist is refused", "https://evil.example.com", nil, ""},
		{"an enterprise host the operator never supplied is refused", "https://ghe.corp.example", nil, ""},
		{"a lookalike suffix is refused", "https://api.github.com.evil.example", nil, ""},
		{"a lookalike path is refused", "https://evil.example/api.github.com", nil, ""},
		{"a lookalike userinfo is refused", "https://api.github.com@evil.example", nil, ""},
		{"a lookalike prefix is refused", "https://notapi.github.com", nil, ""},
		{"http is refused, since a real token would travel in clear", "http://api.github.com", nil, ""},
		{"an empty endpoint is refused", "", nil, ""},
		{"a blank endpoint is refused", "   ", nil, ""},
		{"a non-URL is refused", "not a url", nil, ""},
		{"a scheme with no host is refused", "https://", nil, ""},
		{"ftp is refused", "ftp://api.github.com", nil, ""},
	}
	for _, host := range []string{"10.0.0.5", "127.0.0.1", "169.254.169.254", "[::1]", "localhost", "metadata.google.internal", "192.168.1.1", "0.0.0.0"} {
		cases = append(cases, baseCase{"the internal or metadata host " + host + " is refused", "https://" + host, nil, ""})
	}
	for _, host := range []string{"10.0.0.5", "127.0.0.1", "169.254.169.254", "[::1]", "localhost", "metadata.google.internal", "0.0.0.0", "8.8.8.8"} {
		cases = append(cases, baseCase{"an operator extra host never reopens the internal, metadata or bare-IP host " + host, "https://" + host, []string{host}, ""})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := ghapi.Base(tc.endpoint, tc.extra...)

			if b.String() != tc.want || (tc.want == "") != (err != nil) {
				t.Errorf("Base(%q, %q) = %q, %v; want %q", tc.endpoint, tc.extra, b.String(), err, tc.want)
			}
		})
	}
}

func TestBaseFromEnv_ResolvesAgainstTheOperatorsEnvAllowlist(t *testing.T) {
	envOf := func(v string) *string { return &v }
	cases := []struct {
		name     string
		env      *string
		endpoint string
		want     string
	}{
		{"unset env accepts api.github.com", nil, "https://api.github.com", "https://api.github.com"},
		{"unset env refuses an enterprise host", nil, "https://ghe.example.com/api/v3", ""},
		{"a listed enterprise host resolves with the path dropped", envOf("ghe.example.com"), "https://ghe.example.com/api/v3", "https://ghe.example.com"},
		{"entries are trimmed and lower-cased", envOf(" GHE.Example.com , other.example "), "https://ghe.example.com", "https://ghe.example.com"},
		{"a second trimmed entry is honoured", envOf(" GHE.Example.com , other.example "), "https://other.example", "https://other.example"},
		{"an extra host never removes api.github.com", envOf("ghe.example.com"), "https://api.github.com", "https://api.github.com"},
		{"empty entries refuse an enterprise host", envOf(",, ,"), "https://ghe.example.com/api/v3", ""},
		{"empty entries still accept api.github.com", envOf(",, ,"), "https://api.github.com", "https://api.github.com"},
		{"an entry carrying a scheme is not a host", envOf("https://ghe.example.com"), "https://ghe.example.com", ""},
		{"a listed metadata IP is refused", envOf("169.254.169.254"), "https://169.254.169.254", ""},
		{"a listed public IP literal is refused", envOf("8.8.8.8"), "https://8.8.8.8", ""},
		{"a listed localhost is refused", envOf("localhost"), "https://localhost", ""},
		{"a listed metadata hostname is refused", envOf("metadata.google.internal"), "https://metadata.google.internal", ""},
		{"a listed host over http is refused", envOf("ghe.example.com"), "http://ghe.example.com", ""},
		{"unset env has no loopback carve-out", nil, "http://127.0.0.1:8080", ""},
		{"a listed loopback has no http carve-out", envOf("127.0.0.1:8080"), "http://127.0.0.1:8080", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ghapi.HostsEnv, "")
			if tc.env == nil {
				if err := os.Unsetenv(ghapi.HostsEnv); err != nil {
					t.Fatalf("unset %s: %v", ghapi.HostsEnv, err)
				}
			} else {
				t.Setenv(ghapi.HostsEnv, *tc.env)
			}

			b, err := ghapi.BaseFromEnv(tc.endpoint)

			if b.String() != tc.want || (tc.want == "") != (err != nil) || (err != nil && !b.IsZero()) {
				t.Errorf("BaseFromEnv(%q) with %s=%v = %q, %v; want %q, and a zero Base with any error", tc.endpoint, ghapi.HostsEnv, tc.env, b.String(), err, tc.want)
			}
		})
	}
}
