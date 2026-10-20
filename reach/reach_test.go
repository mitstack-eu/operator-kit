package reach_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/reach"
)

const (
	app = "https://app.example/"
	box = "https://52.57.246.147/"
)

// fakeTransport records every URL dialled, the one observable that tells a refusal from a failed connection, and answers status or err; with dnsNames a hostname fails to resolve while an IP gets err.
type fakeTransport struct {
	status   int
	err      error
	dnsNames bool
	dialled  []string
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.dialled = append(f.dialled, req.URL.String())
	if host := req.URL.Hostname(); f.dnsNames && net.ParseIP(host) == nil {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{StatusCode: f.status, Body: http.NoBody, Header: http.Header{}}, nil
}

func TestKeys_AreTheAdvertisedSortedContract(t *testing.T) {
	want := []string{"appHostname", "appUrl", "certTrusted", "publicIp", "reachable"}

	got := reach.Keys()

	if !slices.Equal(got, want) {
		t.Errorf("Keys() = %v, want %v, sorted so a parity comparison is stable", got, want)
	}
}

func TestApply(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]string
		res  reach.Result
		want map[string]string
	}{
		{"it always writes reachable and certTrusted, and no appHostname when the probed URL served",
			map[string]string{"appUrl": app}, reach.Result{Reachable: true, CertTrusted: true, ServingURL: app},
			map[string]string{"appUrl": app, "reachable": "true", "certTrusted": "true"}},
		{"a verdict of not serving writes false rather than omitting, so absent never means unprobed",
			map[string]string{"appUrl": app}, reach.Result{},
			map[string]string{"appUrl": app, "reachable": "false", "certTrusted": "false"}},
		{"a fallback advertises what answered and preserves the intended hostname",
			map[string]string{"appUrl": app, "publicIp": "203.0.113.9"}, reach.Result{Reachable: true, ServingURL: "https://203.0.113.9/"},
			map[string]string{"appUrl": "https://203.0.113.9/", "appHostname": app, "publicIp": "203.0.113.9", "reachable": "true", "certTrusted": "false"}},
		{"a promotion advertises the hostname again and leaves no stale appHostname",
			map[string]string{"appUrl": box, "appHostname": app, "publicIp": "52.57.246.147"}, reach.Result{Reachable: true, CertTrusted: true, ServingURL: app},
			map[string]string{"appUrl": app, "publicIp": "52.57.246.147", "reachable": "true", "certTrusted": "true"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := maps.Clone(tc.in)

			got := reach.Apply(tc.in, tc.res)

			if !maps.Equal(got, tc.want) {
				t.Errorf("Apply(%v, %+v) = %v, want %v", tc.in, tc.res, got, tc.want)
			}
			if !maps.Equal(tc.in, before) {
				t.Errorf("Apply mutated its argument: %v, was %v", tc.in, before)
			}
		})
	}
}

