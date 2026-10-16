// Package attest, part of mitstack's own tooling, is the signed conformance attestation that a block passed block-verify, checkable offline against a JWKS.
package attest

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Claims are what an attestation asserts, all of them part of the signed payload.
type Claims struct {
	BlockID        string    `json:"blockId"`
	Version        string    `json:"version"`
	ManifestDigest string    `json:"manifestDigest"`
	ImageDigest    string    `json:"imageDigest,omitempty"`
	SuiteVersion   string    `json:"suiteVersion"`
	KitVersion     string    `json:"kitVersion"`
	ResultsDigest  string    `json:"resultsDigest"`
	IssuedAt       time.Time `json:"issuedAt"`
	Kid            string    `json:"kid"`
}

// Attestation is the claims plus a detached signature over their canonical encoding.
type Attestation struct {
	Claims    Claims `json:"claims"`
	Signature string `json:"signature"`
}

var (
	// ErrPartialPass is returned by Sign when asked to attest anything short of a full pass.
	ErrPartialPass  = errors.New("attest: refusing to sign — a partial pass is not a pass, and a caveated signature is one nobody reads")
	ErrUnknownKid   = errors.New("attest: unknown signing key id")
	ErrBadSignature = errors.New("attest: signature does not verify")
	// ErrManifestChanged is returned when the manifest digest no longer matches.
	ErrManifestChanged = errors.New("attest: manifest digest does not match — this attestation describes a different block")
	ErrIncomplete      = errors.New("attest: incomplete claims")
)

func canonical(c Claims) ([]byte, error) {
	return json.Marshal(c)
}

// Sign produces an attestation for a full pass and refuses any other verdict.
func Sign(priv ed25519.PrivateKey, kid string, c Claims, verdict string) (Attestation, error) {
	if verdict != "pass" {
		return Attestation{}, fmt.Errorf("%w (verdict was %q)", ErrPartialPass, verdict)
	}
	if c.BlockID == "" || c.Version == "" || c.ManifestDigest == "" || c.SuiteVersion == "" || c.ResultsDigest == "" {
		return Attestation{}, fmt.Errorf("%w: blockId, version, manifestDigest, suiteVersion and resultsDigest are all required", ErrIncomplete)
	}

	c.Kid = kid
	if c.IssuedAt.IsZero() {
		c.IssuedAt = time.Now().UTC()
	}
	payload, err := canonical(c)
	if err != nil {
		return Attestation{}, err
	}
	return Attestation{
		Claims:    c,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload)),
	}, nil
}

// KeyResolver returns the public key for a kid, typically from a fetched JWKS.
type KeyResolver func(kid string) (ed25519.PublicKey, bool)

// Verify checks an attestation against the manifest in hand and fails closed on every uncertainty.
func Verify(a Attestation, resolve KeyResolver, manifestDigest string) error {
	if a.Claims.Kid == "" {
		return fmt.Errorf("%w: attestation carries no kid", ErrUnknownKid)
	}
	pub, ok := resolve(a.Claims.Kid)
	if !ok {
		return fmt.Errorf("%w: %q is not in the JWKS", ErrUnknownKid, a.Claims.Kid)
	}

	sig, err := base64.StdEncoding.DecodeString(a.Signature)
	if err != nil {
		return fmt.Errorf("%w: signature is not base64", ErrBadSignature)
	}
	payload, err := canonical(a.Claims)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, payload, sig) {
		return ErrBadSignature
	}

	// Checked after the signature: comparing first would leak whether a forged signature was well-formed.
	if manifestDigest != "" && a.Claims.ManifestDigest != manifestDigest {
		return fmt.Errorf("%w (attested %q, in hand %q)", ErrManifestChanged, a.Claims.ManifestDigest, manifestDigest)
	}
	return nil
}

// Status is what a catalog shows for a block.
type Status string

const (
	// Unverified is the zero value: no attestation, which must never be mistaken for proof.
	Unverified Status = "unverified"
	// Verified is a valid attestation matching the manifest in hand.
	Verified Status = "verified"
	// Invalid is an attestation that exists but does not check out, kept distinct from Unverified because it is a louder signal.
	Invalid Status = "invalid"
)

// Evaluate resolves the catalog status for a block.
func Evaluate(a *Attestation, resolve KeyResolver, manifestDigest string) (Status, error) {
	if a == nil {
		return Unverified, nil
	}
	if err := Verify(*a, resolve, manifestDigest); err != nil {
		return Invalid, err
	}
	return Verified, nil
}
