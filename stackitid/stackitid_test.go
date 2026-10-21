package stackitid_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/kittest"
	"github.com/mitstack-eu/operator-kit/v3/stackitid"
)

const (
	leakAssertion   = "STACKIT-ASSERTION-JWT-THAT-MUST-NEVER-SURFACE"
	leakAccessToken = "STACKIT-ACCESS-TOKEN-THAT-MUST-NEVER-SURFACE"
	testAudience    = "urn:stackit:iam:project:11111111-1111-1111-1111-111111111111"
	okBody          = `{"access_token":"tok","token_type":"Bearer","expires_in":3600}`
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// tokenRequest is the last request the fixture token endpoint received, and how many it received.
type tokenRequest struct {
	method, contentType string
	form                url.Values
	n                   int
}

// endpoint serves status and body over TLS and returns an identity pointed at it, recording each request into got.
func endpoint(t *testing.T, status int, body string, got *tokenRequest) stackitid.Identity {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("server: parse form: %v", err)
		}
		if got != nil {
			*got = tokenRequest{r.Method, r.Header.Get("Content-Type"), r.PostForm, got.n + 1}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return stackitid.Identity{TokenURL: srv.URL + "/token", Audience: testAudience, Assertion: leakAssertion, Client: srv.Client()}
}

func TestResolve_ExchangesTheAssertion(t *testing.T) {
	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {leakAssertion},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:jwt"},
		"audience":           {testAudience},
	}
	withScope := url.Values{"scope": {"resource-manager.projects"}}
	for k, v := range form {
		withScope[k] = v
	}
	cases := []struct {
		name, scope, body string
		wantForm          url.Values
		want              stackitid.Credential
		wantExpiresIn     time.Duration
	}{
		{"it posts the form-encoded exchange and returns the token with its absolute expiry", "", `{"access_token":"the-access-token","token_type":"Bearer","expires_in":3600}`,
			form, stackitid.Credential{AccessToken: "the-access-token", TokenType: "Bearer"}, 3600 * time.Second},
		{"a set scope is sent", "resource-manager.projects", okBody, withScope, stackitid.Credential{AccessToken: "tok", TokenType: "Bearer"}, 3600 * time.Second},
		{"a missing expires_in leaves the expiry zero, which is never cached", "", `{"access_token":"tok","token_type":"Bearer"}`, form, stackitid.Credential{AccessToken: "tok", TokenType: "Bearer"}, 0},
		{"a zero expires_in leaves the expiry zero", "", `{"access_token":"tok","token_type":"Bearer","expires_in":0}`, form, stackitid.Credential{AccessToken: "tok", TokenType: "Bearer"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got tokenRequest
			id := endpoint(t, http.StatusOK, tc.body, &got)
			id.Scope = tc.scope
			before := time.Now()

			cred, err := stackitid.Resolve(context.Background(), id)

			after := time.Now()
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got.n != 1 || got.method != http.MethodPost || !strings.HasPrefix(got.contentType, "application/x-www-form-urlencoded") || !reflect.DeepEqual(got.form, tc.wantForm) {
				t.Errorf("request = %+v, want a form-encoded POST of %v", got, tc.wantForm)
			}
			if expires := cred.Expires; tc.wantExpiresIn == 0 && !expires.IsZero() || tc.wantExpiresIn != 0 && (expires.Before(before.Add(tc.wantExpiresIn)) || expires.After(after.Add(tc.wantExpiresIn))) {
				t.Errorf("Expires = %v, want %v after the exchange (zero for none)", expires, tc.wantExpiresIn)
			}
			cred.Expires = time.Time{}
			if cred != tc.want {
				t.Errorf("credential = %+v, want %+v", cred, tc.want)
			}
		})
	}
}

