package handler_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// recordedEvent is decoded independently of the events package so the test proves the wire shape rather than importing its type.
type recordedEvent struct {
	SubscriptionID string `json:"subscriptionId"`
	Token          string `json:"token"`
	Operation      string `json:"operation"`
	Status         string `json:"status"`
	Detail         string `json:"detail"`
	IdempotencyKey string `json:"idempotencyKey"`
}

// fakeEventsServer records every event POSTed to it.
type fakeEventsServer struct {
	*httptest.Server
	mu     sync.Mutex
	events []recordedEvent
}

func newFakeEventsServer(t *testing.T) *fakeEventsServer {
	t.Helper()
	f := &fakeEventsServer{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev recordedEvent
		_ = json.NewDecoder(r.Body).Decode(&ev)
		f.mu.Lock()
		f.events = append(f.events, ev)
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(f.Server.Close)
	return f
}

func (f *fakeEventsServer) Events() []recordedEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedEvent, len(f.events))
	copy(out, f.events)
	return out
}

func eventsCallbackField(url, token string) map[string]any {
	return map[string]any{"url": url, "token": token}
}

// At most one event per verb call: validate emits nothing; the others emit completed on a settled 2xx, progress on a pending:true 2xx, failed on 4xx/5xx, and no start event, which would take the unique-index slot of the terminal event and loop ProbeHealth on validate.

func TestServe_ValidateEmitsNoEvents(t *testing.T) {
	present := contract.Present
	unknown := contract.Unknown
	cases := []struct {
		name string
		f    *kittest.FakeResource
	}{
		{"present", &kittest.FakeResource{ObserveState: &present}},
		{"unknown", &kittest.FakeResource{ObserveState: &unknown}},
		{"observe error", &kittest.FakeResource{ObserveErr: errors.New("observe backend unavailable")}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			h := newHandler(t, c.f, s)
			events := newFakeEventsServer(t)

			body := envelope(t, s, "nonce-events-validate-"+c.name, map[string]any{
				"operation":      "validate",
				"eventsCallback": eventsCallbackField(events.URL, "events-tok"),
			})
			_, raw := postHook(t, h, s, body)

			got := events.Events()
			if len(got) != 0 {
				t.Errorf("recorded %d events for validate, want 0: %+v\nresponse: %s", len(got), got, raw)
			}
		})
	}
}

// A spurious start event beside the right terminal event is the defect the count guards.
func TestServe_SettledSubscribeEmitsExactlyOneCompletedEvent(t *testing.T) {
	for _, op := range []string{"subscribe", "upgrade"} {
		t.Run(op, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			f := &kittest.FakeResource{Outputs: map[string]string{"url": "https://example.test"}, StatusDetail: "provisioned"}
			h := newHandler(t, f, s)
			events := newFakeEventsServer(t)

			body := envelope(t, s, "nonce-events-settled-"+op, map[string]any{
				"operation":      op,
				"eventsCallback": eventsCallbackField(events.URL, "events-tok"),
			})
			status, raw := postHook(t, h, s, body)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200, body: %s", status, raw)
			}

			got := events.Events()
			if len(got) != 1 {
				t.Fatalf("recorded %d events, want exactly 1 (completed): %+v", len(got), got)
			}
			if got[0].Status != "completed" {
				t.Errorf("event status = %q, want %q", got[0].Status, "completed")
			}
			if got[0].SubscriptionID != "sub-1" {
				t.Errorf("event SubscriptionID = %q, want %q", got[0].SubscriptionID, "sub-1")
			}
			if got[0].Token != "events-tok" {
				t.Errorf("event Token = %q, want the eventsCallback's own token", got[0].Token)
			}
		})
	}
}

