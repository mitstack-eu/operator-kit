package webhook

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Deps is what the two Authenticate functions need from their host operator, with WriteError being the operator's own writer so its 401 body shape is unchanged.
type Deps struct {
	// JWKSURL empty means not configured, which is an authentication failure and not a bypass.
	JWKSURL string

	PublicKey func(kid string) (ed25519.PublicKey, error)

	// Replay is required for AuthenticateHook and unused by AuthenticateLogsGET.
	Replay *Replay

	// WriteError defaults to a minimal JSON error body when nil.
	WriteError func(w http.ResponseWriter, status int, msg string)

	Now func() time.Time
}

func (d Deps) writeError(w http.ResponseWriter, status int, msg string) {
	if d.WriteError != nil {
		d.WriteError(w, status, msg)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d Deps) signingKey(w http.ResponseWriter, r *http.Request) (ed25519.PublicKey, bool) {
	kid := r.Header.Get("X-Mitstack-Key-Id")
	if kid == "" || d.JWKSURL == "" {
		d.writeError(w, http.StatusUnauthorized, "missing X-Mitstack-Key-Id or JWKS not configured")
		return nil, false
	}
	pub, err := d.PublicKey(kid)
	if errors.Is(err, ErrKeysUnavailable) {
		d.writeError(w, http.StatusServiceUnavailable, "cannot fetch mitstack signing keys: "+err.Error())
		return nil, false
	}
	if err != nil {
		d.writeError(w, http.StatusUnauthorized, "cannot resolve mitstack signing key: "+err.Error())
		return nil, false
	}
	return pub, true
}

// AuthenticateHook verifies mitstack's Ed25519 signature on the webhook body and returns the platform key, or false once a 401, or a 503 for unavailable keys, has been written.
func AuthenticateHook(d Deps, w http.ResponseWriter, r *http.Request, body []byte) (ed25519.PublicKey, bool) {
	pub, ok := d.signingKey(w, r)
	if !ok {
		return nil, false
	}
	if !VerifyEd25519Sig(pub, body, r.Header.Get("X-Mitstack-Signature-Ed25519")) {
		d.writeError(w, http.StatusUnauthorized, "ed25519 signature verification failed")
		return nil, false
	}
	if err := d.Replay.Verify(body, d.now()); err != nil {
		d.writeError(w, http.StatusUnauthorized, "replay check failed: "+err.Error())
		return nil, false
	}
	return pub, true
}

// AuthenticateLogsGET verifies the signature of a bodyless GET over "GET\n<path-with-query>\n<timestamp>" and bounds the X-Mitstack-Timestamp header by ReplaySkew, returning false once a 401, or a 503 for unavailable keys, has been written.
func AuthenticateLogsGET(d Deps, w http.ResponseWriter, r *http.Request) (ed25519.PublicKey, bool) {
	pub, ok := d.signingKey(w, r)
	if !ok {
		return nil, false
	}
	ts := r.Header.Get("X-Mitstack-Timestamp")
	if ts == "" {
		d.writeError(w, http.StatusUnauthorized, "missing X-Mitstack-Timestamp")
		return nil, false
	}
	canonical := []byte("GET\n" + r.URL.RequestURI() + "\n" + ts)
	if !VerifyEd25519Sig(pub, canonical, r.Header.Get("X-Mitstack-Signature-Ed25519")) {
		d.writeError(w, http.StatusUnauthorized, "ed25519 signature verification failed")
		return nil, false
	}
	if err := VerifyTimestampSkew(ts, d.now()); err != nil {
		d.writeError(w, http.StatusUnauthorized, "replay check failed: "+err.Error())
		return nil, false
	}
	return pub, true
}
