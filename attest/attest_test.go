package attest_test

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/attest"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

func claims() attest.Claims {
	return attest.Claims{
		BlockID:        "aws-app-infra",
		Version:        "1.0.0",
		ManifestDigest: "sha256:manifest",
		ImageDigest:    "sha256:image",
		SuiteVersion:   "1.0.0",
		KitVersion:     "v0.1.0",
		ResultsDigest:  "sha256:results",
		IssuedAt:       time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
	}
}

func resolverFor(s *kittest.Signer) attest.KeyResolver {
	return func(kid string) (ed25519.PublicKey, bool) {
		if kid == s.Kid {
			return s.Public, true
		}
		return nil, false
	}
}

// signed returns an attestation of claims() signed by s, failing the test if Sign refuses it.
func signed(t *testing.T, s *kittest.Signer, mutate func(*attest.Attestation)) attest.Attestation {
	t.Helper()
	a, err := attest.Sign(s.Private, s.Kid, claims(), "pass")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if mutate != nil {
		mutate(&a)
	}
	return a
}

func TestSign_Refuses(t *testing.T) {
	s := kittest.NewSigner(t, "kid-platform", 9)
	cases := []struct {
		name    string
		verdict string
		mutate  func(*attest.Claims)
		want    error
	}{
		{"a fail verdict, since there is no signed-with-exceptions", "fail", nil, attest.ErrPartialPass},
		{"a partial verdict", "partial", nil, attest.ErrPartialPass},
		{"an empty verdict", "", nil, attest.ErrPartialPass},
		{"an upper-case PASS", "PASS", nil, attest.ErrPartialPass},
		{"a pass with warnings", "pass-with-warnings", nil, attest.ErrPartialPass},
		{"claims with no blockId", "pass", func(c *attest.Claims) { c.BlockID = "" }, attest.ErrIncomplete},
		{"claims with no version", "pass", func(c *attest.Claims) { c.Version = "" }, attest.ErrIncomplete},
		{"claims with no manifestDigest", "pass", func(c *attest.Claims) { c.ManifestDigest = "" }, attest.ErrIncomplete},
		{"claims with no suiteVersion", "pass", func(c *attest.Claims) { c.SuiteVersion = "" }, attest.ErrIncomplete},
		{"claims with no resultsDigest", "pass", func(c *attest.Claims) { c.ResultsDigest = "" }, attest.ErrIncomplete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := claims()
			if tc.mutate != nil {
				tc.mutate(&c)
			}

			_, err := attest.Sign(s.Private, s.Kid, c, tc.verdict)

			if !errors.Is(err, tc.want) {
				t.Errorf("Sign(verdict %q, %+v) error = %v, want %v", tc.verdict, c, err, tc.want)
			}
		})
	}
}

func TestVerify(t *testing.T) {
	s := kittest.NewSigner(t, "kid-platform", 9)
	sameKidOtherKey := kittest.NewSigner(t, "kid-platform", 77)
	forged, err := attest.Sign(sameKidOtherKey.Private, "kid-platform", claims(), "pass")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	tamper := func(f func(*attest.Attestation)) attest.Attestation { return signed(t, s, f) }
	cases := []struct {
		name    string
		a       attest.Attestation
		resolve attest.KeyResolver
		digest  string
		want    error
	}{
		{"a signed full pass verifies against its manifest", signed(t, s, nil), resolverFor(s), "sha256:manifest", nil},
		{"a key that cannot be resolved fails closed", signed(t, s, nil), resolverFor(kittest.NewSigner(t, "kid-other", 44)), "sha256:manifest", attest.ErrUnknownKid},
		{"an edited version breaks the signature", tamper(func(x *attest.Attestation) { x.Claims.Version = "9.9.9" }), resolverFor(s), "sha256:manifest", attest.ErrBadSignature},
		{"an edited suiteVersion breaks the signature", tamper(func(x *attest.Attestation) { x.Claims.SuiteVersion = "0.0.1" }), resolverFor(s), "sha256:manifest", attest.ErrBadSignature},
		{"an edited kitVersion breaks the signature", tamper(func(x *attest.Attestation) { x.Claims.KitVersion = "v0.0.1" }), resolverFor(s), "sha256:manifest", attest.ErrBadSignature},
		{"an edited resultsDigest breaks the signature", tamper(func(x *attest.Attestation) { x.Claims.ResultsDigest = "sha256:other" }), resolverFor(s), "sha256:manifest", attest.ErrBadSignature},
		{"an edited imageDigest breaks the signature", tamper(func(x *attest.Attestation) { x.Claims.ImageDigest = "sha256:swapped" }), resolverFor(s), "sha256:manifest", attest.ErrBadSignature},
		{"an edited issuedAt breaks the signature", tamper(func(x *attest.Attestation) { x.Claims.IssuedAt = time.Now() }), resolverFor(s), "sha256:manifest", attest.ErrBadSignature},
		{"a signature by another key under a known kid fails", forged, resolverFor(s), "sha256:manifest", attest.ErrBadSignature},
		{"a changed manifest makes it invalid", signed(t, s, nil), resolverFor(s), "sha256:DIFFERENT", attest.ErrManifestChanged},
		{"a forgery reports its signature before the digest the presenter controls", forged, resolverFor(s), "sha256:ALSO-DIFFERENT", attest.ErrBadSignature},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := attest.Verify(tc.a, tc.resolve, tc.digest)

			if (tc.want == nil && err != nil) || !errors.Is(err, tc.want) {
				t.Errorf("Verify(digest %q) = %v, want %v", tc.digest, err, tc.want)
			}
		})
	}
}

func TestEvaluate(t *testing.T) {
	s := kittest.NewSigner(t, "kid-platform", 9)
	a := signed(t, s, nil)
	cases := []struct {
		name    string
		a       *attest.Attestation
		digest  string
		want    attest.Status
		wantErr bool
	}{
		{"no attestation is unverified, not invalid", nil, "sha256:manifest", attest.Unverified, false},
		{"a broken attestation is invalid and says why", &a, "sha256:CHANGED", attest.Invalid, true},
		{"a valid attestation is verified", &a, "sha256:manifest", attest.Verified, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := attest.Evaluate(tc.a, resolverFor(s), tc.digest)

			if got != tc.want || (err != nil) != tc.wantErr {
				t.Errorf("Evaluate(digest %q) = %v, %v; want %v, error %v", tc.digest, got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestStatus_ZeroValueIsNotVerified(t *testing.T) {
	var s attest.Status

	if s == attest.Verified {
		t.Error("the zero Status must never be Verified")
	}
}
