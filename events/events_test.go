package events_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/events"
)

func TestEmit_NoOpsWithoutACallback(t *testing.T) {
	// An older engine sends no callback, and that must be silence rather than an error.
	ctx := events.WithEnvelope(context.Background(), events.Envelope{SubscriptionID: "sub-1"})

	if err := events.EmitProgress(ctx, "half way"); err != nil {
		t.Errorf("EmitProgress with no callback returned %v, want nil", err)
	}
	if err := events.EmitCompleted(ctx); err != nil {
		t.Errorf("EmitCompleted with no callback returned %v, want nil", err)
	}
	if err := events.EmitFailed(ctx, "boom"); err != nil {
		t.Errorf("EmitFailed with no callback returned %v, want nil", err)
	}
}

func TestEmit_NoOpsWithoutAnEnvelope(t *testing.T) {
	if err := events.EmitProgress(context.Background(), "detail"); err != nil {
		t.Errorf("emit on a bare context returned %v, want nil", err)
	}
}

func TestEmitProgress_PostsTheEventToTheCallback(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()

	ctx := events.WithEnvelope(context.Background(), events.Envelope{
		SubscriptionID: "sub-1",
		Operation:      "subscribe",
		IdempotencyKey: "idem-1",
		Callback:       &events.Callback{URL: srv.URL, Token: "tok-abc"},
	})

	if err := events.EmitProgress(ctx, "creating instance"); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got["subscriptionId"] != "sub-1" || got["operation"] != "subscribe" ||
		got["status"] != "progress" || got["detail"] != "creating instance" ||
		got["idempotencyKey"] != "idem-1" || got["token"] != "tok-abc" {
		t.Errorf("posted event = %v", got)
	}
}

func TestEmitCompletedAndFailed_CarryTheRightStatus(t *testing.T) {
	var statuses []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev map[string]string
		_ = json.NewDecoder(r.Body).Decode(&ev)
		statuses = append(statuses, ev["status"])
	}))
	defer srv.Close()

	ctx := events.WithEnvelope(context.Background(), events.Envelope{
		SubscriptionID: "sub-1",
		Callback:       &events.Callback{URL: srv.URL},
	})
	_ = events.EmitCompleted(ctx)
	_ = events.EmitFailed(ctx, "it broke")

	if len(statuses) != 2 || statuses[0] != "completed" || statuses[1] != "failed" {
		t.Errorf("statuses = %v, want [completed failed]", statuses)
	}
}

func TestEmit_ScrubsSecretsBeforeSendingOffProcess(t *testing.T) {
	var detail string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev map[string]string
		_ = json.NewDecoder(r.Body).Decode(&ev)
		detail = ev["detail"]
	}))
	defer srv.Close()

	ctx := events.WithEnvelope(context.Background(), events.Envelope{
		SubscriptionID: "sub-1",
		Callback:       &events.Callback{URL: srv.URL},
	})
	_ = events.EmitFailed(ctx, "auth failed: token=ghp_supersecretvalue while calling API")

	if strings.Contains(detail, "ghp_supersecretvalue") {
		t.Errorf("event detail leaked a secret off-process: %q", detail)
	}
	if !strings.Contains(detail, "[REDACTED]") {
		t.Errorf("detail = %q, want the secret replaced by [REDACTED]", detail)
	}
}

func TestScrubDetail(t *testing.T) {
	cases := []struct{ in, wantAbsent string }{
		{"password=hunter2", "hunter2"},
		{"api_key: AKIAEXAMPLE", "AKIAEXAMPLE"},
		{"Secret = s3cr3t", "s3cr3t"},
		{"private_key=-----BEGIN", "-----BEGIN"},
		{"credential:abc123", "abc123"},
	}
	for _, c := range cases {
		if got := events.ScrubDetail(c.in); strings.Contains(got, c.wantAbsent) {
			t.Errorf("ScrubDetail(%q) = %q, still contains %q", c.in, got, c.wantAbsent)
		}
	}
	if got := events.ScrubDetail("instance i-abc123 is running"); got != "instance i-abc123 is running" {
		t.Errorf("ScrubDetail must leave non-secret detail intact, got %q", got)
	}
}
