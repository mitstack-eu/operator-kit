package reach_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/reach"
)

func TestKeys_AreTheAdvertisedContract(t *testing.T) {
	got := reach.Keys()
	want := []string{"appHostname", "appUrl", "certTrusted", "publicIp", "reachable"}
	if len(got) != len(want) {
		t.Fatalf("Keys() = %v, want %v", got, want)
	}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("Keys() must be sorted so a parity comparison is stable: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Keys()[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestApply_AlwaysWritesReachableAndCertTrusted(t *testing.T) {
	out := reach.Apply(map[string]string{"appUrl": "https://app.example/"},
		reach.Result{Reachable: true, CertTrusted: true, ServingURL: "https://app.example/"})

	if out["reachable"] != "true" {
		t.Fatalf("reachable = %q, want \"true\"", out["reachable"])
	}
	if out["certTrusted"] != "true" {
		t.Fatalf("certTrusted = %q, want \"true\"", out["certTrusted"])
	}
	if _, present := out["appHostname"]; present {
		t.Fatalf("appHostname must be absent when the probed URL is the serving URL, got %q", out["appHostname"])
	}
}

// A not-serving verdict must still emit both keys, or "absent" is ambiguous between unprobed and dead.
func TestApply_WritesFalseRatherThanOmitting(t *testing.T) {
	out := reach.Apply(map[string]string{"appUrl": "https://app.example/"}, reach.Result{})
	if out["reachable"] != "false" || out["certTrusted"] != "false" {
		t.Fatalf("want explicit false values, got reachable=%q certTrusted=%q",
			out["reachable"], out["certTrusted"])
	}
}

func TestApply_FallbackPreservesIntendedHostnameAndAdvertisesWhatAnswered(t *testing.T) {
	out := reach.Apply(
		map[string]string{"appUrl": "https://app.example/", "publicIp": "203.0.113.9"},
		reach.Result{Reachable: true, ServingURL: "https://203.0.113.9/"},
	)
	if out["appUrl"] != "https://203.0.113.9/" {
		t.Fatalf("appUrl must become the URL that actually answered, got %q", out["appUrl"])
	}
	if out["appHostname"] != "https://app.example/" {
		t.Fatalf("appHostname must preserve the intended hostname, got %q", out["appHostname"])
	}
}

func TestApply_DoesNotMutateCallersMap(t *testing.T) {
	in := map[string]string{"appUrl": "https://app.example/"}
	_ = reach.Apply(in, reach.Result{Reachable: true, ServingURL: "https://203.0.113.9/"})
	if len(in) != 1 || in["appUrl"] != "https://app.example/" {
		t.Fatalf("Apply mutated its argument: %v", in)
	}
}

func TestEvaluate_NoAppURLIsReachable(t *testing.T) {
	var c reach.Checker
	got := c.Evaluate(context.Background(), map[string]string{})
	if !got.Reachable {
		t.Fatalf("outputs without an appUrl must be treated as reachable, got %+v", got)
	}
}

func TestEvaluate_DNSFailureFallsBackToPublicIP(t *testing.T) {
	var probed []string
	c := reach.Checker{
		Probe: func(_ context.Context, url string) reach.Result {
			probed = append(probed, url)
			if url == "https://app.example/" {
				return reach.Result{DNSFailure: true, Reason: "no such host"}
			}
			return reach.Result{Reachable: true}
		},
	}

	got := c.Evaluate(context.Background(), map[string]string{
		"appUrl":   "https://app.example/",
		"publicIp": "203.0.113.9",
	})

	if !got.Reachable {
		t.Fatalf("the box answered on its IP — must be reachable, got %+v", got)
	}
	if got.ServingURL != "https://203.0.113.9/" {
		t.Fatalf("ServingURL = %q, want the IP fallback", got.ServingURL)
	}
	if got.CertTrusted {
		t.Fatal("a bare-IP URL can only carry a self-signed cert — CertTrusted must be false")
	}
	if len(probed) != 2 {
		t.Fatalf("want hostname then IP probed, got %v", probed)
	}
}

func TestEvaluate_FallbackThatAlsoFailsStaysUnreachable(t *testing.T) {
	c := reach.Checker{
		Probe: func(_ context.Context, _ string) reach.Result {
			return reach.Result{DNSFailure: true, Reason: "no such host"}
		},
	}
	got := c.Evaluate(context.Background(), map[string]string{
		"appUrl":   "https://app.example/",
		"publicIp": "203.0.113.9",
	})
	if got.Reachable {
		t.Fatalf("nothing answered — must not be reachable: %+v", got)
	}
	if got.Reason != "no such host" {
		t.Fatalf("want the DNS reason preserved, got %q", got.Reason)
	}
}

func TestEvaluate_NoPublicIPMeansNoFallback(t *testing.T) {
	calls := 0
	c := reach.Checker{
		Probe: func(_ context.Context, _ string) reach.Result {
			calls++
			return reach.Result{DNSFailure: true, Reason: "no such host"}
		},
	}
	got := c.Evaluate(context.Background(), map[string]string{"appUrl": "https://app.example/"})
	if got.Reachable || calls != 1 {
		t.Fatalf("without publicIp there is nothing to fall back to: reachable=%v calls=%d", got.Reachable, calls)
	}
}

func TestEvaluate_DiagnosisIsAppendedWithoutChangingTheVerdict(t *testing.T) {
	c := reach.Checker{
		Probe: func(_ context.Context, _ string) reach.Result {
			return reach.Result{DNSFailure: true, Reason: "no such host"}
		},
		Diagnose: func(_ context.Context, _ string) string {
			return "apps.example is not delegated"
		},
	}
	got := c.Evaluate(context.Background(), map[string]string{"appUrl": "https://app.example/"})
	if got.Reachable {
		t.Fatal("a diagnosis must never change the verdict")
	}
	if got.Reason != "no such host — apps.example is not delegated" {
		t.Fatalf("Reason = %q, want the diagnosis appended", got.Reason)
	}
}

// The default (nil Probe) path must vouch for the URL the Checker computed, or ProbeURL's default-deny gate refuses it.
func TestEvaluate_DefaultProbeVouchesForTheURLItComputedAndReachesIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := reach.Checker{Client: srv.Client()}
	got := c.Evaluate(context.Background(), map[string]string{"appUrl": srv.URL})
	if !got.Reachable {
		t.Fatalf("the checker must vouch for the appUrl it computed and reach it, got %+v", got)
	}
}