func TestEvaluate_WithAnInjectedProbe(t *testing.T) {
	dns := reach.Result{DNSFailure: true, Reason: "no such host"}
	up := reach.Result{Reachable: true, EdgeUp: true}
	trusted := reach.Result{Reachable: true, EdgeUp: true, CertTrusted: true}
	promoted := trusted
	promoted.ServingURL = app
	cases := []struct {
		name      string
		answers   map[string]reach.Result
		diagnosis string
		outputs   map[string]string
		want      reach.Result
		probed    []string
	}{
		{"outputs with no appUrl count as reachable behind a live edge", nil, "", map[string]string{}, up, nil},
		{"a hostname that does not resolve falls back to the box address, never trusted", map[string]reach.Result{app: dns, "https://203.0.113.9/": {Reachable: true}}, "",
			map[string]string{"appUrl": app, "publicIp": "203.0.113.9"},
			reach.Result{Reachable: true, EdgeUp: true, ServingURL: "https://203.0.113.9/",
				Reason: "serving on https://203.0.113.9/; the app hostname does not resolve yet, so the platform is advertising the box address instead"},
			[]string{app, "https://203.0.113.9/"}},
		{"a fallback that answers counts the edge up", map[string]reach.Result{app: dns, "https://198.51.100.7/": {Reachable: true, EdgeUp: true, ServingURL: "https://198.51.100.7/"}}, "",
			map[string]string{"appUrl": app, "publicIp": "198.51.100.7"},
			reach.Result{Reachable: true, EdgeUp: true, ServingURL: "https://198.51.100.7/",
				Reason: "serving on https://198.51.100.7/; the app hostname does not resolve yet, so the platform is advertising the box address instead"},
			[]string{app, "https://198.51.100.7/"}},
		{"a fallback that also fails stays unreachable with the DNS reason", map[string]reach.Result{app: dns, "https://203.0.113.9/": dns}, "",
			map[string]string{"appUrl": app, "publicIp": "203.0.113.9"}, dns, []string{app, "https://203.0.113.9/"}},
		{"no public IP means no fallback", map[string]reach.Result{app: dns}, "", map[string]string{"appUrl": app}, dns, []string{app}},
		{"a hostname that does not resolve reaches no edge", map[string]reach.Result{"https://app.example.test/": dns}, "",
			map[string]string{"appUrl": "https://app.example.test/"}, dns, []string{"https://app.example.test/"}},
		{"with no diagnoser the reason is unchanged", map[string]reach.Result{app: dns}, "", map[string]string{"appUrl": app}, dns, []string{app}},
		{"a diagnosis is appended without changing the verdict", map[string]reach.Result{app: dns}, "apps.example is not delegated", map[string]string{"appUrl": app},
			reach.Result{DNSFailure: true, Reason: "no such host — apps.example is not delegated"}, []string{app}},
		{"a hostname that now serves is probed and promoted back, with its own certificate trust",
			map[string]reach.Result{box: up, app: trusted}, "", map[string]string{"appUrl": box, "appHostname": app, "publicIp": "52.57.246.147"},
			promoted, []string{box, app}},
		{"a serving hostname with an untrusted certificate still promotes, claiming no trust",
			map[string]reach.Result{box: up, app: up}, "", map[string]string{"appUrl": box, "appHostname": app, "publicIp": "52.57.246.147"},
			reach.Result{Reachable: true, EdgeUp: true, ServingURL: app}, []string{box, app}},
		{"a hostname that answers but does not serve leaves the box address advertised",
			map[string]reach.Result{box: up, app: {EdgeUp: true}}, "", map[string]string{"appUrl": box, "appHostname": app, "publicIp": "52.57.246.147"},
			up, []string{box, app}},
		{"a hostname that still does not resolve keeps the box address and says why",
			map[string]reach.Result{box: up, app: dns}, "", map[string]string{"appUrl": box, "appHostname": app, "publicIp": "52.57.246.147"},
			reach.Result{Reachable: true, EdgeUp: true, Reason: "serving on https://52.57.246.147/; the app hostname still does not resolve, so the platform is staying on the box address"},
			[]string{box, app}},
		{"with nothing to promote a healthy app is probed exactly once", map[string]reach.Result{app: trusted}, "", map[string]string{"appUrl": app}, trusted, []string{app}},
		{"an appHostname equal to appUrl is nothing to promote and adds no probe", map[string]reach.Result{app: trusted}, "",
			map[string]string{"appUrl": app, "appHostname": app}, trusted, []string{app}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var probed []string
			c := reach.Checker{Probe: func(_ context.Context, url string) reach.Result {
				probed = append(probed, url)
				return tc.answers[url]
			}}
			if tc.diagnosis != "" {
				c.Diagnose = func(context.Context, string) string { return tc.diagnosis }
			}

			got := c.Evaluate(context.Background(), tc.outputs)

			if got != tc.want {
				t.Errorf("Evaluate(%v) = %+v, want %+v", tc.outputs, got, tc.want)
			}
			if !slices.Equal(probed, tc.probed) {
				t.Errorf("Evaluate(%v) probed %q, want %q", tc.outputs, probed, tc.probed)
			}
		})
	}
}

