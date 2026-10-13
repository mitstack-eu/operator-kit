// Package handler_test exercises the lifecycle webhook Handler end to end with real signed requests.
package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/webhook"
)

func noopSleep(context.Context, time.Duration) {}

func baseConfig(t *testing.T, r handler.Resource, s *kittest.Signer) handler.Config {
	t.Helper()
	return handler.Config{
		Resource:     r,
		JWKSURL:      s.JWKSServer(t),
		VerbBudget:   3,
		PollInterval: time.Millisecond,
		Sleep:        noopSleep,
	}
}

// newHandler takes the handler.Resource interface so a test needing a fake kittest does not provide still goes through this one constructor.
func newHandler(t *testing.T, r handler.Resource, s *kittest.Signer, mutate ...func(*handler.Config)) *handler.Handler {
	t.Helper()
	cfg := baseConfig(t, r, s)
	for _, m := range mutate {
		m(&cfg)
	}
	h, err := handler.New(cfg)
	if err != nil {
		t.Fatalf("handler.New: unexpected error: %v", err)
	}
	return h
}

// envelope attaches a valid OBO token unless extra sets one, so a test that wants no obo field builds its body with kittest.Envelope directly.
func envelope(t *testing.T, s *kittest.Signer, nonce string, extra map[string]any) []byte {
	t.Helper()
	fields := map[string]any{
		"operation":      "subscribe",
		"subscriptionId": "sub-1",
		"blockId":        "block-1",
		"orgId":          "org-1",
	}
	for k, v := range extra {
		fields[k] = v
	}
	if _, hasOBO := fields["obo"]; !hasOBO {
		subID, _ := fields["subscriptionId"].(string)
		orgID, _ := fields["orgId"].(string)
		appID, _ := fields["appId"].(string)
		tok, err := webhook.MintOBO(s.Private, s.Kid, webhook.OBOClaims{
			OrgID: orgID, AppID: appID, SubscriptionID: subID,
		}, time.Hour, time.Now())
		if err != nil {
			t.Fatalf("minting a default OBO token for envelope(): %v", err)
		}
		fields["obo"] = tok
	}
	return kittest.Envelope(nonce, time.Now(), fields)
}

func postHook(t *testing.T, h http.Handler, s *kittest.Signer, body []byte) (int, []byte) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, s.HookRequest(t, "/hook", body))
	return w.Code, w.Body.Bytes()
}

// postHookWithContext lets a test cancel the context a verb runs under.
func postHookWithContext(t *testing.T, h http.Handler, s *kittest.Signer, body []byte, ctx context.Context) (int, []byte) {
	t.Helper()
	w := httptest.NewRecorder()
	r := s.HookRequest(t, "/hook", body).WithContext(ctx)
	h.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

func decodeResponse(t *testing.T, raw []byte) contract.Response {
	t.Helper()
	resp, err := contract.DecodeResponse(raw)
	if err != nil {
		t.Fatalf("response does not decode as a contract.Response: %v\nbody: %s", err, raw)
	}
	return resp
}

func assertConforms(t *testing.T, raw []byte) {
	t.Helper()
	if problems := kittest.CheckResponse(raw); len(problems) != 0 {
		t.Errorf("response does not conform to the lifecycle webhook response contract: %v\nbody: %s", problems, raw)
	}
}

func zeroCalls(t *testing.T, f *kittest.FakeResource) {
	t.Helper()
	if f.EnsureCalls != 0 || f.ObserveCalls != 0 || f.DeleteCalls != 0 {
		t.Errorf("expected zero Resource calls, got Ensure=%d Observe=%d Delete=%d",
			f.EnsureCalls, f.ObserveCalls, f.DeleteCalls)
	}
}