func TestEvaluate_NilDiagnoserIsFine(t *testing.T) {
	c := reach.Checker{
		Probe: func(_ context.Context, _ string) reach.Result {
			return reach.Result{DNSFailure: true, Reason: "no such host"}
		},
	}
	got := c.Evaluate(context.Background(), map[string]string{"appUrl": "https://app.example/"})
	if got.Reason != "no such host" {
		t.Fatalf("Reason = %q, want it unchanged with no diagnoser", got.Reason)
	}
}

func TestProbeURL_2xxIsServingAndTrusted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got := reach.ProbeURL(context.Background(), srv.Client(), srv.URL, srv.URL)
	if !got.Reachable || !got.CertTrusted {
		t.Fatalf("a plain 200 must be serving and trusted, got %+v", got)
	}
}

func TestProbeURL_4xxIsServing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if got := reach.ProbeURL(context.Background(), srv.Client(), srv.URL, srv.URL); !got.Reachable {
		t.Fatalf("404 means the edge answered — must be reachable, got %+v", got)
	}
}

func TestProbeURL_5xxIsNotServing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if got := reach.ProbeURL(context.Background(), srv.Client(), srv.URL, srv.URL); got.Reachable {
		t.Fatalf("502 must not be serving, got %+v", got)
	}
}

func TestProbeURL_UntrustedCertIsServingButNotTrusted(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got := reach.ProbeURL(context.Background(), &http.Client{}, srv.URL, srv.URL)
	if !got.Reachable {
		t.Fatalf("an untrusted cert still proves something is serving, got %+v", got)
	}
	if got.CertTrusted {
		t.Fatal("CertTrusted must be false for an untrusted chain")
	}
}