func TestEvaluate_AReusedCheckerProbesOnlyTheAppItIsHanded(t *testing.T) {
	var c reach.Checker
	var probed []string
	c.Probe = func(_ context.Context, url string) reach.Result {
		probed = append(probed, url)
		return reach.Result{Reachable: true, EdgeUp: true, CertTrusted: true}
	}

	c.Evaluate(context.Background(), map[string]string{"appUrl": "https://203.0.113.9/", "appHostname": "https://a.example/", "publicIp": "203.0.113.9"})
	forA := slices.Clone(probed)
	probed = nil
	c.Evaluate(context.Background(), map[string]string{"appUrl": "https://198.51.100.7/", "appHostname": "https://b.example/", "publicIp": "198.51.100.7"})

	if want := []string{"https://203.0.113.9/", "https://a.example/"}; !slices.Equal(forA, want) {
		t.Errorf("app A's evaluate probed %q, want %q", forA, want)
	}
	if want := []string{"https://198.51.100.7/", "https://b.example/"}; !slices.Equal(probed, want) {
		t.Errorf("app B's evaluate probed %q, want %q", probed, want)
	}
}

func TestEvaluate_TheRealProbe(t *testing.T) {
	t.Run("it vouches for the appUrl it computed and reaches it", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
		defer srv.Close()

		got := reach.Checker{Client: srv.Client()}.Evaluate(context.Background(), map[string]string{"appUrl": srv.URL})

		if !got.Reachable {
			t.Errorf("Evaluate(%s) = %+v, want reachable", srv.URL, got)
		}
	})
	t.Run("it actually dials the box address when the hostname does not resolve", func(t *testing.T) {
		ft := &fakeTransport{dnsNames: true, err: errors.New("no listener in this test")}

		reach.Checker{Client: &http.Client{Transport: ft}}.Evaluate(context.Background(),
			map[string]string{"appUrl": "https://nothing.invalid/", "publicIp": "203.0.113.9"})

		if want := []string{"https://nothing.invalid/", "https://203.0.113.9/"}; !slices.Equal(ft.dialled, want) {
			t.Errorf("dialled %q, want %q, or the fallback is dead code", ft.dialled, want)
		}
	})
}

