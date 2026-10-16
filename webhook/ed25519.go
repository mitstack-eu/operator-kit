// Package webhook, part of the core every block needs, implements the lifecycle-webhook trust boundary an operator consumes, verifying mitstack's detached Ed25519 signature against a public key fetched from its JWKS with no shared secret.
package webhook

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ReplaySkew bounds how far a signed request's createdAt may differ from this verifier's clock, closing the replay hole in a detached signature.
const ReplaySkew = 5 * time.Minute

// Replay rejects webhook bodies whose signed createdAt is outside ReplaySkew or whose nonce was already seen inside it.
type Replay struct {
	mu sync.Mutex
	// Points at the caller's map variable, so reassigning that variable resets the guard and a map held by value would not.
	nonces *map[string]time.Time
}

// NewReplay returns an empty replay guard owning its own nonce map.
func NewReplay() *Replay {
	m := map[string]time.Time{}
	return &Replay{nonces: &m}
}

// NewReplayOver returns a replay guard backed by the caller's map variable.
func NewReplayOver(nonces *map[string]time.Time) *Replay { return &Replay{nonces: nonces} }

// Verify rejects a webhook whose createdAt is outside the skew window or whose nonce was already seen, and must run after the signature check because it trusts both fields.
func (rp *Replay) Verify(body []byte, now time.Time) error {
	var env struct {
		CreatedAt time.Time `json:"createdAt"`
		Nonce     string    `json:"nonce"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return err
	}
	if env.Nonce == "" {
		return errors.New("missing nonce")
	}
	if env.CreatedAt.IsZero() {
		return errors.New("missing createdAt")
	}
	skew := now.Sub(env.CreatedAt)
	if skew < 0 {
		skew = -skew
	}
	if skew > ReplaySkew {
		return errors.New("stale request timestamp")
	}
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if rp.nonces == nil {
		m := map[string]time.Time{}
		rp.nonces = &m
	}
	if *rp.nonces == nil {
		*rp.nonces = map[string]time.Time{}
	}
	for n, t := range *rp.nonces {
		if now.Sub(t) > ReplaySkew {
			delete(*rp.nonces, n)
		}
	}
	if _, seen := (*rp.nonces)[env.Nonce]; seen {
		return errors.New("replayed nonce")
	}
	(*rp.nonces)[env.Nonce] = now
	return nil
}

// VerifyTimestampSkew bounds a GET's signed timestamp, in RFC3339 or unix seconds, by ReplaySkew, and must run after the signature check.
func VerifyTimestampSkew(ts string, now time.Time) error {
	if ts == "" {
		return errors.New("missing timestamp")
	}
	var t time.Time
	if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
		t = parsed
	} else if secs, err2 := strconv.ParseInt(ts, 10, 64); err2 == nil {
		t = time.Unix(secs, 0)
	} else {
		return errors.New("unparseable timestamp")
	}
	skew := now.Sub(t)
	if skew < 0 {
		skew = -skew
	}
	if skew > ReplaySkew {
		return errors.New("stale request timestamp")
	}
	return nil
}

// KeyCache memoises Ed25519 public keys fetched from a JWKS endpoint by kid, and is safe for concurrent use.
type KeyCache struct {
	mu sync.Mutex
	// Points at the caller's map variable for the same reason as Replay.nonces.
	keys *map[string]ed25519.PublicKey
}

// NewKeyCache returns an empty key cache owning its own map.
func NewKeyCache() *KeyCache {
	m := map[string]ed25519.PublicKey{}
	return &KeyCache{keys: &m}
}

// NewKeyCacheOver returns a key cache backed by the caller's map variable.
func NewKeyCacheOver(keys *map[string]ed25519.PublicKey) *KeyCache { return &KeyCache{keys: keys} }

// PublicKey returns the Ed25519 public key for kid, fetching it from jwksURL once, with a default 5s client when client is nil.
func (kc *KeyCache) PublicKey(jwksURL string, client *http.Client, kid string) (ed25519.PublicKey, error) {
	kc.mu.Lock()
	if kc.keys == nil {
		m := map[string]ed25519.PublicKey{}
		kc.keys = &m
	}
	if *kc.keys == nil {
		*kc.keys = map[string]ed25519.PublicKey{}
	}
	pub, ok := (*kc.keys)[kid]
	kc.mu.Unlock()
	if ok {
		return pub, nil
	}
	pub, err := fetchJWKSKey(jwksURL, client, kid)
	if err != nil {
		return nil, err
	}
	kc.mu.Lock()
	if *kc.keys == nil {
		*kc.keys = map[string]ed25519.PublicKey{}
	}
	(*kc.keys)[kid] = pub
	kc.mu.Unlock()
	return pub, nil
}

func fetchJWKSKey(jwksURL string, client *http.Client, kid string) (ed25519.PublicKey, error) {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Get(jwksURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	return PublicKeyFromJWKS(raw, kid)
}

// VerifyEd25519Sig strips the "ed25519=" prefix, base64-decodes, and verifies.
func VerifyEd25519Sig(pub ed25519.PublicKey, body []byte, sigHeader string) bool {
	sigHeader = strings.TrimPrefix(sigHeader, "ed25519=")
	sig, err := base64.StdEncoding.DecodeString(sigHeader)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, body, sig)
}

// PublicKeyFromJWKS extracts the Ed25519 (OKP) key for kid from a JWKS document.
func PublicKeyFromJWKS(jwksJSON []byte, kid string) (ed25519.PublicKey, error) {
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Crv string `json:"crv"`
			Kid string `json:"kid"`
			X   string `json:"x"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(jwksJSON, &doc); err != nil {
		return nil, err
	}
	for _, k := range doc.Keys {
		if k.Kid != kid {
			continue
		}
		if k.Kty != "OKP" || k.Crv != "Ed25519" {
			return nil, errors.New("unexpected key type for kid")
		}
		raw, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		if len(raw) != ed25519.PublicKeySize {
			return nil, errors.New("invalid ed25519 public key length")
		}
		return ed25519.PublicKey(raw), nil
	}
	return nil, errors.New("kid not found in jwks")
}
