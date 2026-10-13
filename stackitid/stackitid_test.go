package stackitid_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/stackitid"
)

const (
	leakAssertion = "STACKIT-ASSERTION-JWT-THAT-MUST-NEVER-SURFACE"
	testAudience  = "urn:stackit:iam:project:11111111-1111-1111-1111-111111111111"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixtureServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func validIdentity(srv *httptest.Server) stackitid.Identity {
	return stackitid.Identity{
		TokenURL:  srv.URL + "/token",
		Audience:  testAudience,
		Assertion: leakAssertion,
		Client:    srv.Client(),
	}
}

func jsonHandler(t *testing.T, status int, body string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestResolve_PostsFormEncodedTokenExchangeRequest(t *testing.T) {
	var gotMethod, gotContentType string
	var gotForm map[string][]string

	srv := fixtureServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		if err := r.ParseForm(); err != nil {
			t.Fatalf("server: parse form: %v", err)
		}
		gotForm = map[string][]string(r.PostForm)
		jsonHandler(t, http.StatusOK, `{"access_token":"tok","token_type":"Bearer","expires_in":3600}`)(w, r)
	})

	if _, err := stackitid.Resolve(context.Background(), validIdentity(srv)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.HasPrefix(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q, want form-encoded", gotContentType)
	}

	want := map[string]string{
		"grant_type":         "urn:ietf:params:oauth:grant-type:token-exchange",
		"subject_token":      leakAssertion,
		"subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
		"audience":           testAudience,
	}
	for k, v := range want {
		got := gotForm[k]
		if len(got) != 1 || got[0] != v {
			t.Errorf("form[%q] = %v, want [%q]", k, got, v)
		}
	}
}

func TestResolve_IncludesScopeWhenSet(t *testing.T) {
	var gotScope []string
	srv := fixtureServer(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("server: parse form: %v", err)
		}
		gotScope = r.PostForm["scope"]
		jsonHandler(t, http.StatusOK, `{"access_token":"tok","token_type":"Bearer","expires_in":3600}`)(w, r)
	})

	id := validIdentity(srv)
	id.Scope = "resource-manager.projects"
	if _, err := stackitid.Resolve(context.Background(), id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotScope) != 1 || gotScope[0] != "resource-manager.projects" {
		t.Errorf("form[scope] = %v, want [resource-manager.projects]", gotScope)
	}
}

func TestResolve_OmitsScopeParameterWhenUnset(t *testing.T) {
	var sawScope bool
	srv := fixtureServer(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("server: parse form: %v", err)
		}
		_, sawScope = r.PostForm["scope"]
		jsonHandler(t, http.StatusOK, `{"access_token":"tok","token_type":"Bearer","expires_in":3600}`)(w, r)
	})

	id := validIdentity(srv)
	id.Scope = ""
	if _, err := stackitid.Resolve(context.Background(), id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawScope {
		t.Error("scope form field present with an empty Identity.Scope, want it omitted")
	}
}

func TestResolve_ReturnsCredentialFromTokenResponse(t *testing.T) {
	srv := fixtureServer(t, jsonHandler(t, http.StatusOK, `{"access_token":"the-access-token","token_type":"Bearer","expires_in":3600}`))

	before := time.Now()
	cred, err := stackitid.Resolve(context.Background(), validIdentity(srv))
	after := time.Now()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cred.AccessToken != "the-access-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "the-access-token")
	}
	if cred.TokenType != "Bearer" {
		t.Errorf("TokenType = %q, want %q", cred.TokenType, "Bearer")
	}
	wantMin := before.Add(3600 * time.Second)
	wantMax := after.Add(3600 * time.Second)
	if cred.Expires.Before(wantMin) || cred.Expires.After(wantMax) {
		t.Errorf("Expires = %v, want between %v and %v (expires_in converted to an absolute time)", cred.Expires, wantMin, wantMax)
	}
}

func TestResolve_RejectsEmptyAccessTokenInResponse(t *testing.T) {
	srv := fixtureServer(t, jsonHandler(t, http.StatusOK, `{"access_token":"","token_type":"Bearer","expires_in":3600}`))

	_, err := stackitid.Resolve(context.Background(), validIdentity(srv))
	if err == nil {
		t.Fatal("expected an error for an incomplete credential, want it rejected rather than cached")
	}
}

func TestResolve_MissingExpiresInLeavesExpiresZero(t *testing.T) {
	srv := fixtureServer(t, jsonHandler(t, http.StatusOK, `{"access_token":"tok","token_type":"Bearer"}`))

	cred, err := stackitid.Resolve(context.Background(), validIdentity(srv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cred.Expires.IsZero() {
		t.Errorf("Expires = %v, want zero when expires_in is absent (credrefresh treats zero as unusable-for-caching)", cred.Expires)
	}
}

func TestResolve_ZeroExpiresInLeavesExpiresZero(t *testing.T) {
	srv := fixtureServer(t, jsonHandler(t, http.StatusOK, `{"access_token":"tok","token_type":"Bearer","expires_in":0}`))

	cred, err := stackitid.Resolve(context.Background(), validIdentity(srv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cred.Expires.IsZero() {
		t.Errorf("Expires = %v, want zero when expires_in is 0", cred.Expires)
	}
}

func TestResolve_RejectsNonJSONResponseBody(t *testing.T) {
	srv := fixtureServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("this is not json"))
	})

	_, err := stackitid.Resolve(context.Background(), validIdentity(srv))
	if err == nil {
		t.Fatal("expected an error for a non-JSON response body")
	}
}

func TestResolve_ServerErrorReturnsError(t *testing.T) {
	srv := fixtureServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	})

	_, err := stackitid.Resolve(context.Background(), validIdentity(srv))
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestResolve_InvalidGrantErrorNamesTheRejection(t *testing.T) {
	const description = "assertion signature verification failed"
	srv := fixtureServer(t, jsonHandler(t, http.StatusBadRequest,
		`{"error":"invalid_grant","error_description":"`+description+`"}`))

	_, err := stackitid.Resolve(context.Background(), validIdentity(srv))
	if err == nil {
		t.Fatal("expected an error for a rejected assertion")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error = %v, want it to name invalid_grant", err)
	}
	if !strings.Contains(err.Error(), description) {
		t.Errorf("error = %v, want it to carry the RFC 6749 error_description", err)
	}
	if !errors.Is(err, stackitid.ErrInvalidGrant) {
		t.Errorf("error = %v, want errors.Is(err, stackitid.ErrInvalidGrant) so an operator can map it to statusDetail", err)
	}
}