func TestProbeURL_ThroughATransport(t *testing.T) {
	served := func(u string) reach.Result {
		return reach.Result{Reachable: true, CertTrusted: true, EdgeUp: true, ServingURL: u}
	}
	untrusted := func(u string) reach.Result { return reach.Result{Reachable: true, EdgeUp: true, ServingURL: u} }
	refused := errors.New("connection refused")
	cases := []struct {
		name      string
		url       string
		vouch     []string
		unvouched bool
		status    int
		err       error
		want      reach.Result
		reasonHas string
		dialled   bool
	}{
		{"a 2xx is serving and trusted", app, nil, false, 200, nil, served(app), "", true},
		{"a 4xx means the edge answered, so it is serving", app, nil, false, 404, nil, served(app), "", true},
		{"a 5xx is the edge up with the app not serving", app, nil, false, 502, nil, reach.Result{EdgeUp: true}, "answered 502", true},
		{"an unknown authority is serving but not trusted", app, nil, false, 0, x509.UnknownAuthorityError{}, untrusted(app), "", true},
		{"a hostname mismatch is serving but not trusted", app, nil, false, 0, x509.HostnameError{}, untrusted(app), "", true},
		{"an invalid certificate is serving but not trusted", app, nil, false, 0, x509.CertificateInvalidError{}, untrusted(app), "", true},
		{"a failed verification is serving but not trusted", app, nil, false, 0, &tls.CertificateVerificationError{}, untrusted(app), "", true},
		{"a name that does not resolve is a DNS failure", app, nil, false, 0, &net.DNSError{Err: "no such host", IsNotFound: true}, reach.Result{DNSFailure: true}, "does not resolve", true},
		{"a refused connection is neither serving nor a DNS failure", app, nil, false, 0, refused, reach.Result{}, "not answering", true},
		{"an allowlisted apps-domain URL is probed over HTTP", "https://myapp.apps.mitstack.dev/", nil, false, 200, nil, served("https://myapp.apps.mitstack.dev/"), "", true},
		{"a vouched public IP is dialled rather than refused", "https://203.0.113.9/", nil, false, 0, refused, reach.Result{}, "not answering", true},
		{"no URL is not reachable", "", nil, true, 200, nil, reach.Result{}, "no app URL to probe", false},
		{"an unvouched loopback URL is never dialled", "http://127.0.0.1:8080/", nil, true, 200, nil, reach.Result{}, "not vouched for", false},
		{"an unvouched apps-domain URL is never dialled", "https://myapp.apps.mitstack.dev/", nil, true, 200, nil, reach.Result{}, "not vouched for", false},
		{"a URL absent from the allowlist is never dialled", app, []string{"https://other.example/"}, false, 200, nil, reach.Result{}, "not vouched for", false},
		{"an unvouched public IP is refused for want of a vouch", "https://203.0.113.9/", []string{"https://203.0.113.10/"}, false, 200, nil, reach.Result{}, "not vouched for", false},
		{"a self-vouched https metadata address is refused and named", "https://169.254.169.254/latest/meta-data/", nil, false, 200, nil, reach.Result{}, "169.254.169.254", false},
		{"a vouched http metadata address is refused before a dial", "http://169.254.169.254/latest/meta-data/", nil, false, 200, nil, reach.Result{}, "clears neither", false},
		{"a vouched 10/8 address is refused before a dial", "https://10.0.0.5/", nil, false, 200, nil, reach.Result{}, "clears neither", false},
		{"a vouched 192.168/16 address is refused before a dial", "https://192.168.1.10/", nil, false, 200, nil, reach.Result{}, "clears neither", false},
		{"a vouched 172.16/12 address is refused before a dial", "https://172.16.4.4/", nil, false, 200, nil, reach.Result{}, "clears neither", false},
		{"a vouched unspecified address is refused before a dial", "https://0.0.0.0/", nil, false, 200, nil, reach.Result{}, "clears neither", false},
		{"a vouched IPv6 link-local address is refused before a dial", "https://[fe80::1]/", nil, false, 200, nil, reach.Result{}, "clears neither", false},
		{"a vouched IPv6 unique-local address is refused before a dial", "https://[fd00::1]/", nil, false, 200, nil, reach.Result{}, "clears neither", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ft := &fakeTransport{status: tc.status, err: tc.err}
			vouch := tc.vouch
			if vouch == nil && !tc.unvouched {
				vouch = []string{tc.url}
			}

			got := reach.ProbeURL(context.Background(), &http.Client{Transport: ft}, tc.url, vouch...)

			reason := got.Reason
			got.Reason = ""
			if got != tc.want || !strings.Contains(reason, tc.reasonHas) {
				t.Errorf("ProbeURL(%q, vouched %q) = %+v with reason %q; want %+v with a reason saying %q", tc.url, vouch, got, reason, tc.want, tc.reasonHas)
			}
			if wantDials := map[bool]int{true: 1}[tc.dialled]; len(ft.dialled) != wantDials {
				t.Errorf("ProbeURL(%q) dialled %q, want %d dial(s)", tc.url, ft.dialled, wantDials)
			}
		})
	}
}

