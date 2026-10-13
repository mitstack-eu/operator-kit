package main

import (
	"net/http"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/webhook"
)

const apiToken = "sample-api-token-5e4d3c2b1a"

func TestNoSecretLeak_LogsEndpoint(t *testing.T) {
	kittest.AssertNoSecretLeak(t, kittest.SecretLeak{
		Secrets: []string{apiToken},
		Serve: func(logs *webhook.LogBuffer) (http.Handler, *kittest.Signer) {
			signer := kittest.NewSigner(t, "sample-kid", 3)
			h, err := newHandler(signer.JWKSServer(t), logs)
			if err != nil {
				t.Fatalf("newHandler: %v", err)
			}
			return h, signer
		},
		Requests: []kittest.LeakRequest{{
			Name: "subscribe whose Ensure quotes the API token",
			Body: map[string]any{
				"operation":      "subscribe",
				"subscriptionId": "sub-1",
				"blockId":        "secretleak-sample",
				"orgId":          "org-1",
				"secrets":        map[string]any{"api": map[string]any{"token": apiToken}},
			},
			Fails: true,
		}},
	})
}