func TestResolve_RequiresTokenURL(t *testing.T) {
	id := stackitid.Identity{Audience: testAudience, Assertion: leakAssertion}
	if _, err := stackitid.Resolve(context.Background(), id); err == nil {
		t.Error("expected an error when TokenURL is empty")
	}
}

func TestResolve_RequiresAudience(t *testing.T) {
	id := stackitid.Identity{TokenURL: "https://sts.stackit.cloud/token", Assertion: leakAssertion}
	if _, err := stackitid.Resolve(context.Background(), id); err == nil {
		t.Error("expected an error when Audience is empty")
	}
}

func TestResolve_RequiresAssertion(t *testing.T) {
	id := stackitid.Identity{TokenURL: "https://sts.stackit.cloud/token", Audience: testAudience}
	if _, err := stackitid.Resolve(context.Background(), id); err == nil {
		t.Error("expected an error when Assertion is empty")
	}
}

func TestResolve_RejectsNonHTTPSTokenURL(t *testing.T) {
	// The unreachable TEST-NET-2 address would hang if the package tried to connect, so a fast error proves rejection precedes any network attempt.
	id := stackitid.Identity{
		TokenURL:  "http://198.51.100.1:1/token",
		Audience:  testAudience,
		Assertion: leakAssertion,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := stackitid.Resolve(ctx, id)
	if err == nil {
		t.Fatal("expected an error for a non-HTTPS TokenURL")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error = %v, want it to name the https requirement (and to fail before dialing)", err)
	}
}

func TestResolve_RejectsUnparseableTokenURL(t *testing.T) {
	id := stackitid.Identity{
		TokenURL:  "://not-a-url",
		Audience:  testAudience,
		Assertion: leakAssertion,
	}
	if _, err := stackitid.Resolve(context.Background(), id); err == nil {
		t.Error("expected an error for an unparseable TokenURL")
	}
}

func TestResolve_HonoursContextCancellation(t *testing.T) {
	srv := fixtureServer(t, jsonHandler(t, http.StatusOK, `{"access_token":"tok","token_type":"Bearer","expires_in":3600}`))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := stackitid.Resolve(ctx, validIdentity(srv))
	if err == nil {
		t.Fatal("expected an error for an already-cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
}

func TestResolve_TransportFailureReturnsError(t *testing.T) {
	id := stackitid.Identity{
		TokenURL:  "https://sts.stackit.cloud/token",
		Audience:  testAudience,
		Assertion: leakAssertion,
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp: connection refused")
		})},
	}
	if _, err := stackitid.Resolve(context.Background(), id); err == nil {
		t.Error("expected an error when the transport fails")
	}
}

func TestRefresher_ReusesCredentialUntilNearExpiry(t *testing.T) {
	var calls int
	now := time.Now()
	r := stackitid.NewRefresher(func(context.Context) (stackitid.Credential, error) {
		calls++
		return stackitid.Credential{AccessToken: "tok", Expires: now.Add(time.Hour)}, nil
	})
	r.Now = func() time.Time { return now }

	for range 3 {
		if _, err := r.Get(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("resolved %d times, want 1 — a valid credential must not be re-minted per call", calls)
	}
}

func TestRefresher_FailsClosedWhenCredentialComesBackAlreadyExpired(t *testing.T) {
	now := time.Now()
	r := stackitid.NewRefresher(func(context.Context) (stackitid.Credential, error) {
		return stackitid.Credential{AccessToken: "tok", Expires: now.Add(-time.Hour)}, nil
	})
	r.Now = func() time.Time { return now }

	got, err := r.Get(context.Background())
	if err == nil {
		t.Fatal("an already-expired credential must fail closed, never be returned")
	}
	if got.AccessToken != "" {
		t.Errorf("returned credential %+v alongside the error — the caller must get nothing usable", got)
	}
}

func TestRefresher_FailsClosedWhenResolverErrors(t *testing.T) {
	now := time.Now()
	r := stackitid.NewRefresher(func(context.Context) (stackitid.Credential, error) {
		return stackitid.Credential{}, errors.New("token endpoint unreachable")
	})
	r.Now = func() time.Time { return now }

	got, err := r.Get(context.Background())
	if err == nil {
		t.Fatal("expected the resolver's error to propagate")
	}
	if got.AccessToken != "" {
		t.Errorf("returned credential %+v alongside the error — the caller must get nothing usable", got)
	}
}

func TestFromIdentity_ResolvesUsingTheGivenIdentity(t *testing.T) {
	var calls int
	srv := fixtureServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		jsonHandler(t, http.StatusOK, `{"access_token":"tok","token_type":"Bearer","expires_in":3600}`)(w, r)
	})

	r := stackitid.FromIdentity(validIdentity(srv))
	got, err := r.Get(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AccessToken != "tok" {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, "tok")
	}
	if calls != 1 {
		t.Errorf("token endpoint called %d times, want 1", calls)
	}
}