func TestServe_SettledUnsubscribeEmitsExactlyOneCompletedEvent(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{Count: 1} // Delete drives Count to 0 -> Observe reports Absent
	h := newHandler(t, f, s)
	events := newFakeEventsServer(t)

	body := envelope(t, s, "nonce-events-settled-unsub", map[string]any{
		"operation":      "unsubscribe",
		"eventsCallback": eventsCallbackField(events.URL, "events-tok"),
	})
	status, raw := postHook(t, h, s, body)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}

	got := events.Events()
	if len(got) != 1 {
		t.Fatalf("recorded %d events, want exactly 1 (completed): %+v", len(got), got)
	}
	if got[0].Status != "completed" {
		t.Errorf("event status = %q, want %q", got[0].Status, "completed")
	}
}

func TestServe_PendingSubscribeEmitsExactlyOneProgressEvent(t *testing.T) {
	for _, op := range []string{"subscribe", "upgrade"} {
		t.Run(op, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			absent := contract.Absent
			f := &kittest.FakeResource{ObserveState: &absent} // never reaches Present
			h := newHandler(t, f, s, func(c *handler.Config) { c.VerbBudget = 2 })
			events := newFakeEventsServer(t)

			body := envelope(t, s, "nonce-events-pending-"+op, map[string]any{
				"operation":      op,
				"eventsCallback": eventsCallbackField(events.URL, "events-tok"),
			})
			status, raw := postHook(t, h, s, body)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200 (pending is never an error), body: %s", status, raw)
			}
			resp := decodeResponse(t, raw)
			if !resp.Pending {
				t.Fatalf("response Pending = false, want true — test setup did not reach the pending outcome it means to exercise")
			}

			got := events.Events()
			if len(got) != 1 {
				t.Fatalf("recorded %d events, want exactly 1 (progress): %+v", len(got), got)
			}
			if got[0].Status != "progress" {
				t.Errorf("event status = %q, want %q — a pending outcome is progress, not completed", got[0].Status, "progress")
			}
		})
	}
}

func TestServe_FailingSubscribeEmitsExactlyOneFailedEventWithDetail(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantDetail string
	}{
		{"plain error", errors.New("resource backend unavailable"), http.StatusInternalServerError, "resource backend unavailable"},
		{"refused", handler.Refuse("quota-exceeded", "org quota exhausted"), http.StatusBadRequest, "quota-exceeded"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			f := &kittest.FakeResource{EnsureErr: c.err}
			h := newHandler(t, f, s)
			events := newFakeEventsServer(t)

			body := envelope(t, s, "nonce-events-failed-"+c.name, map[string]any{
				"eventsCallback": eventsCallbackField(events.URL, "events-tok"),
			})
			status, raw := postHook(t, h, s, body)
			if status != c.wantStatus {
				t.Fatalf("status = %d, want %d, body: %s", status, c.wantStatus, raw)
			}

			got := events.Events()
			if len(got) != 1 {
				t.Fatalf("recorded %d events, want exactly 1 (failed): %+v", len(got), got)
			}
			if got[0].Status != "failed" {
				t.Errorf("event status = %q, want %q", got[0].Status, "failed")
			}
			if !strings.Contains(got[0].Detail, c.wantDetail) {
				t.Errorf("failed event detail = %q, want it to contain %q", got[0].Detail, c.wantDetail)
			}
		})
	}
}

// ADR 0042: an events endpoint answering 500, or unreachable, must not change the lifecycle outcome.
func TestServe_EventsFailureNeverChangesTheLifecycleResponse(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"events endpoint returns 500", ""}, // filled below with a real 500 server
		{"events endpoint unreachable", "http://127.0.0.1:1/events"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			f := &kittest.FakeResource{}
			h := newHandler(t, f, s)

			url := c.url
			if url == "" {
				failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				}))
				t.Cleanup(failing.Close)
				url = failing.URL
			}

			body := envelope(t, s, "nonce-events-advisory-"+c.name, map[string]any{
				"eventsCallback": eventsCallbackField(url, "events-tok"),
			})
			status, raw := postHook(t, h, s, body)
			if status != http.StatusOK {
				t.Errorf("status = %d, want 200 — a broken events channel must never change the lifecycle response, body: %s", status, raw)
			}
			resp := decodeResponse(t, raw)
			if resp.Error != "" {
				t.Errorf("Error = %q, want empty — the events channel is advisory only", resp.Error)
			}
		})
	}
}
