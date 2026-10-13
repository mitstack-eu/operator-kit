package reach_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/reach"
)

func TestTrustedAppURL_AcceptsThePlatformAppsDomain(t *testing.T) {
	got, err := reach.TrustedAppURL("https://myapp.apps.mitstack.dev/healthz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, "https://myapp.apps.mitstack.dev") {
		t.Fatalf("TrustedAppURL() = %q, want it to keep the scheme+host", got)
	}
	if !strings.HasSuffix(got, "/healthz") {
		t.Fatalf("TrustedAppURL() = %q, want the path preserved", got)
	}
}

func TestTrustedAppURL_AcceptsAnOperatorSuppliedExtraSuffix(t *testing.T) {
	got, err := reach.TrustedAppURL("https://myapp.apps.example.org/", "apps.example.org")
	if err != nil {
		t.Fatalf("unexpected error with an allowlisted extra suffix: %v", err)
	}
	if !strings.Contains(got, "myapp.apps.example.org") {
		t.Fatalf("TrustedAppURL() = %q, want the extra-suffix host preserved", got)
	}
}

func TestTrustedAppURL_RejectsAForeignHost(t *testing.T) {
	if _, err := reach.TrustedAppURL("https://attacker.example.com/"); err == nil {
		t.Fatal("a host outside every allowed suffix must be refused")
	}
}

func TestTrustedAppURL_RejectsPlainHTTP(t *testing.T) {
	if _, err := reach.TrustedAppURL("http://myapp.apps.mitstack.dev/"); err == nil {
		t.Fatal("plain http must be refused — the probe barrier requires https")
	}
}

func TestTrustedAppURL_RejectsASuffixTrickHostWithTrailingAttackerLabel(t *testing.T) {
	if _, err := reach.TrustedAppURL("https://evil-apps.mitstack.dev.attacker.com/"); err == nil {
		t.Fatal("a host that merely CONTAINS the allowed domain must be refused")
	}
}

func TestTrustedAppURL_RejectsAHostThatIsAPrefixNotASubdomain(t *testing.T) {
	// A suffix check missing the leading dot would wrongly accept this.
	if _, err := reach.TrustedAppURL("https://xapps.mitstack.dev/"); err == nil {
		t.Fatal("a host that is merely string-suffixed (no dot boundary) must be refused")
	}
}

func TestTrustedAppURL_RejectsEmptyString(t *testing.T) {
	if _, err := reach.TrustedAppURL(""); err == nil {
		t.Fatal("an empty raw URL must be refused")
	}
}

func TestTrustedAppURL_RejectsUnparsableGarbage(t *testing.T) {
	if _, err := reach.TrustedAppURL("://not a url"); err == nil {
		t.Fatal("a URL that fails to parse must be refused")
	}
}

func TestTrustedAppURL_RejectionErrorNamesTheOffendingHost(t *testing.T) {
	_, err := reach.TrustedAppURL("https://attacker.example.com/")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "attacker.example.com") {
		t.Fatalf("error %q must name the offending host so an operator can diagnose it", err.Error())
	}
}

func TestTrustedAppURL_AcceptedReturnValueIsExactlyTheRebuiltForm(t *testing.T) {
	raw := "https://myapp.apps.mitstack.dev/healthz?x=" + "attacker-controlled-marker"
	got, err := reach.TrustedAppURL(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://myapp.apps.mitstack.dev/healthz?x=attacker-controlled-marker"
	if got != want {
		t.Fatalf("TrustedAppURL() = %q, want the exact rebuilt form %q", got, want)
	}
}

// failIfCalledTransport fails the test if any request is attempted.
type failIfCalledTransport struct{ t *testing.T }

func (f failIfCalledTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.t.Fatalf("no HTTP request may be made for a URL that fails the barrier, got a request to %q", req.URL)
	return nil, nil
}

// A self-vouch alone must never suffice, or any https URL, including the cloud metadata endpoint, becomes dialable.
func TestProbeURL_HostileSelfVouchedURLIsRefusedWithoutDialing(t *testing.T) {
	hostile := "https://169.254.169.254/latest/meta-data/"
	client := &http.Client{Transport: failIfCalledTransport{t: t}}

	got := reach.ProbeURL(context.Background(), client, hostile, hostile)

	if got.Reachable {
		t.Fatalf("a self-vouched but off-domain, non-loopback host must never be reachable, got %+v", got)
	}
	if !strings.Contains(got.Reason, "169.254.169.254") {
		t.Fatalf("Reason = %q, want it to name the disallowed host", got.Reason)
	}
}

func TestProbeURL_VouchedLoopbackURLIsDialedNormally(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got := reach.ProbeURL(context.Background(), srv.Client(), srv.URL, srv.URL)

	if !got.Reachable {
		t.Fatalf("a vouched loopback URL must be dialed and reachable, got %+v", got)
	}
}

// An apps-domain URL with an empty allowedURLs must still be refused, so being on the apps domain never substitutes for a vouch.
func TestProbeURL_AppsDomainURLWithoutAVouchIsRefusedWithoutDialing(t *testing.T) {
	client := &http.Client{Transport: failIfCalledTransport{t: t}}

	got := reach.ProbeURL(context.Background(), client, "https://myapp.apps.mitstack.dev/")

	if got.Reachable {
		t.Fatalf("an unvouched URL must never be reachable even on the platform's own apps domain, got %+v", got)
	}
	if !strings.Contains(strings.ToLower(got.Reason), "vouch") {
		t.Fatalf("Reason = %q, want it to say the URL was not vouched for", got.Reason)
	}
}

func TestProbeURL_StillProbesAnAllowlistedURL(t *testing.T) {
	called := false
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		if req.URL.Host != "myapp.apps.mitstack.dev" {
			t.Fatalf("request went to host %q, want myapp.apps.mitstack.dev", req.URL.Host)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Header:     make(http.Header),
		}, nil
	})}

	appURL := "https://myapp.apps.mitstack.dev/"
	got := reach.ProbeURL(context.Background(), client, appURL, appURL)

	if !called {
		t.Fatal("an allowlisted, vouched URL must still be probed over HTTP")
	}
	if !got.Reachable {
		t.Fatalf("a 200 from an allowlisted host must be reachable, got %+v", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
