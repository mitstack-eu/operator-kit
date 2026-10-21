package webhook_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/kittest"
	"github.com/mitstack-eu/operator-kit/v3/webhook"
)

func TestVerifyOBO_RoundTrip(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 3)
	now := time.Now()
	claims := webhook.OBOClaims{OrgID: "org-1", AppID: "app-1", SubscriptionID: "sub-1"}

	tok, err := webhook.MintOBO(s.Private, s.Kid, claims, time.Hour, now)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	got, err := webhook.VerifyOBO(s.Public, tok, now)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got != claims {
		t.Errorf("claims = %+v, want %+v", got, claims)
	}
}

func TestVerifyOBO_ExpiredIsItsOwnError(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 3)
	now := time.Now()

	tok, _ := webhook.MintOBO(s.Private, s.Kid, webhook.OBOClaims{OrgID: "org-1"}, time.Minute, now)

	if _, err := webhook.VerifyOBO(s.Public, tok, now.Add(2*time.Minute)); !errors.Is(err, webhook.ErrOBOExpired) {
		t.Errorf("err = %v, want ErrOBOExpired — an expired token must be distinguishable from a forged one", err)
	}
}

func TestVerifyOBO_RejectsTokenSignedByAnotherKey(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 3)
	other := kittest.NewSigner(t, "kid-2", 44)

	tok, _ := webhook.MintOBO(other.Private, other.Kid, webhook.OBOClaims{OrgID: "org-1"}, time.Hour, time.Now())

	if _, err := webhook.VerifyOBO(s.Public, tok, time.Now()); !errors.Is(err, webhook.ErrOBOInvalid) {
		t.Errorf("err = %v, want ErrOBOInvalid", err)
	}
}

func TestVerifyOBO_RejectsMalformedTokens(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 3)
	for _, tok := range []string{"", "onepart", "a.b.c", "!!!.???", "eyJ.notbase64"} {
		if _, err := webhook.VerifyOBO(s.Public, tok, time.Now()); !errors.Is(err, webhook.ErrOBOInvalid) {
			t.Errorf("token %q: err = %v, want ErrOBOInvalid", tok, err)
		}
	}
}

func TestVerifyOBO_AudienceBindingRejectsAnotherOperatorsToken(t *testing.T) {
	// A token minted for block-a must not be presentable to block-b.
	s := kittest.NewSigner(t, "kid-1", 3)
	now := time.Now()
	t.Setenv("MITSTACK_OBO_AUDIENCE", "block-b")

	tok, _ := webhook.MintOBO(s.Private, s.Kid, webhook.OBOClaims{OrgID: "org-1", Audience: "block-a"}, time.Hour, now)

	if _, err := webhook.VerifyOBO(s.Public, tok, now); !errors.Is(err, webhook.ErrOBOInvalid) {
		t.Errorf("err = %v, want ErrOBOInvalid for a token minted for another operator", err)
	}
}

func TestVerifyOBO_AudienceBindingAcceptsOwnToken(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 3)
	now := time.Now()
	t.Setenv("MITSTACK_OBO_AUDIENCE", "block-b")

	tok, _ := webhook.MintOBO(s.Private, s.Kid, webhook.OBOClaims{OrgID: "org-1", Audience: "block-b"}, time.Hour, now)

	if _, err := webhook.VerifyOBO(s.Public, tok, now); err != nil {
		t.Errorf("unexpected error for a matching audience: %v", err)
	}
}

func TestVerifyOBO_UnsetAudienceStaysBackwardCompatible(t *testing.T) {
	// Empty env = not enforced, so an older engine minting no aud still works.
	s := kittest.NewSigner(t, "kid-1", 3)
	now := time.Now()
	t.Setenv("MITSTACK_OBO_AUDIENCE", "")

	tok, _ := webhook.MintOBO(s.Private, s.Kid, webhook.OBOClaims{OrgID: "org-1"}, time.Hour, now)

	if _, err := webhook.VerifyOBO(s.Public, tok, now); err != nil {
		t.Errorf("unexpected error with audience enforcement off: %v", err)
	}
}