func TestProbeURL_AgainstARealServer(t *testing.T) {
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	badGateway := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	cases := []struct {
		name          string
		tls, closed   bool
		handler       http.Handler
		trustedClient bool
		want          func(url string) reach.Result
	}{
		{"a vouched loopback server answering 200 is reachable and trusted", false, false, ok, true,
			func(u string) reach.Result {
				return reach.Result{Reachable: true, CertTrusted: true, EdgeUp: true, ServingURL: u}
			}},
		{"a TLS server answering 200 is reachable behind a live edge", true, false, ok, true,
			func(u string) reach.Result {
				return reach.Result{Reachable: true, CertTrusted: true, EdgeUp: true, ServingURL: u}
			}},
		{"a TLS 502 comes from the edge, so the edge is up and the app is not", true, false, badGateway, true,
			func(string) reach.Result { return reach.Result{EdgeUp: true} }},
		{"a self-signed certificate is serving behind a live edge but not trusted", true, false, ok, false,
			func(u string) reach.Result { return reach.Result{Reachable: true, EdgeUp: true, ServingURL: u} }},
		{"a closed http port is neither serving nor a DNS failure", false, true, ok, false, func(string) reach.Result { return reach.Result{} }},
		{"a closed https port proves neither the app nor the edge", true, true, ok, false, func(string) reach.Result { return reach.Result{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewUnstartedServer(tc.handler)
			if tc.tls {
				srv.StartTLS()
			} else {
				srv.Start()
			}
			client := &http.Client{}
			if tc.trustedClient {
				client = srv.Client()
			}
			if tc.closed {
				srv.Close()
			} else {
				defer srv.Close()
			}

			got := reach.ProbeURL(context.Background(), client, srv.URL, srv.URL)

			got.Reason = ""
			if want := tc.want(srv.URL); got != want {
				t.Errorf("ProbeURL(%s) = %+v, want %+v", srv.URL, got, want)
			}
		})
	}
}

func TestTrustedAppURL(t *testing.T) {
	cases := []struct {
		name, raw string
		extra     []string
		want      string
		errHas    string
	}{
		{"the platform apps domain is accepted with its path", "https://myapp.apps.mitstack.dev/healthz", nil, "https://myapp.apps.mitstack.dev/healthz", ""},
		{"an operator-supplied suffix is accepted", "https://myapp.apps.example.org/", []string{"apps.example.org"}, "https://myapp.apps.example.org/", ""},
		{"the accepted value is exactly the rebuilt form, query included", "https://myapp.apps.mitstack.dev/healthz?x=attacker-controlled-marker", nil,
			"https://myapp.apps.mitstack.dev/healthz?x=attacker-controlled-marker", ""},
		{"a foreign host is refused, naming the host for the operator", "https://attacker.example.com/", nil, "", "attacker.example.com"},
		{"plain http is refused, since the barrier requires https", "http://myapp.apps.mitstack.dev/", nil, "", ""},
		{"a host merely containing the domain ahead of an attacker label is refused", "https://evil-apps.mitstack.dev.attacker.com/", nil, "", ""},
		{"a host suffixed with no dot boundary is refused", "https://xapps.mitstack.dev/", nil, "", ""},
		{"an empty URL is refused", "", nil, "", ""},
		{"a URL that does not parse is refused", "://not a url", nil, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := reach.TrustedAppURL(tc.raw, tc.extra...)

			if got != tc.want || (tc.want == "") != (err != nil) || (err != nil && !strings.Contains(err.Error(), tc.errHas)) {
				t.Errorf("TrustedAppURL(%q, %q) = %q, %v; want %q with an error naming %q", tc.raw, tc.extra, got, err, tc.want, tc.errHas)
			}
		})
	}
}

// fakeResolver answers from addrs, NXDOMAIN for any other name, or err, and counts its lookups.
type fakeResolver struct {
	addrs map[string][]string
	err   error
	calls int
}