func TestProbeURL_EmptyURL(t *testing.T) {
	if got := reach.ProbeURL(context.Background(), nil, ""); got.Reachable {
		t.Fatalf("no URL is not reachable, got %+v", got)
	}
}

func TestProbeURL_ConnectionRefusedIsNotADNSFailure(t *testing.T) {
	// Resolvable but not answering: misclassifying it as DNSFailure would trigger the IP fallback for an app that is merely down.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind loopback: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	url := "http://" + addr + "/"
	got := reach.ProbeURL(context.Background(), &http.Client{}, url, url)
	if got.Reachable {
		t.Fatalf("a refused connection is not serving, got %+v", got)
	}
	if got.DNSFailure {
		t.Fatalf("a refused connection is not a DNS failure, got %+v", got)
	}
}

// An appURL that no allowlist entry vouches for must never be dialed or reported reachable.
func TestProbeURL_EmptyAllowlistNeverDialsAndIsNotReachable(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got := reach.ProbeURL(context.Background(), srv.Client(), srv.URL)
	if got.Reachable {
		t.Fatalf("an unvouched URL must never be treated as reachable, got %+v", got)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("an unvouched URL must never be dialed, but the server received %d request(s)", n)
	}
	if !strings.Contains(strings.ToLower(got.Reason), "vouch") {
		t.Fatalf("Reason must say the URL was not vouched for, got %q", got.Reason)
	}
}

func TestProbeURL_NotInAllowlistNeverDialsAndIsNotReachable(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got := reach.ProbeURL(context.Background(), srv.Client(), srv.URL, "https://other.example/")
	if got.Reachable {
		t.Fatalf("a URL absent from the allowlist must never be treated as reachable, got %+v", got)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("a URL absent from the allowlist must never be dialed, but the server received %d request(s)", n)
	}
}

func TestProbeURL_ClassifiesTransportErrors(t *testing.T) {
	cases := map[string]struct {
		err              error
		wantReachable    bool
		wantDNSFailure   bool
		wantCertAccepted bool
	}{
		"unknown authority": {err: x509.UnknownAuthorityError{}, wantReachable: true, wantCertAccepted: true},
		"hostname mismatch": {err: x509.HostnameError{}, wantReachable: true, wantCertAccepted: true},
		"invalid cert":      {err: x509.CertificateInvalidError{}, wantReachable: true, wantCertAccepted: true},
		"verification":      {err: &tls.CertificateVerificationError{}, wantReachable: true, wantCertAccepted: true},
		"dns":               {err: &net.DNSError{Err: "no such host", IsNotFound: true}, wantDNSFailure: true},
		"refused":           {err: errors.New("connection refused")},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: errTransport{err: tc.err}}
			got := reach.ProbeURL(context.Background(), client, "https://app.example/", "https://app.example/")

			if got.Reachable != tc.wantReachable {
				t.Fatalf("Reachable = %v, want %v (%+v)", got.Reachable, tc.wantReachable, got)
			}
			if got.DNSFailure != tc.wantDNSFailure {
				t.Fatalf("DNSFailure = %v, want %v (%+v)", got.DNSFailure, tc.wantDNSFailure, got)
			}
			if tc.wantCertAccepted && got.CertTrusted {
				t.Fatalf("a certificate error must never yield CertTrusted: %+v", got)
			}
		})
	}
}

type errTransport struct{ err error }

func (t errTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, t.err }

func TestProbeURL_VouchedPublicIPIsDialledRatherThanRefused(t *testing.T) {
	// TEST-NET-3 never answers, so the attempt must fail as a dial, not as a refusal before one.
	const ip = "https://203.0.113.9/"
	got := reach.ProbeURL(context.Background(), &http.Client{Timeout: 300 * time.Millisecond}, ip, ip)

	if got.Reachable {
		t.Fatal("TEST-NET-3 cannot answer — a reachable verdict would mean the probe never ran")
	}
	if strings.Contains(got.Reason, "clears neither") {
		t.Fatalf("the fallback address was refused before any dial, so the fallback is dead code: %s", got.Reason)
	}
}