// Every failure is an error carrying neither the assertion nor any other request material.
func TestResolve_FailsWithoutLeakingTheAssertion(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name      string
		id        func(t *testing.T) stackitid.Identity
		ctx       context.Context
		wantIs    error
		wantInErr []string
	}{
		{"no TokenURL", func(*testing.T) stackitid.Identity {
			return stackitid.Identity{Audience: testAudience, Assertion: leakAssertion}
		}, nil, nil, nil},
		{"no Audience", func(*testing.T) stackitid.Identity {
			return stackitid.Identity{TokenURL: "https://sts.stackit.cloud/token", Assertion: leakAssertion}
		}, nil, nil, nil},
		{"no Assertion", func(*testing.T) stackitid.Identity {
			return stackitid.Identity{TokenURL: "https://sts.stackit.cloud/token", Audience: testAudience}
		}, nil, nil, nil},
		{"a TokenURL that is not https is refused before any dial to its unreachable host", func(*testing.T) stackitid.Identity {
			return stackitid.Identity{TokenURL: "http://198.51.100.1:1/token", Audience: testAudience, Assertion: leakAssertion}
		}, nil, nil, []string{"https"}},
		{"a TokenURL that does not parse", func(*testing.T) stackitid.Identity {
			return stackitid.Identity{TokenURL: "://not-a-url", Audience: testAudience, Assertion: leakAssertion}
		}, nil, nil, nil},
		{"a rejected assertion names invalid_grant and its description", func(t *testing.T) stackitid.Identity {
			return endpoint(t, http.StatusBadRequest, `{"error":"invalid_grant","error_description":"assertion signature verification failed"}`, nil)
		}, nil, stackitid.ErrInvalidGrant, []string{"invalid_grant", "assertion signature verification failed"}},
		{"a server error", func(t *testing.T) stackitid.Identity {
			return endpoint(t, http.StatusInternalServerError, "internal server error", nil)
		}, nil, nil, nil},
		{"a body that is not JSON", func(t *testing.T) stackitid.Identity { return endpoint(t, http.StatusOK, "this is not json", nil) }, nil, nil, nil},
		{"an empty access token, which must not be cached", func(t *testing.T) stackitid.Identity {
			return endpoint(t, http.StatusOK, `{"access_token":"","token_type":"Bearer","expires_in":3600}`, nil)
		}, nil, nil, nil},
		{"an already cancelled context", func(t *testing.T) stackitid.Identity { return endpoint(t, http.StatusOK, okBody, nil) }, cancelled, context.Canceled, nil},
		{"a transport failure whose text carries the assertion", func(*testing.T) stackitid.Identity {
			return stackitid.Identity{TokenURL: "https://sts.stackit.cloud/token", Audience: testAudience, Assertion: leakAssertion,
				Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("dial tcp: connection refused, assertion=" + leakAssertion)
				})}}
		}, nil, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tc.ctx
			if ctx == nil {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
			}
			id := tc.id(t)

			cred, err := stackitid.Resolve(ctx, id)

			if err == nil {
				t.Fatalf("Resolve = %+v; want an error", cred)
			}
			kittest.AssertNoSecret(t, "the Resolve error", err.Error(), leakAssertion)
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("Resolve error = %v, want it to wrap %v", err, tc.wantIs)
			}
			for _, want := range tc.wantInErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Resolve error = %v, want it to name %q", err, want)
				}
			}
		})
	}
}

func TestRefresher_Get(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		resolve   func() (stackitid.Credential, error)
		gets      int
		wantErr   bool
		wantCalls int
	}{
		{"a valid credential is reused rather than re-minted per call", func() (stackitid.Credential, error) {
			return stackitid.Credential{AccessToken: "tok", Expires: now.Add(time.Hour)}, nil
		}, 3, false, 1},
		{"a credential that comes back already expired fails closed", func() (stackitid.Credential, error) {
			return stackitid.Credential{AccessToken: "tok", Expires: now.Add(-time.Hour)}, nil
		}, 1, true, 1},
		{"a resolver error fails closed", func() (stackitid.Credential, error) {
			return stackitid.Credential{}, errors.New("token endpoint unreachable")
		}, 1, true, 1},
		{"the fail-closed error carries no credential material", func() (stackitid.Credential, error) {
			return stackitid.Credential{AccessToken: leakAccessToken, Expires: now.Add(-time.Hour)}, nil
		}, 1, true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			r := stackitid.NewRefresher(func(context.Context) (stackitid.Credential, error) { calls++; return tc.resolve() })
			r.Now = func() time.Time { return now }

			var got stackitid.Credential
			var err error
			for range tc.gets {
				got, err = r.Get(context.Background())
			}

			if (err != nil) != tc.wantErr || calls != tc.wantCalls {
				t.Errorf("Get = %+v, %v after %d resolves; want error %v after %d", got, err, calls, tc.wantErr, tc.wantCalls)
			}
			if tc.wantErr {
				if got.AccessToken != "" {
					t.Errorf("Get returned %+v alongside its error; the caller must get nothing usable", got)
				}
				kittest.AssertNoSecret(t, "the Refresher error", err.Error(), leakAccessToken)
			}
		})
	}
}

func TestFromIdentity_ResolvesUsingTheGivenIdentity(t *testing.T) {
	var req tokenRequest
	r := stackitid.FromIdentity(endpoint(t, http.StatusOK, okBody, &req))

	got, err := r.Get(context.Background())

	if err != nil || got.AccessToken != "tok" || req.n != 1 {
		t.Errorf("Get = %+v, %v after request %+v; want token tok from one exchange", got, err, req)
	}
}

func TestResolve_LogsNoSecretOnAnyPath(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	ok := endpoint(t, http.StatusOK, `{"access_token":"`+leakAccessToken+`","token_type":"Bearer","expires_in":3600}`, nil)
	rejected := endpoint(t, http.StatusBadRequest, `{"error":"invalid_grant","error_description":"assertion signature verification failed"}`, nil)
	expired := stackitid.NewRefresher(func(context.Context) (stackitid.Credential, error) {
		return stackitid.Credential{AccessToken: leakAccessToken, Expires: time.Now().Add(-time.Hour)}, nil
	})

	if _, err := stackitid.Resolve(context.Background(), ok); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	_, _ = stackitid.Resolve(context.Background(), rejected)
	_, _ = expired.Get(context.Background())

	kittest.AssertNoSecret(t, "the log", buf.String(), leakAssertion, leakAccessToken)
}
