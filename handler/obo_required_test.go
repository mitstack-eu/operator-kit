package handler_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/webhook"
)

// oboVerbCases lists the four verbs envelope.schema.json's operation enum
// carries, each paired with the FakeResource.Count a validate/unsubscribe
// needs to find something to observe.
var oboVerbCases = []struct {
	name      string
	operation string
	seed      int
}{
	{"subscribe", "subscribe", 0},
	{"upgrade", "upgrade", 0},
	{"validate", "validate", 1},
	{"unsubscribe", "unsubscribe", 1},
}

// TestServe_401WithNoOBOToken pins that a correctly signed request carrying
// no obo field at all — the token is required, not merely verified when
// present — is refused with 401 on every verb, before the Resource is ever
// consulted. The body is built with kittest.Envelope directly, not this
// package's envelope() helper, because envelope() now mints a default valid
// token (see helpers_test.go); a test of the missing-token boundary must
// build a body that genuinely lacks the field.
func TestServe_401WithNoOBOToken(t *testing.T) {
	for _, c := range oboVerbCases {
		t.Run(c.name, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			f := &kittest.FakeResource{Count: c.seed}
			h := newHandler(t, f, s)

			fields := map[string]any{
				"operation":      c.operation,
				"subscriptionId": "sub-1",
				"blockId":        "block-1",
				"orgId":          "org-1",
			}
			body := kittest.Envelope("nonce-noobo-"+c.name, time.Now(), fields)

			status, raw := postHook(t, h, s, body)
			if status != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 for a request with no obo field, body: %s", status, raw)
			}

			resp := decodeResponse(t, raw)
			if resp.Error != "missing OBO token" {
				t.Errorf("error = %q, want the fixed text %q so the error carries nothing from the request body", resp.Error, "missing OBO token")
			}

			zeroCalls(t, f)
		})
	}
}

// TestServe_200WithAValidOBOToken pins that a correctly signed request
// carrying a token minted from the same signer that signed the request
// still gets the answer the handler gives today, on every verb — the
// regression guard for the fix TestServe_401WithNoOBOToken drives red.
func TestServe_200WithAValidOBOToken(t *testing.T) {
	for _, c := range oboVerbCases {
		t.Run(c.name, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			f := &kittest.FakeResource{Count: c.seed}
			h := newHandler(t, f, s)

			tok, err := webhook.MintOBO(s.Private, s.Kid, webhook.OBOClaims{
				OrgID: "org-1", SubscriptionID: "sub-1",
			}, time.Hour, time.Now())
			if err != nil {
				t.Fatalf("minting a valid OBO token: %v", err)
			}

			fields := map[string]any{
				"operation":      c.operation,
				"subscriptionId": "sub-1",
				"blockId":        "block-1",
				"orgId":          "org-1",
				"obo":            tok,
			}
			body := kittest.Envelope("nonce-validobo-"+c.name, time.Now(), fields)

			status, raw := postHook(t, h, s, body)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200 for a request carrying a valid OBO token, body: %s", status, raw)
			}
			assertConforms(t, raw)
		})
	}
}

// TestConfig_CarriesNoOBOEscapeHatch pins the boundary that nothing in
// handler.Config can turn the OBO requirement off: no field name on Config
// mentions OBO, so there is no flag a caller could set to skip the check.
func TestConfig_CarriesNoOBOEscapeHatch(t *testing.T) {
	typ := reflect.TypeOf(handler.Config{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if strings.Contains(strings.ToLower(name), "obo") {
			t.Errorf("handler.Config carries field %q, want no OBO-related escape hatch on Config", name)
		}
	}
}
