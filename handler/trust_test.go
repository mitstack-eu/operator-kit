package handler_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/webhook"
)

// The trust-boundary scenarios below mirror webhook/authenticate_test.go's
// own cases, run through Serve rather than AuthenticateHook directly: a
// signature failure of any kind is a 401 already written before the
// Resource is ever consulted, and every fake counter must stay at zero.

func TestServe_401WhenKeyIdMissing(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	r := s.HookRequest(t, "/hook", envelope(t, s, "nonce-trust-nokid", nil))
	r.Header.Del("X-Mitstack-Key-Id")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	zeroCalls(t, f)
}

func TestServe_401OnForgedSignature(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	other := kittest.NewSigner(t, "kid-1", 99)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	body := envelope(t, s, "nonce-trust-forged", nil)
	r := s.HookRequest(t, "/hook", body)
	r.Header.Set("X-Mitstack-Signature-Ed25519", other.SignBody(body))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	zeroCalls(t, f)
}

func TestServe_401WhenBodyTamperedAfterSigning(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	original := envelope(t, s, "nonce-trust-tamper", map[string]any{"operation": "subscribe"})
	r := s.HookRequest(t, "/hook", original) // headers signed over `original`
	tampered := []byte(strings.Replace(string(original), "subscribe", "unsubscrib", 1))
	r.Body = io.NopCloser(strings.NewReader(string(tampered))) // but the wire body is `tampered`

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	zeroCalls(t, f)
}

func TestServe_401OnAStaleCreatedAt(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	fields := map[string]any{"operation": "subscribe", "subscriptionId": "sub-1", "blockId": "block-1", "orgId": "org-1"}
	body := kittest.Envelope("nonce-trust-stale", time.Now().Add(-webhook.ReplaySkew-time.Minute), fields)

	status, _ := postHook(t, h, s, body)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
	zeroCalls(t, f)
}

func TestServe_401OnAReplayedNonce(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	body := envelope(t, s, "nonce-trust-replay", nil)

	status1, _ := postHook(t, h, s, body)
	if status1 != http.StatusOK {
		t.Fatalf("first delivery: status = %d, want 200", status1)
	}
	if f.EnsureCalls != 1 {
		t.Fatalf("EnsureCalls after first delivery = %d, want 1", f.EnsureCalls)
	}

	status2, _ := postHook(t, h, s, body) // exact same body, same nonce
	if status2 != http.StatusUnauthorized {
		t.Errorf("replay status = %d, want 401", status2)
	}
	if f.EnsureCalls != 1 {
		t.Errorf("EnsureCalls after the replay = %d, want still 1", f.EnsureCalls)
	}
}

// TestServe_401OnAnInvalidOBOToken pins that a malformed/garbage OBO token
// is rejected with 401 and never reaches the Resource, even though the
// outer mitstack signature verifies fine.
func TestServe_401OnAnInvalidOBOToken(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	body := envelope(t, s, "nonce-trust-obo", map[string]any{"obo": "not-a-real-obo-token"})
	status, _ := postHook(t, h, s, body)

	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an invalid OBO token", status)
	}
	zeroCalls(t, f)
}

// TestServe_400OnAMalformedEnvelope pins that a properly-signed body that
// fails contract.DecodeRequest (missing a required field) is 400, not 401 —
// the signature is valid, the envelope inside it is not — and never reaches
// the Resource.
func TestServe_400OnAMalformedEnvelope(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	// blockId is required by contract.DecodeRequest and deliberately omitted.
	fields := map[string]any{"operation": "subscribe", "subscriptionId": "sub-1", "orgId": "org-1"}
	body := kittest.Envelope("nonce-trust-malformed", time.Now(), fields)

	status, _ := postHook(t, h, s, body)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an envelope missing a required field", status)
	}
	zeroCalls(t, f)
}
