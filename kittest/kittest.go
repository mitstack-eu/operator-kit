// Package kittest, part of the core every block needs, provides the test scaffolding every operator needs to exercise the mitstack trust boundary, from a deterministic signing identity to the conformance checks.
package kittest

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Signer is a deterministic stand-in for mitstack's platform signing identity.
type Signer struct {
	Kid     string
	Public  ed25519.PublicKey
	Private ed25519.PrivateKey
}

// NewSigner returns a signer seeded deterministically from seed, so a failing
// test reproduces byte for byte rather than depending on entropy.
func NewSigner(t *testing.T, kid string, seed byte) *Signer {
	t.Helper()
	raw := make([]byte, ed25519.SeedSize)
	for i := range raw {
		raw[i] = seed + byte(i)
	}
	priv := ed25519.NewKeyFromSeed(raw)
	return &Signer{Kid: kid, Public: priv.Public().(ed25519.PublicKey), Private: priv}
}

// JWKS returns the signer's public key as a JWKS document.
func (s *Signer) JWKS() []byte {
	doc := map[string]any{"keys": []map[string]string{{
		"kty": "OKP",
		"crv": "Ed25519",
		"kid": s.Kid,
		"x":   base64.RawURLEncoding.EncodeToString(s.Public),
	}}}
	raw, _ := json.Marshal(doc)
	return raw
}

// JWKSServer starts an httptest server serving this signer's JWKS and returns
// its URL. It is shut down when the test ends.
func (s *Signer) JWKSServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(s.JWKS())
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// SignBody returns the detached signature header value for a webhook body.
func (s *Signer) SignBody(body []byte) string {
	return "ed25519=" + base64.StdEncoding.EncodeToString(ed25519.Sign(s.Private, body))
}

// HookRequest builds a correctly-signed POST /hook request for body.
func (s *Signer) HookRequest(t *testing.T, path string, body []byte) *http.Request {
	t.Helper()
	return s.hookRequest(path, body)
}

func (s *Signer) hookRequest(path string, body []byte) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	r.Header.Set("X-Mitstack-Key-Id", s.Kid)
	r.Header.Set("X-Mitstack-Signature-Ed25519", s.SignBody(body))
	return r
}

// LogsRequest builds a signed read-only GET whose signature covers "GET\n<path-with-query>\n<timestamp>".
func (s *Signer) LogsRequest(t *testing.T, path string, at time.Time) *http.Request {
	t.Helper()
	return s.logsRequest(path, at)
}

func (s *Signer) logsRequest(path string, at time.Time) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	ts := at.UTC().Format(time.RFC3339)
	canonical := []byte("GET\n" + r.URL.RequestURI() + "\n" + ts)
	r.Header.Set("X-Mitstack-Key-Id", s.Kid)
	r.Header.Set("X-Mitstack-Timestamp", ts)
	r.Header.Set("X-Mitstack-Signature-Ed25519", "ed25519="+base64.StdEncoding.EncodeToString(ed25519.Sign(s.Private, canonical)))
	return r
}

// Envelope builds a minimal signed-webhook body with a fresh nonce and
// createdAt, merged with any extra top-level fields the operator needs.
func Envelope(nonce string, at time.Time, extra map[string]any) []byte {
	body := map[string]any{"nonce": nonce, "createdAt": at.UTC().Format(time.RFC3339Nano)}
	for k, v := range extra {
		body[k] = v
	}
	raw, _ := json.Marshal(body)
	return raw
}

// AssertNoSecret fails the test if any of secrets appears anywhere in haystack.
func AssertNoSecret(t *testing.T, what string, haystack string, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if s == "" {
			continue
		}
		if strings.Contains(haystack, s) {
			t.Errorf("%s leaked secret %q\nin: %s", what, redact(s), haystack)
		}
	}
}

func redact(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return fmt.Sprintf("%s…(%d chars)", s[:2], len(s))
}