func TestProbeURL_VouchedCloudMetadataAddressIsStillRefused(t *testing.T) {
	const metadata = "http://169.254.169.254/latest/meta-data/"
	got := reach.ProbeURL(context.Background(), &http.Client{Timeout: 300 * time.Millisecond}, metadata, metadata)

	if got.Reachable {
		t.Fatal("the cloud metadata endpoint must never be reachable through this probe")
	}
	if !strings.Contains(got.Reason, "clears neither") {
		t.Fatalf("link-local must be refused BEFORE a dial, whatever the caller vouches for; got %q", got.Reason)
	}
}

func TestProbeURL_VouchedPrivateAndUnspecifiedAddressesAreStillRefused(t *testing.T) {
	for _, u := range []string{
		"https://10.0.0.5/",
		"https://192.168.1.10/",
		"https://172.16.4.4/",
		"https://0.0.0.0/",
		"https://[fe80::1]/",
		"https://[fd00::1]/",
	} {
		got := reach.ProbeURL(context.Background(), &http.Client{Timeout: 300 * time.Millisecond}, u, u)
		if !strings.Contains(got.Reason, "clears neither") {
			t.Errorf("%s must be refused before a dial; got %q", u, got.Reason)
		}
	}
}

func TestProbeURL_UnvouchedPublicIPIsStillRefused(t *testing.T) {
	got := reach.ProbeURL(context.Background(), &http.Client{Timeout: 300 * time.Millisecond},
		"https://203.0.113.9/", "https://203.0.113.10/")

	if !strings.Contains(got.Reason, "not vouched for") {
		t.Fatalf("the vouch requirement is unchanged — an unvouched address is refused first; got %q", got.Reason)
	}
}

// recordingTransport records dialled URLs, the only observable that tells a refusal from a failed connection.
type recordingTransport struct{ dialled []string }

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.dialled = append(rt.dialled, req.URL.String())
	if host := req.URL.Hostname(); net.ParseIP(host) == nil {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return nil, errors.New("no listener in this test")
}

// With no injected Probe, Evaluate runs the real barrier; what matters is that the fallback was attempted.
func TestEvaluate_FallbackIsActuallyDialledThroughTheRealProbe(t *testing.T) {
	rt := &recordingTransport{}
	c := reach.Checker{Client: &http.Client{Transport: rt, Timeout: 300 * time.Millisecond}}

	c.Evaluate(context.Background(), map[string]string{
		"appUrl":   "https://nothing.invalid/",
		"publicIp": "203.0.113.9",
	})

	var sawFallback bool
	for _, u := range rt.dialled {
		if strings.Contains(u, "203.0.113.9") {
			sawFallback = true
		}
	}
	if !sawFallback {
		t.Fatalf("the box address was never dialled, so the fallback is dead code; dialled=%v", rt.dialled)
	}
}

// With appUrl rewritten to the box address and the name now serving, the name must be advertised again.
func TestEvaluate_PromotesBackToHostnameOnceItServes(t *testing.T) {
	var probed []string
	c := reach.Checker{
		Probe: func(_ context.Context, url string) reach.Result {
			probed = append(probed, url)
			switch url {
			case "https://52.57.246.147/":
				return reach.Result{Reachable: true, EdgeUp: true, CertTrusted: false}
			case "https://app.example/":
				return reach.Result{Reachable: true, EdgeUp: true, CertTrusted: true}
			}
			t.Fatalf("unexpected probe of %q", url)
			return reach.Result{}
		},
	}

	got := c.Evaluate(context.Background(), map[string]string{
		"appUrl":      "https://52.57.246.147/",
		"appHostname": "https://app.example/",
		"publicIp":    "52.57.246.147",
	})

	if !got.Reachable {
		t.Fatalf("the hostname is serving — must be reachable, got %+v", got)
	}
	if got.ServingURL != "https://app.example/" {
		t.Fatalf("ServingURL = %q, want the hostname promoted back once it resolves and serves", got.ServingURL)
	}
	if !got.CertTrusted {
		t.Fatal("the hostname's own probe reported a trusted certificate — CertTrusted must follow the promoted address, not the box fallback's false")
	}

	var sawHostname bool
	for _, u := range probed {
		if u == "https://app.example/" {
			sawHostname = true
		}
	}
	if !sawHostname {
		t.Fatalf("the hostname was never actually probed, so the promotion is a guess rather than a verified promote; probed=%v", probed)
	}
}

