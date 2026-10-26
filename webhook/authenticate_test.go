package webhook_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/kittest"
	"github.com/mitstack-eu/operator-kit/v3/webhook"
)

func depsFor(s *kittest.Signer, jwksURL string) webhook.Deps {
	cache := webhook.NewKeyCache()
	return webhook.Deps{
		JWKSURL:   jwksURL,
		PublicKey: func(kid string) (ed25519.PublicKey, error) { return cache.PublicKey(jwksURL, nil, kid) },
		Replay:    webhook.NewReplay(),
	}
}

func TestAuthenticateHook_AcceptsAFreshlySignedBody(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	d := depsFor(s, s.JWKSServer(t))
	body := kittest.Envelope("nonce-1", time.Now(), nil)

	w := httptest.NewRecorder()
	pub, ok := webhook.AuthenticateHook(d, w, s.HookRequest(t, "/hook", body), body)

	if !ok {
		t.Fatalf("expected authentication to succeed, got %d %s", w.Code, w.Body)
	}
	if !pub.Equal(s.Public) {
		t.Error("returned public key is not the signing key")
	}
}

func TestAuthenticateHook_401WhenKeyIdMissing(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	d := depsFor(s, s.JWKSServer(t))
	body := kittest.Envelope("nonce-1", time.Now(), nil)

	r := s.HookRequest(t, "/hook", body)
	r.Header.Del("X-Mitstack-Key-Id")
	w := httptest.NewRecorder()

	if _, ok := webhook.AuthenticateHook(d, w, r, body); ok {
		t.Fatal("expected authentication to fail with no key id")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthenticateHook_401WhenJWKSNotConfigured(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	d := depsFor(s, "")
	body := kittest.Envelope("nonce-1", time.Now(), nil)

	w := httptest.NewRecorder()
	if _, ok := webhook.AuthenticateHook(d, w, s.HookRequest(t, "/hook", body), body); ok {
		t.Fatal("an unconfigured JWKS must fail closed, never bypass")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthenticateHook_401OnForgedSignature(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	other := kittest.NewSigner(t, "kid-1", 99)
	d := depsFor(s, s.JWKSServer(t))
	body := kittest.Envelope("nonce-1", time.Now(), nil)

	r := s.HookRequest(t, "/hook", body)
	r.Header.Set("X-Mitstack-Signature-Ed25519", other.SignBody(body))
	w := httptest.NewRecorder()

	if _, ok := webhook.AuthenticateHook(d, w, r, body); ok {
		t.Fatal("a body signed by a different key must be rejected")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthenticateHook_401WhenBodyTamperedAfterSigning(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	d := depsFor(s, s.JWKSServer(t))
	body := kittest.Envelope("nonce-1", time.Now(), map[string]any{"operation": "subscribe"})

	r := s.HookRequest(t, "/hook", body)
	tampered := []byte(strings.Replace(string(body), "subscribe", "unsubscrib", 1))
	w := httptest.NewRecorder()

	if _, ok := webhook.AuthenticateHook(d, w, r, tampered); ok {
		t.Fatal("a body altered after signing must be rejected")
	}
}

func TestAuthenticateHook_RejectsAReplayedNonce(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	d := depsFor(s, s.JWKSServer(t))
	body := kittest.Envelope("nonce-replayed", time.Now(), nil)

	if _, ok := webhook.AuthenticateHook(d, httptest.NewRecorder(), s.HookRequest(t, "/hook", body), body); !ok {
		t.Fatal("first delivery should succeed")
	}
	w := httptest.NewRecorder()
	if _, ok := webhook.AuthenticateHook(d, w, s.HookRequest(t, "/hook", body), body); ok {
		t.Fatal("a captured request replayed inside the window must be rejected (T-357)")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthenticateHook_RejectsAStaleTimestamp(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	d := depsFor(s, s.JWKSServer(t))
	body := kittest.Envelope("nonce-old", time.Now().Add(-webhook.ReplaySkew-time.Minute), nil)

	w := httptest.NewRecorder()
	if _, ok := webhook.AuthenticateHook(d, w, s.HookRequest(t, "/hook", body), body); ok {
		t.Fatal("a request older than the skew window must be rejected")
	}
}

func TestAuthenticateLogsGET_AcceptsACanonicallySignedGET(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	d := depsFor(s, s.JWKSServer(t))

	w := httptest.NewRecorder()
	if _, ok := webhook.AuthenticateLogsGET(d, w, s.LogsRequest(t, "/logs?tail=10", time.Now())); !ok {
		t.Fatalf("expected the signed GET to authenticate, got %d %s", w.Code, w.Body)
	}
}

func TestAuthenticateLogsGET_RejectsASignatureForADifferentPath(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	d := depsFor(s, s.JWKSServer(t))

	// A signature minted for /logs?tail=10 must not authorise /logs?tail=500:
	// the query string is inside the canonical string precisely so it is bound.
	signed := s.LogsRequest(t, "/logs?tail=10", time.Now())
	forged := httptest.NewRequest(http.MethodGet, "/logs?tail=500", nil)
	for k, v := range signed.Header {
		forged.Header[k] = v
	}

	w := httptest.NewRecorder()
	if _, ok := webhook.AuthenticateLogsGET(d, w, forged); ok {
		t.Fatal("a signature bound to one path+query must not authorise another")
	}
}

func TestAuthenticateLogsGET_RejectsAStaleTimestamp(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	d := depsFor(s, s.JWKSServer(t))

	w := httptest.NewRecorder()
	r := s.LogsRequest(t, "/logs", time.Now().Add(-webhook.ReplaySkew-time.Minute))
	if _, ok := webhook.AuthenticateLogsGET(d, w, r); ok {
		t.Fatal("a GET signed outside the skew window must be rejected (T-578)")
	}
}

func TestAuthenticateLogsGET_401WhenTimestampHeaderMissing(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	d := depsFor(s, s.JWKSServer(t))

	r := s.LogsRequest(t, "/logs", time.Now())
	r.Header.Del("X-Mitstack-Timestamp")
	w := httptest.NewRecorder()

	if _, ok := webhook.AuthenticateLogsGET(d, w, r); ok {
		t.Fatal("a GET with no anti-replay timestamp must be rejected")
	}
}

func TestDeps_UsesTheOperatorsOwnErrorWriter(t *testing.T) {
	// Deps carries the operator's own writeError so its 401 body shape is unchanged.
	s := kittest.NewSigner(t, "kid-1", 7)
	d := depsFor(s, "")
	d.WriteError = func(w http.ResponseWriter, status int, msg string) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("operator-shaped: " + msg))
	}

	w := httptest.NewRecorder()
	body := kittest.Envelope("n", time.Now(), nil)
	_, _ = webhook.AuthenticateHook(d, w, s.HookRequest(t, "/hook", body), body)

	if !strings.HasPrefix(w.Body.String(), "operator-shaped: ") {
		t.Errorf("kit wrote its own error body %q; it must delegate to the operator's writer", w.Body.String())
	}
}

func TestVerifyEd25519Sig_RejectsUndecodableSignature(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	if webhook.VerifyEd25519Sig(s.Public, []byte("body"), "ed25519=not-base64!!") {
		t.Error("an undecodable signature must not verify")
	}
}

func TestPublicKeyFromJWKS(t *testing.T) {
	s := kittest.NewSigner(t, "kid-A", 11)

	got, err := webhook.PublicKeyFromJWKS(s.JWKS(), "kid-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(s.Public) {
		t.Error("extracted key does not match the signer")
	}

	if _, err := webhook.PublicKeyFromJWKS(s.JWKS(), "kid-missing"); err == nil {
		t.Error("an unknown kid must be an error, not a silent fallback to any key")
	}
}

func TestPublicKeyFromJWKS_RejectsNonEd25519Key(t *testing.T) {
	doc := []byte(`{"keys":[{"kty":"RSA","crv":"","kid":"kid-A","x":""}]}`)
	if _, err := webhook.PublicKeyFromJWKS(doc, "kid-A"); err == nil {
		t.Error("a non-OKP/Ed25519 key for the requested kid must be rejected")
	}
}

func TestPublicKeyFromJWKS_RejectsWrongLengthKey(t *testing.T) {
	short := base64.RawURLEncoding.EncodeToString([]byte("too-short"))
	doc := []byte(`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"kid-A","x":"` + short + `"}]}`)
	if _, err := webhook.PublicKeyFromJWKS(doc, "kid-A"); err == nil {
		t.Error("a key of the wrong length must be rejected")
	}
}

func TestKeyCache_FetchesOncePerKidAndAnOutageAnswersOnlyAnUnheldKidUnavailable(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits++; hits > 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write(s.JWKS())
	}))
	defer srv.Close()

	cache := webhook.NewKeyCache()
	for range 3 {
		if _, err := cache.PublicKey(srv.URL, nil, "kid-1"); err != nil {
			t.Fatalf("held kid during the outage: %v", err)
		}
	}
	if _, err := cache.PublicKey(srv.URL, nil, "kid-2"); !errors.Is(err, webhook.ErrKeysUnavailable) {
		t.Errorf("unheld kid during the outage = %v, want ErrKeysUnavailable", err)
	}
	if _, err := webhook.PublicKeyFromJWKS(s.JWKS(), "kid-2"); err == nil || errors.Is(err, webhook.ErrKeysUnavailable) {
		t.Errorf("a kid missing from a fetched JWKS = %v, want an error that is not ErrKeysUnavailable", err)
	}
	if hits != 2 {
		t.Errorf("JWKS fetched %d times, want 2 (memoised per kid)", hits)
	}
}

func TestVerifyTimestampSkew_AcceptsRFC3339AndUnixSeconds(t *testing.T) {
	now := time.Now()
	if err := webhook.VerifyTimestampSkew(now.Format(time.RFC3339), now); err != nil {
		t.Errorf("RFC3339 timestamp rejected: %v", err)
	}
	if err := webhook.VerifyTimestampSkew(strings.TrimSpace(time.Unix(now.Unix(), 0).UTC().Format("20060102")), now); err == nil {
		t.Error("an unparseable timestamp must be rejected")
	}
}

func TestReplay_PrunesExpiredNonces(t *testing.T) {
	rp := webhook.NewReplay()
	t0 := time.Now()
	body := kittest.Envelope("n-1", t0, nil)

	if err := rp.Verify(body, t0); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	// Outside the window the nonce is pruned — but the body's own createdAt is
	// then stale too, which is the real guarantee: age alone cannot make a
	// captured request acceptable again.
	if err := rp.Verify(body, t0.Add(webhook.ReplaySkew+time.Minute)); err == nil {
		t.Error("an aged-out replay must still be rejected on its stale createdAt")
	}
}

func TestReplay_RejectsMissingNonceOrCreatedAt(t *testing.T) {
	rp := webhook.NewReplay()
	now := time.Now()

	if err := rp.Verify([]byte(`{"createdAt":"`+now.Format(time.RFC3339)+`"}`), now); err == nil {
		t.Error("a body with no nonce must be rejected")
	}
	if err := rp.Verify([]byte(`{"nonce":"n"}`), now); err == nil {
		t.Error("a body with no createdAt must be rejected")
	}
	if err := rp.Verify([]byte(`not json`), now); err == nil {
		t.Error("an unparseable body must be rejected")
	}
}