func (f *fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if addrs, ok := f.addrs[host]; ok {
		return addrs, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func TestVerifiedCustomDomain(t *testing.T) {
	const domain, provisioned = "myapp.example.org", "203.0.113.10"
	cases := []struct {
		name      string
		resolver  *fakeResolver
		domain    string
		want      string
		errHas    string
		wantCalls int
	}{
		{"a domain resolving to the provisioned address verifies", &fakeResolver{addrs: map[string][]string{domain: {provisioned}}}, domain, domain, "", 1},
		{"one matching address among round-robin records verifies", &fakeResolver{addrs: map[string][]string{domain: {"198.51.100.1", provisioned, "198.51.100.2"}}}, domain, domain, "", 1},
		{"a different address is refused as not matching", &fakeResolver{addrs: map[string][]string{domain: {"198.51.100.99"}}}, domain, "", "match", 1},
		{"a domain that does not resolve is refused", &fakeResolver{}, domain, "", "did not resolve", 1},
		{"a resolver error is never read as verified", &fakeResolver{err: errors.New("resolver timeout")}, domain, "", "resolver timeout", 1},
		{"an empty domain is refused before any lookup", &fakeResolver{}, "", "", "not a bare hostname", 0},
		{"a domain carrying a scheme is refused before any lookup", &fakeResolver{}, "https://" + domain, "", "not a bare hostname", 0},
		{"a domain carrying a port is refused before any lookup", &fakeResolver{}, domain + ":8080", "", "not a bare hostname", 0},
		{"a domain carrying a path is refused before any lookup", &fakeResolver{}, domain + "/admin", "", "not a bare hostname", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := reach.VerifiedCustomDomain(context.Background(), tc.resolver, tc.domain, []string{provisioned})

			if got != tc.want || (tc.want == "") != (err != nil) || (err != nil && !strings.Contains(err.Error(), tc.errHas)) {
				t.Errorf("VerifiedCustomDomain(%q) = %q, %v; want %q with an error saying %q", tc.domain, got, err, tc.want, tc.errHas)
			}
			if tc.resolver.calls != tc.wantCalls {
				t.Errorf("the resolver was consulted %d times, want %d", tc.resolver.calls, tc.wantCalls)
			}
		})
	}
}

// The verified domain widens the barrier by itself and its real subdomains for the one call it is passed to, never to a lookalike.
func TestVerifiedCustomDomain_WidensTrustedAppURLByItselfAlone(t *testing.T) {
	verified, err := reach.VerifiedCustomDomain(context.Background(), &fakeResolver{addrs: map[string][]string{"plain.mitstack.dev": {"203.0.113.10"}}},
		"plain.mitstack.dev", []string{"203.0.113.10"})
	if err != nil {
		t.Fatalf("verifying plain.mitstack.dev: %v", err)
	}
	cases := []struct {
		name, raw string
		extra     []string
		trusted   bool
	}{
		{"the verified domain itself is trusted", "https://plain.mitstack.dev/", []string{verified}, true},
		{"a subdomain of the verified domain is trusted", "https://api.plain.mitstack.dev/", []string{verified}, true},
		{"a trailing attacker label is refused", "https://plain.mitstack.dev.attacker.com/", []string{verified}, false},
		{"a prefix with no dot boundary is refused", "https://evilplain.mitstack.dev/", []string{verified}, false},
		{"the domain in a foreign host's path is refused", "https://attacker.com/plain.mitstack.dev", []string{verified}, false},
		{"the domain as userinfo ahead of a foreign host is refused", "https://plain.mitstack.dev@attacker.com/", []string{verified}, false},
		{"the domain is untrusted again once the suffix is omitted, since there is no global allowlist", "https://plain.mitstack.dev/", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := reach.TrustedAppURL(tc.raw, tc.extra...)

			if (err == nil) != tc.trusted {
				t.Errorf("TrustedAppURL(%q, %q) error = %v, want trusted %v", tc.raw, tc.extra, err, tc.trusted)
			}
		})
	}
}
