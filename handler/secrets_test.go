package handler_test

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/webhook"
)

// The handler logs via the standard "log" package, so this test redirects log.SetOutput as an operator's main() wires LogBuffer, since Config has no logger field.
func TestServe_NeverLeaksSecretsInResponseOrLogs(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)

	const secretValue = "top-secret-db-password-value"
	const secretInputValue = "top-secret-input-value-here"
	const appSecretValue = "top-secret-app-secret-value"
	const eventsToken = "top-secret-events-callback-token"

	oboToken, err := webhook.MintOBO(s.Private, s.Kid, webhook.OBOClaims{
		OrgID: "org-1", AppID: "app-1", SubscriptionID: "sub-1",
	}, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("minting a test OBO token: %v", err)
	}

	// The block's own error quotes the secret, so scrubbing must cover block-authored text.
	f := &kittest.FakeResource{EnsureErr: fmt.Errorf("upstream rejected credential %s", secretValue)}

	var logBuf bytes.Buffer
	prevOut := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prevOut) })

	h := newHandler(t, f, s)

	body := envelope(t, s, "nonce-secrets", map[string]any{
		"obo":          oboToken,
		"secrets":      map[string]any{"db": map[string]any{"password": secretValue}},
		"secretInputs": map[string]any{"apiKey": secretInputValue},
		"appSecrets":   map[string]any{"masterKey": appSecretValue},
		"eventsCallback": map[string]any{
			"url":   "http://127.0.0.1:1/events", // deliberately unreachable; advisory only
			"token": eventsToken,
		},
	})

	status, raw := postHook(t, h, s, body)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (the Ensure error), body: %s", status, raw)
	}

	kittest.AssertNoSecret(t, "response body", string(raw),
		secretValue, secretInputValue, appSecretValue, oboToken, eventsToken)
	kittest.AssertNoSecret(t, "log output", logBuf.String(),
		secretValue, secretInputValue, appSecretValue, oboToken, eventsToken)
}