func TestEvaluate_HostnameRespondsButNotServing_StaysOnBoxAddress(t *testing.T) {
	c := reach.Checker{
		Probe: func(_ context.Context, url string) reach.Result {
			switch url {
			case "https://52.57.246.147/":
				return reach.Result{Reachable: true, EdgeUp: true}
			case "https://app.example/":
				return reach.Result{Reachable: false, EdgeUp: true}
			}
			t.Fatalf("unexpected probe of %q", url)
			return reach.Result{}
		},
	}

	got := c.Evaluate(context.Background(), map[string]string{
		"appUrl":      "https://52.57.246.147/",
		"appHostname": "https://app.example/",
		"publicIp":    "52.57.246.147",
	})

	if !got.Reachable {
		t.Fatalf("the box is still serving — must stay reachable, got %+v", got)
	}
	if got.ServingURL != "" {
		t.Fatalf("no promotion should happen — ServingURL must stay empty (appUrl unchanged), got %q", got.ServingURL)
	}
}

func TestEvaluate_HostnameStillUnresolved_StaysOnBoxAddressAndKeepsReason(t *testing.T) {
	c := reach.Checker{
		Probe: func(_ context.Context, url string) reach.Result {
			switch url {
			case "https://52.57.246.147/":
				return reach.Result{Reachable: true, EdgeUp: true}
			case "https://app.example/":
				return reach.Result{DNSFailure: true, Reason: "no such host"}
			}
			t.Fatalf("unexpected probe of %q", url)
			return reach.Result{}
		},
	}

	got := c.Evaluate(context.Background(), map[string]string{
		"appUrl":      "https://52.57.246.147/",
		"appHostname": "https://app.example/",
		"publicIp":    "52.57.246.147",
	})

	if !got.Reachable {
		t.Fatalf("the box is still serving — must stay reachable, got %+v", got)
	}
	if got.ServingURL != "" {
		t.Fatalf("no promotion — the hostname still doesn't resolve, got ServingURL=%q", got.ServingURL)
	}
	if got.Reason == "" || !strings.Contains(got.Reason, "does not resolve") {
		t.Fatalf("Reason = %q, want it to keep explaining that the hostname still does not resolve", got.Reason)
	}
}

// Nothing to promote must not add a second outbound call, which would double every health poll.
func TestEvaluate_NoAppHostname_HealthyPathProbesExactlyOnce(t *testing.T) {
	calls := 0
	c := reach.Checker{
		Probe: func(_ context.Context, _ string) reach.Result {
			calls++
			return reach.Result{Reachable: true, EdgeUp: true, CertTrusted: true}
		},
	}
	got := c.Evaluate(context.Background(), map[string]string{"appUrl": "https://app.example/"})

	if !got.Reachable || !got.CertTrusted {
		t.Fatalf("a directly-serving app must stay reachable and trusted, got %+v", got)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want exactly 1 — nothing to promote must not add a probe", calls)
	}
}

func TestEvaluate_AppHostnameEqualsAppURL_NoPromotionNoChurn(t *testing.T) {
	calls := 0
	c := reach.Checker{
		Probe: func(_ context.Context, _ string) reach.Result {
			calls++
			return reach.Result{Reachable: true, EdgeUp: true, CertTrusted: true}
		},
	}
	got := c.Evaluate(context.Background(), map[string]string{
		"appUrl":      "https://app.example/",
		"appHostname": "https://app.example/",
	})

	if calls != 1 {
		t.Fatalf("calls = %d, want exactly 1 — appHostname equal to appUrl is nothing to promote", calls)
	}
	if got.ServingURL != "" {
		t.Fatalf("ServingURL = %q, want empty — no churn when there is nothing to promote", got.ServingURL)
	}
}

