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

func TestSign_ThenVerify(t *testing.T) {
	s := kittest.NewSigner(t, "kid-platform", 9)

	a, err := attest.Sign(s.Private, s.Kid, claims(), "pass")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := attest.Verify(a, resolverFor(s), "sha256:manifest"); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestSign_RefusesAnythingButAFullPass(t *testing.T) {
	s := kittest.NewSigner(t, "kid-platform", 9)

	for _, verdict := range []string{"fail", "partial", "", "PASS", "pass-with-warnings"} {
		if _, err := attest.Sign(s.Private, s.Kid, claims(), verdict); !errors.Is(err, attest.ErrPartialPass) {
			t.Errorf("verdict %q: err = %v, want ErrPartialPass — there is no signed-with-exceptions", verdict, err)
		}
	}
}

func TestSign_RefusesIncompleteClaims(t *testing.T) {
	s := kittest.NewSigner(t, "kid-platform", 9)

	for name, mutate := range map[string]func(*attest.Claims){
		"no blockId":        func(c *attest.Claims) { c.BlockID = "" },
		"no version":        func(c *attest.Claims) { c.Version = "" },
		"no manifestDigest": func(c *attest.Claims) { c.ManifestDigest = "" },
		"no suiteVersion":   func(c *attest.Claims) { c.SuiteVersion = "" },
		"no resultsDigest":  func(c *attest.Claims) { c.ResultsDigest = "" },
	} {
		t.Run(name, func(t *testing.T) {
			c := claims()
			mutate(&c)
			if _, err := attest.Sign(s.Private, s.Kid, c, "pass"); !errors.Is(err, attest.ErrIncomplete) {
				t.Errorf("err = %v, want ErrIncomplete — an attestation that omits what it attests to is decorative", err)
			}
		})
	}
}

func TestVerify_UnknownKidFailsClosed(t *testing.T) {
	s := kittest.NewSigner(t, "kid-platform", 9)
	other := kittest.NewSigner(t, "kid-other", 44)

	a, _ := attest.Sign(s.Private, s.Kid, claims(), "pass")
	if err := attest.Verify(a, resolverFor(other), "sha256:manifest"); !errors.Is(err, attest.ErrUnknownKid) {
		t.Errorf("err = %v, want ErrUnknownKid — a key we cannot resolve must never read as fine", err)
	}
}

func TestVerify_TamperedClaimsFail(t *testing.T) {
	s := kittest.NewSigner(t, "kid-platform", 9)
	a, _ := attest.Sign(s.Private, s.Kid, claims(), "pass")

	// Every claim is inside the signed payload, so any edit must break it.
	for name, mutate := range map[string]func(*attest.Attestation){
		"version":       func(x *attest.Attestation) { x.Claims.Version = "9.9.9" },
		"suiteVersion":  func(x *attest.Attestation) { x.Claims.SuiteVersion = "0.0.1" },
		"kitVersion":    func(x *attest.Attestation) { x.Claims.KitVersion = "v0.0.1" },
		"resultsDigest": func(x *attest.Attestation) { x.Claims.ResultsDigest = "sha256:other" },
		"imageDigest":   func(x *attest.Attestation) { x.Claims.ImageDigest = "sha256:swapped" },
		"issuedAt":      func(x *attest.Attestation) { x.Claims.IssuedAt = time.Now() },
	} {
		t.Run(name, func(t *testing.T) {
			tampered := a
			mutate(&tampered)
			if err := attest.Verify(tampered, resolverFor(s), "sha256:manifest"); !errors.Is(err, attest.ErrBadSignature) {
				t.Errorf("editing %s did not break the signature: %v", name, err)
			}
		})
	}
}

func TestVerify_ForgedSignatureFails(t *testing.T) {
	s := kittest.NewSigner(t, "kid-platform", 9)
	other := kittest.NewSigner(t, "kid-platform", 77) // same kid, different key

	a, _ := attest.Sign(other.Private, "kid-platform", claims(), "pass")
	if err := attest.Verify(a, resolverFor(s), "sha256:manifest"); !errors.Is(err, attest.ErrBadSignature) {
		t.Errorf("err = %v, want ErrBadSignature for a token signed by another key under a known kid", err)
	}
}

func TestVerify_ManifestChangeMakesItInvalid(t *testing.T) {
	s := kittest.NewSigner(t, "kid-platform", 9)
	a, _ := attest.Sign(s.Private, s.Kid, claims(), "pass")

	if err := attest.Verify(a, resolverFor(s), "sha256:DIFFERENT"); !errors.Is(err, attest.ErrManifestChanged) {
		t.Errorf("err = %v, want ErrManifestChanged", err)
	}
}

func TestVerify_SignatureIsCheckedBeforeTheDigest(t *testing.T) {
	// A forged attestation must report a signature failure, not a digest mismatch.
	s := kittest.NewSigner(t, "kid-platform", 9)
	other := kittest.NewSigner(t, "kid-platform", 55)

	a, _ := attest.Sign(other.Private, "kid-platform", claims(), "pass")
	err := attest.Verify(a, resolverFor(s), "sha256:ALSO-DIFFERENT")

	if !errors.Is(err, attest.ErrBadSignature) {
		t.Errorf("err = %v, want ErrBadSignature — the signature must be judged before anything the presenter controls", err)
	}
}

func TestEvaluate_NoAttestationIsUnverifiedNotInvalid(t *testing.T) {
	s := kittest.NewSigner(t, "kid-platform", 9)

	got, err := attest.Evaluate(nil, resolverFor(s), "sha256:manifest")
	if err != nil || got != attest.Unverified {
		t.Errorf("status = %v (err %v), want unverified — nobody verified it is not the same as it failed", got, err)
	}
}

func TestEvaluate_BrokenAttestationIsInvalidNotUnverified(t *testing.T) {
	s := kittest.NewSigner(t, "kid-platform", 9)
	a, _ := attest.Sign(s.Private, s.Kid, claims(), "pass")

	got, err := attest.Evaluate(&a, resolverFor(s), "sha256:CHANGED")
	if got != attest.Invalid {
		t.Errorf("status = %v, want invalid", got)
	}
	if err == nil {
		t.Error("Evaluate must return WHY it is invalid, not just that it is")
	}
}

func TestEvaluate_ValidAttestationIsVerified(t *testing.T) {
	s := kittest.NewSigner(t, "kid-platform", 9)
	a, _ := attest.Sign(s.Private, s.Kid, claims(), "pass")

	got, err := attest.Evaluate(&a, resolverFor(s), "sha256:manifest")
	if err != nil || got != attest.Verified {
		t.Errorf("status = %v (err %v), want verified", got, err)
	}
}

func TestStatus_ZeroValueIsNotVerified(t *testing.T) {
	var s attest.Status
	if s == attest.Verified {
		t.Error("the zero Status must never be Verified")
	}
}
