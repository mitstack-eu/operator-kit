package stackitid_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/stackitid"
)

const (
	leakAssertionMaterial   = "STACKIT-ASSERTION-JWT-THAT-MUST-NEVER-SURFACE"
	leakAccessTokenMaterial = "STACKIT-ACCESS-TOKEN-THAT-MUST-NEVER-SURFACE"
)

func TestNoSecretLeak_ValidationErrorsCarryNoAssertion(t *testing.T) {
	cases := []stackitid.Identity{
		{Audience: testAudience, Assertion: leakAssertionMaterial},                                             // no TokenURL
		{TokenURL: "https://sts.stackit.cloud/token", Assertion: leakAssertionMaterial},                        // no Audience
		{TokenURL: "https://sts.stackit.cloud/token", Audience: testAudience, Assertion: ""},                   // no Assertion
		{TokenURL: "http://sts.stackit.cloud/token", Audience: testAudience, Assertion: leakAssertionMaterial}, // non-https
	}
	for _, id := range cases {
		_, err := stackitid.Resolve(context.Background(), id)
		if err == nil {
			t.Fatalf("expected a validation error for %+v", id)
		}
		kittest.AssertNoSecret(t, "Resolve validation error", err.Error(), leakAssertionMaterial)
	}
}

func TestNoSecretLeak_InvalidGrantErrorCarriesNoAssertion(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"assertion signature verification failed"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := stackitid.Resolve(context.Background(), stackitid.Identity{
		TokenURL: srv.URL + "/token", Audience: testAudience, Assertion: leakAssertionMaterial, Client: srv.Client(),
	})
	if err == nil {
		t.Fatal("expected an error for a rejected assertion")
	}
	kittest.AssertNoSecret(t, "Resolve invalid_grant error", err.Error(), leakAssertionMaterial)
}

func TestNoSecretLeak_NonJSONBodyErrorCarriesNoAssertion(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("this is not json"))
	}))
	t.Cleanup(srv.Close)

	_, err := stackitid.Resolve(context.Background(), stackitid.Identity{
		TokenURL: srv.URL + "/token", Audience: testAudience, Assertion: leakAssertionMaterial, Client: srv.Client(),
	})
	if err == nil {
		t.Fatal("expected an error for a non-JSON response body")
	}
	kittest.AssertNoSecret(t, "Resolve non-JSON body error", err.Error(), leakAssertionMaterial)
}

func TestNoSecretLeak_ServerErrorCarriesNoAssertion(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	t.Cleanup(srv.Close)

	_, err := stackitid.Resolve(context.Background(), stackitid.Identity{
		TokenURL: srv.URL + "/token", Audience: testAudience, Assertion: leakAssertionMaterial, Client: srv.Client(),
	})
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
	kittest.AssertNoSecret(t, "Resolve 500 error", err.Error(), leakAssertionMaterial)
}

func TestNoSecretLeak_TransportFailureCarriesNoAssertion(t *testing.T) {
	_, err := stackitid.Resolve(context.Background(), stackitid.Identity{
		TokenURL:  "https://sts.stackit.cloud/token",
		Audience:  testAudience,
		Assertion: leakAssertionMaterial,
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp: connection refused, assertion=" + leakAssertionMaterial)
		})},
	})
	if err == nil {
		t.Fatal("expected an error when the transport fails")
	}
	kittest.AssertNoSecret(t, "Resolve transport-failure error", err.Error(), leakAssertionMaterial)
}

func TestNoSecretLeak_RefresherErrorCarriesNoCredential(t *testing.T) {
	now := time.Now()
	r := stackitid.NewRefresher(func(context.Context) (stackitid.Credential, error) {
		return stackitid.Credential{
			AccessToken: leakAccessTokenMaterial,
			Expires:     now.Add(-time.Hour), // already dead: forces the fail-closed path
		}, nil
	})
	r.Now = func() time.Time { return now }

	got, err := r.Get(context.Background())
	if err == nil {
		t.Fatal("expected the fail-closed error")
	}
	kittest.AssertNoSecret(t, "Refresher error", err.Error(), leakAccessTokenMaterial)
	if got.AccessToken != "" {
		t.Error("Refresher returned credential material alongside its error")
	}
}

func TestNoSecretLeak_NothingIsLoggedDuringResolve(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	// Success path.
	okSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"` + leakAccessTokenMaterial + `","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(okSrv.Close)
	if _, err := stackitid.Resolve(context.Background(), stackitid.Identity{
		TokenURL: okSrv.URL + "/token", Audience: testAudience, Assertion: leakAssertionMaterial, Client: okSrv.Client(),
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Rejected-assertion path.
	rejectSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"assertion signature verification failed"}`))
	}))
	t.Cleanup(rejectSrv.Close)
	_, _ = stackitid.Resolve(context.Background(), stackitid.Identity{
		TokenURL: rejectSrv.URL + "/token", Audience: testAudience, Assertion: leakAssertionMaterial, Client: rejectSrv.Client(),
	})

	// Refresher fail-closed path.
	now := time.Now()
	r := stackitid.NewRefresher(func(context.Context) (stackitid.Credential, error) {
		return stackitid.Credential{AccessToken: leakAccessTokenMaterial, Expires: now.Add(-time.Hour)}, nil
	})
	r.Now = func() time.Time { return now }
	_, _ = r.Get(context.Background())

	if strings.Contains(buf.String(), leakAssertionMaterial) {
		t.Errorf("LEAK: the assertion surfaced in log output: %s", buf.String())
	}
	if strings.Contains(buf.String(), leakAccessTokenMaterial) {
		t.Errorf("LEAK: the access token surfaced in log output: %s", buf.String())
	}
}
