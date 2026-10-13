package reach_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/reach"
)

type fakeResolver struct {
	addrs map[string][]string
	err   error
}

func (f fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	addrs, ok := f.addrs[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return addrs, nil
}

// poisonResolver fails the test if consulted, proving a malformed domain is refused before any lookup.
type poisonResolver struct{ t *testing.T }

func (p poisonResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	p.t.Fatalf("resolver must not be consulted for a malformed domain, got a lookup for %q", host)
	return nil, nil
}

func TestVerifiedCustomDomain_MatchingAddressVerifies(t *testing.T) {
	resolver := fakeResolver{addrs: map[string][]string{"myapp.example.org": {"203.0.113.10"}}}

	got, err := reach.VerifiedCustomDomain(context.Background(), resolver, "myapp.example.org", []string{"203.0.113.10"})
	if err != nil {
		t.Fatalf("a domain resolving to exactly the expected address must verify, got error: %v", err)
	}
	if got != "myapp.example.org" {
		t.Fatalf("VerifiedCustomDomain() = %q, want the verified domain returned", got)
	}
}

// Round-robin records are ordinary, so one matching address among several must verify.
func TestVerifiedCustomDomain_OneOfMultipleResolvedAddressesMatchingVerifies(t *testing.T) {
	resolver := fakeResolver{addrs: map[string][]string{
		"myapp.example.org": {"198.51.100.1", "203.0.113.10", "198.51.100.2"},
	}}

	got, err := reach.VerifiedCustomDomain(context.Background(), resolver, "myapp.example.org", []string{"203.0.113.10"})
	if err != nil {
		t.Fatalf("one matching address among several resolved must still verify, got error: %v", err)
	}
	if got != "myapp.example.org" {
		t.Fatalf("VerifiedCustomDomain() = %q, want the verified domain returned", got)
	}
}

func TestVerifiedCustomDomain_DifferentAddressIsRefused(t *testing.T) {
	resolver := fakeResolver{addrs: map[string][]string{"myapp.example.org": {"198.51.100.99"}}}

	_, err := reach.VerifiedCustomDomain(context.Background(), resolver, "myapp.example.org", []string{"203.0.113.10"})
	if err == nil {
		t.Fatal("a domain resolving to an address OTHER than the expected one must be refused")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "match") {
		t.Fatalf("error %q must say the resolved address did not match", err.Error())
	}
}

func TestVerifiedCustomDomain_NonResolvingDomainIsRefused(t *testing.T) {
	resolver := fakeResolver{} // empty table => every lookup answers NXDOMAIN

	_, err := reach.VerifiedCustomDomain(context.Background(), resolver, "myapp.example.org", []string{"203.0.113.10"})
	if err == nil {
		t.Fatal("a domain that does not resolve at all must be refused")
	}
}

func TestVerifiedCustomDomain_ResolverErrorIsNeverTreatedAsSuccess(t *testing.T) {
	resolver := fakeResolver{err: errors.New("resolver timeout")}

	got, err := reach.VerifiedCustomDomain(context.Background(), resolver, "myapp.example.org", []string{"203.0.113.10"})
	if err == nil {
		t.Fatalf("a resolver error must never be read as a successful verification, got verified=%q", got)
	}
	if got != "" {
		t.Fatalf("a refused verification must return an empty domain, got %q", got)
	}
}

func TestVerifiedCustomDomain_RefusesEmptyDomain(t *testing.T) {
	if _, err := reach.VerifiedCustomDomain(context.Background(), poisonResolver{t}, "", []string{"203.0.113.10"}); err == nil {
		t.Fatal("an empty domain must be refused")
	}
}

func TestVerifiedCustomDomain_RefusesDomainShapedLikeAURL(t *testing.T) {
	if _, err := reach.VerifiedCustomDomain(context.Background(), poisonResolver{t}, "https://myapp.example.org", []string{"203.0.113.10"}); err == nil {
		t.Fatal("a domain carrying a scheme is not a bare hostname and must be refused")
	}
}

func TestVerifiedCustomDomain_RefusesDomainWithPort(t *testing.T) {
	if _, err := reach.VerifiedCustomDomain(context.Background(), poisonResolver{t}, "myapp.example.org:8080", []string{"203.0.113.10"}); err == nil {
		t.Fatal("a domain carrying a port is not a bare hostname and must be refused")
	}
}

func TestVerifiedCustomDomain_RefusesDomainWithPath(t *testing.T) {
	if _, err := reach.VerifiedCustomDomain(context.Background(), poisonResolver{t}, "myapp.example.org/admin", []string{"203.0.113.10"}); err == nil {
		t.Fatal("a domain carrying a path is not a bare hostname and must be refused")
	}
}

func TestVerifiedCustomDomain_ReturnedDomainAllowsItselfAndSubdomainsViaTrustedAppURL(t *testing.T) {
	resolver := fakeResolver{addrs: map[string][]string{"plain.mitstack.dev": {"203.0.113.10"}}}
	verified, err := reach.VerifiedCustomDomain(context.Background(), resolver, "plain.mitstack.dev", []string{"203.0.113.10"})
	if err != nil {
		t.Fatalf("unexpected error verifying plain.mitstack.dev: %v", err)
	}

	if _, err := reach.TrustedAppURL("https://plain.mitstack.dev/", verified); err != nil {
		t.Fatalf("the verified domain itself must clear TrustedAppURL as an extra suffix: %v", err)
	}
	if _, err := reach.TrustedAppURL("https://api.plain.mitstack.dev/", verified); err != nil {
		t.Fatalf("a subdomain of the verified domain must clear TrustedAppURL as an extra suffix: %v", err)
	}
}

// The extra suffix widens the allowlist by one domain and its real subdomains, never to lookalike hosts.

func TestVerifiedCustomDomain_SuffixTrickery_TrailingAttackerLabelStillRejected(t *testing.T) {
	resolver := fakeResolver{addrs: map[string][]string{"plain.mitstack.dev": {"203.0.113.10"}}}
	verified, err := reach.VerifiedCustomDomain(context.Background(), resolver, "plain.mitstack.dev", []string{"203.0.113.10"})
	if err != nil {
		t.Fatalf("unexpected error verifying plain.mitstack.dev: %v", err)
	}

	if _, err := reach.TrustedAppURL("https://plain.mitstack.dev.attacker.com/", verified); err == nil {
		t.Fatal("a host that merely ENDS with an attacker label must be refused even with the extra suffix in place")
	}
}

func TestVerifiedCustomDomain_SuffixTrickery_NoDotBoundaryPrefixStillRejected(t *testing.T) {
	resolver := fakeResolver{addrs: map[string][]string{"plain.mitstack.dev": {"203.0.113.10"}}}
	verified, err := reach.VerifiedCustomDomain(context.Background(), resolver, "plain.mitstack.dev", []string{"203.0.113.10"})
	if err != nil {
		t.Fatalf("unexpected error verifying plain.mitstack.dev: %v", err)
	}

	if _, err := reach.TrustedAppURL("https://evilplain.mitstack.dev/", verified); err == nil {
		t.Fatal("a host that is string-suffixed with no dot boundary must be refused even with the extra suffix in place")
	}
}

func TestVerifiedCustomDomain_SuffixTrickery_HostInPathStillRejected(t *testing.T) {
	resolver := fakeResolver{addrs: map[string][]string{"plain.mitstack.dev": {"203.0.113.10"}}}
	verified, err := reach.VerifiedCustomDomain(context.Background(), resolver, "plain.mitstack.dev", []string{"203.0.113.10"})
	if err != nil {
		t.Fatalf("unexpected error verifying plain.mitstack.dev: %v", err)
	}

	if _, err := reach.TrustedAppURL("https://attacker.com/plain.mitstack.dev", verified); err == nil {
		t.Fatal("the verified domain appearing only in the PATH of a foreign host must be refused")
	}
}

func TestVerifiedCustomDomain_SuffixTrickery_UserinfoTrickStillRejected(t *testing.T) {
	resolver := fakeResolver{addrs: map[string][]string{"plain.mitstack.dev": {"203.0.113.10"}}}
	verified, err := reach.VerifiedCustomDomain(context.Background(), resolver, "plain.mitstack.dev", []string{"203.0.113.10"})
	if err != nil {
		t.Fatalf("unexpected error verifying plain.mitstack.dev: %v", err)
	}

	if _, err := reach.TrustedAppURL("https://plain.mitstack.dev@attacker.com/", verified); err == nil {
		t.Fatal("the verified domain used as USERINFO ahead of a foreign host must be refused")
	}
}

// Verification is per call: a cached domain would let an unrelated app inherit its trust.
func TestVerifiedCustomDomain_DoesNotBecomeGloballyTrusted(t *testing.T) {
	resolver := fakeResolver{addrs: map[string][]string{"plain.mitstack.dev": {"203.0.113.10"}}}
	verified, err := reach.VerifiedCustomDomain(context.Background(), resolver, "plain.mitstack.dev", []string{"203.0.113.10"})
	if err != nil {
		t.Fatalf("unexpected error verifying plain.mitstack.dev: %v", err)
	}
	if _, err := reach.TrustedAppURL("https://plain.mitstack.dev/", verified); err != nil {
		t.Fatalf("the verified domain must be trusted when explicitly passed as an extra suffix: %v", err)
	}

	if _, err := reach.TrustedAppURL("https://plain.mitstack.dev/"); err == nil {
		t.Fatal("a verified domain must not remain trusted once its extra suffix is omitted from the call — there is no global allowlist")
	}
}