// A serving hostname with an untrusted certificate still promotes, and its trust comes from its own probe.
func TestEvaluate_PromotionRecomputesCertTrusted_UntrustedHostnameCert(t *testing.T) {
	c := reach.Checker{
		Probe: func(_ context.Context, url string) reach.Result {
			switch url {
			case "https://52.57.246.147/":
				return reach.Result{Reachable: true, EdgeUp: true, CertTrusted: false}
			case "https://app.example/":
				return reach.Result{Reachable: true, EdgeUp: true, CertTrusted: false}
			}
			t.Fatalf("unexpected probe of %q", url)
			return reach.Result{}
		},
	}
	got := c.Evaluate(context.Background(), map[string]string{
		"appUrl":      "https://52.57.246.147/",
		"appHostname": "https://app.example/",
		"publicIp":    "52.57.246.147",
	})

	if got.ServingURL != "https://app.example/" {
		t.Fatalf("the hostname serves — it must still be promoted even without a trusted cert, got ServingURL=%q", got.ServingURL)
	}
	if got.CertTrusted {
		t.Fatal("the hostname's own certificate is untrusted — promotion must not claim trust it did not observe")
	}
}

// A reused Checker must probe only the hostname in the outputs it was handed, never another app's.
func TestEvaluate_PromotionNeverProbesAnotherAppsHostname(t *testing.T) {
	var probedForA, probedForB []string

	var c reach.Checker

	c.Probe = func(_ context.Context, url string) reach.Result {
		probedForA = append(probedForA, url)
		switch url {
		case "https://203.0.113.9/", "https://a.example/":
			return reach.Result{Reachable: true, EdgeUp: true, CertTrusted: true}
		}
		t.Fatalf("app A's evaluate probed %q, which belongs to app B", url)
		return reach.Result{}
	}
	c.Evaluate(context.Background(), map[string]string{
		"appUrl":      "https://203.0.113.9/",
		"appHostname": "https://a.example/",
		"publicIp":    "203.0.113.9",
	})

	c.Probe = func(_ context.Context, url string) reach.Result {
		probedForB = append(probedForB, url)
		switch url {
		case "https://198.51.100.7/", "https://b.example/":
			return reach.Result{Reachable: true, EdgeUp: true, CertTrusted: true}
		}
		t.Fatalf("app B's evaluate probed %q, which belongs to app A", url)
		return reach.Result{}
	}
	c.Evaluate(context.Background(), map[string]string{
		"appUrl":      "https://198.51.100.7/",
		"appHostname": "https://b.example/",
		"publicIp":    "198.51.100.7",
	})

	for _, u := range probedForA {
		if strings.Contains(u, "b.example") || strings.Contains(u, "198.51.100.7") {
			t.Fatalf("app A's evaluate probed app B's address: %v", probedForA)
		}
	}
	for _, u := range probedForB {
		if strings.Contains(u, "a.example") || strings.Contains(u, "203.0.113.9") {
			t.Fatalf("app B's evaluate probed app A's address: %v", probedForB)
		}
	}
}

// After a promotion appHostname must be removed, not left duplicating appUrl or overwritten with the box address.
func TestApply_PromotionAdvertisesHostnameAndLeavesNoStaleDuplicate(t *testing.T) {
	outputs := map[string]string{
		"appUrl":      "https://52.57.246.147/",
		"appHostname": "https://app.example/",
		"publicIp":    "52.57.246.147",
	}
	res := reach.Result{Reachable: true, CertTrusted: true, ServingURL: "https://app.example/"}

	out := reach.Apply(outputs, res)

	if out["appUrl"] != "https://app.example/" {
		t.Fatalf("appUrl = %q, want the promoted hostname advertised again", out["appUrl"])
	}
	if v, present := out["appHostname"]; present {
		t.Fatalf("appHostname must not survive a promotion (no stale duplicate, and never the old box address), got %q", v)
	}
}
