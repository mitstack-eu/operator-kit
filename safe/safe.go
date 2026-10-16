// Package safe, part of the core every block needs, holds the security invariants every operator must honour: the operator picks the API host, and log sanitising and secret scrubbing are separate steps.
package safe

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Base is a host the operator is willing to talk to; the URL builders accept only a Base, so a request-supplied string cannot become an outbound URL without a visible conversion.
type Base struct {
	scheme string
	host   string
}

// String renders the base as a URL prefix, e.g. "https://api.github.com".
func (b Base) String() string {
	if b.host == "" {
		return ""
	}
	return b.scheme + "://" + b.host
}

// IsZero reports whether the base was never resolved.
func (b Base) IsZero() bool { return b.host == "" }

// URL joins a path onto the base, taking the host from the allowlist entry and never from the caller-supplied endpoint.
func (b Base) URL(path string) string {
	if b.IsZero() {
		return ""
	}
	return b.String() + "/" + strings.TrimLeft(path, "/")
}

// Allowlist is the set of hosts an operator will talk to, which must come from constants or operator configuration, never a request.
type Allowlist struct {
	hosts []string
}

// NewAllowlist builds an allowlist from operator-owned values, lower-cased and de-duplicated with empty ones dropped.
func NewAllowlist(hosts ...string) Allowlist {
	seen := map[string]bool{}
	var out []string
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	sort.Strings(out)
	return Allowlist{hosts: out}
}

// Hosts returns the allowlisted hosts, for diagnostics.
func (a Allowlist) Hosts() []string { return append([]string(nil), a.hosts...) }

// TrustedBase returns a Base built from the allowlist entry, never from the endpoint, which is only compared (building from it is an SSRF).
func TrustedBase(endpoint string, allow Allowlist) (Base, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(endpoint), "/"))
	if err != nil || u.Host == "" {
		return Base{}, fmt.Errorf("safe: invalid api base %q", endpoint)
	}
	if u.Scheme != "https" {
		return Base{}, fmt.Errorf("safe: api base %q must be https", endpoint)
	}

	host := strings.ToLower(u.Host)
	for _, allowed := range allow.hosts {
		if host == allowed {
			return Base{scheme: "https", host: allowed}, nil
		}
	}
	return Base{}, fmt.Errorf("safe: api base %q is not in this operator's allowlist %v — the operator decides the host, not the caller (MIT-1325)",
		endpoint, allow.hosts)
}

var controlCharPattern = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// LogValue makes a request-derived value safe to log; CR and LF go by explicit ReplaceAll first, since a single strings.Map pass is not a go/log-injection barrier.
func LogValue(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return controlCharPattern.ReplaceAllString(s, "")
}

// Scrub replaces every occurrence of each secret with a redaction marker, and does not remove newlines; compose it with LogValue rather than substituting one for the other.
func Scrub(s string, secrets ...string) string {
	for _, sec := range secrets {
		if len(sec) < 4 {
			continue
		}
		s = strings.ReplaceAll(s, sec, "[REDACTED]")
	}
	return s
}

// LogSafe scrubs the secrets, then sanitises the result for the logger, in that order.
func LogSafe(s string, secrets ...string) string {
	return LogValue(Scrub(s, secrets...))
}
