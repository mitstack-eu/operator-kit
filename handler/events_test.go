// A verb emits at most one terminal event and no start event, validate emits none, and a broken events channel never changes the answer.
package handler_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/handler"
	"github.com/mitstack-eu/operator-kit/v3/kittest"
)

// recordedEvent is decoded independently of the events package so the test proves the wire shape rather than importing its type.
type recordedEvent struct {
	SubscriptionID string `json:"subscriptionId"`
	Token          string `json:"token"`
	Status         string `json:"status"`
	Detail         string `json:"detail"`
}

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
	}))
	t.Cleanup(f.Server.Close)
	return f
}

func (f *fakeEventsServer) recorded() []recordedEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.events)
}

func TestServe_EmitsOneTerminalEventPerVerb(t *testing.T) {
	withOutputs := func() *kittest.FakeResource {
		return &kittest.FakeResource{Outputs: map[string]string{"url": "https://example.test"}, StatusDetail: "provisioned"}
	}
	completed, progress, failed := []string{"completed"}, []string{"progress"}, []string{"failed"}
	cases := []struct {
		name   string
		op     string
		f      *kittest.FakeResource
		budget int
		status int
		want   []string
		detail string
	}{
		{"a validate observed present emits nothing", "validate", &kittest.FakeResource{ObserveState: stateOf(contract.Present)}, 3, http.StatusOK, nil, ""},
		{"a validate observed unknown emits nothing", "validate", &kittest.FakeResource{ObserveState: stateOf(contract.Unknown)}, 3, http.StatusOK, nil, ""},
		{"a validate whose Observe fails emits nothing", "validate", &kittest.FakeResource{ObserveErr: errors.New("observe backend unavailable")}, 3,
			http.StatusInternalServerError, nil, ""},
		{"a settled subscribe emits completed alone", "subscribe", withOutputs(), 3, http.StatusOK, completed, ""},
		{"a settled upgrade emits completed alone", "upgrade", withOutputs(), 3, http.StatusOK, completed, ""},
		{"a settled unsubscribe emits completed alone", "unsubscribe", &kittest.FakeResource{Count: 1}, 3, http.StatusOK, completed, ""},
		{"a pending subscribe emits progress, not completed", "subscribe", &kittest.FakeResource{ObserveState: stateOf(contract.Absent)}, 2, http.StatusOK, progress, ""},
		{"a pending upgrade emits progress, not completed", "upgrade", &kittest.FakeResource{ObserveState: stateOf(contract.Absent)}, 2, http.StatusOK, progress, ""},
		{"a failing subscribe emits failed with the error", "subscribe", &kittest.FakeResource{EnsureErr: errors.New("resource backend unavailable")}, 3,
			http.StatusInternalServerError, failed, "resource backend unavailable"},
		{"a refused subscribe emits failed with the class", "subscribe", &kittest.FakeResource{EnsureErr: handler.Refuse("quota-exceeded", "org quota exhausted")}, 3,
			http.StatusBadRequest, failed, "quota-exceeded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := newFakeEventsServer(t)
			x := newHarness(t, tc.f, withBudget(tc.budget))

			status, resp := x.answer(map[string]any{"operation": tc.op, "eventsCallback": map[string]any{"url": events.URL, "token": "events-tok"}})

			if status != tc.status {
				t.Fatalf("status = %d, want %d; response %+v", status, tc.status, resp)
			}
			if slices.Equal(tc.want, progress) && !resp.Pending {
				t.Fatalf("pending = false, want the pending outcome this row exercises; response %+v", resp)
			}
			got := events.recorded()
			var statuses []string
			for _, ev := range got {
				statuses = append(statuses, ev.Status)
				if ev.SubscriptionID != "sub-1" || ev.Token != "events-tok" || !strings.Contains(ev.Detail, tc.detail) {
					t.Errorf("event = %+v, want subscription sub-1, the eventsCallback's own token and a detail naming %q", ev, tc.detail)
				}
			}
			if !slices.Equal(statuses, tc.want) {
				t.Errorf("recorded event statuses %v, want exactly %v: %+v", statuses, tc.want, got)
			}
		})
	}
}

func TestServe_EventsFailureNeverChangesTheLifecycleResponse(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(failing.Close)
	for name, url := range map[string]string{"an events endpoint answering 500": failing.URL, "an unreachable events endpoint": "http://127.0.0.1:1/events"} {
		t.Run(name, func(t *testing.T) {
			x := newHarness(t, &kittest.FakeResource{})

			status, resp := x.answer(map[string]any{"eventsCallback": map[string]any{"url": url, "token": "events-tok"}})

			if status != http.StatusOK || resp.Error != "" {
				t.Errorf("status, error = %d, %q; want 200 and none, since the events channel is advisory only", status, resp.Error)
			}
		})
	}
}
