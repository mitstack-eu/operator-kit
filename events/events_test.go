package events_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/events"
)

func TestEmit(t *testing.T) {
	progress := func(ctx context.Context) error { return events.EmitProgress(ctx, "creating instance") }
	completed := events.EmitCompleted
	failed := func(ctx context.Context) error { return events.EmitFailed(ctx, "it broke") }
	cases := []struct {
		name  string
		env   *events.Envelope
		emits []func(context.Context) error
		want  []map[string]string
	}{
		{"every emit is silent rather than an error when an older engine sends no callback", &events.Envelope{SubscriptionID: "sub-1"},
			[]func(context.Context) error{progress, completed, failed}, nil},
		{"an emit on a context with no envelope is silent", nil, []func(context.Context) error{progress}, nil},
		{"a progress event posts the whole envelope with its detail", &events.Envelope{SubscriptionID: "sub-1", Operation: "subscribe", IdempotencyKey: "idem-1", Callback: &events.Callback{Token: "tok-abc"}},
			[]func(context.Context) error{progress},
			[]map[string]string{{"subscriptionId": "sub-1", "operation": "subscribe", "status": "progress", "detail": "creating instance", "idempotencyKey": "idem-1", "token": "tok-abc"}}},
		{"completed and failed carry their status", &events.Envelope{SubscriptionID: "sub-1", Callback: &events.Callback{}},
			[]func(context.Context) error{completed, failed},
			[]map[string]string{
				{"subscriptionId": "sub-1", "operation": "", "status": "completed", "detail": "", "idempotencyKey": "", "token": ""},
				{"subscriptionId": "sub-1", "operation": "", "status": "failed", "detail": "it broke", "idempotencyKey": "", "token": ""},
			}},
		{"a secret in the detail is scrubbed before it leaves the process", &events.Envelope{SubscriptionID: "sub-1", Callback: &events.Callback{}},
			[]func(context.Context) error{func(ctx context.Context) error {
				return events.EmitFailed(ctx, "auth failed: token=ghp_supersecretvalue while calling API")
			}},
			[]map[string]string{{"subscriptionId": "sub-1", "operation": "", "status": "failed", "detail": "auth failed: token= [REDACTED] while calling API", "idempotencyKey": "", "token": ""}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []map[string]string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var ev map[string]string
				_ = json.NewDecoder(r.Body).Decode(&ev)
				got = append(got, ev)
			}))
			defer srv.Close()
			ctx := context.Background()
			if tc.env != nil {
				env := *tc.env
				if env.Callback != nil {
					env.Callback = &events.Callback{URL: srv.URL, Token: env.Callback.Token}
				}
				ctx = events.WithEnvelope(ctx, env)
			}

			for i, emit := range tc.emits {
				if err := emit(ctx); err != nil {
					t.Errorf("emit #%d returned %v, want nil", i+1, err)
				}
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("posted events = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScrubDetail(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"password=hunter2", "password= [REDACTED]"},
		{"api_key: AKIAEXAMPLE", "api_key: [REDACTED]"},
		{"Secret = s3cr3t", "Secret = [REDACTED]"},
		{"private_key=-----BEGIN", "private_key= [REDACTED]"},
		{"credential:abc123", "credential: [REDACTED]"},
		{"instance i-abc123 is running", "instance i-abc123 is running"},
	} {
		if got := events.ScrubDetail(tc.in); got != tc.want {
			t.Errorf("ScrubDetail(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEnvelopeFrom(t *testing.T) {
	full := events.Envelope{
		SubscriptionID: "sub-1",
		Operation:      "subscribe",
		IdempotencyKey: "key-1",
		Callback:       &events.Callback{URL: "https://api.mitstack.dev/v1/webhooks/operator-events", Token: "evt-token"},
	}
	cases := []struct {
		name   string
		ctx    context.Context
		want   events.Envelope
		wantOK bool
	}{
		{"it returns the envelope WithEnvelope stashed", events.WithEnvelope(context.Background(), full), full, true},
		{"an envelope with no callback is found, with a nil callback", events.WithEnvelope(context.Background(), events.Envelope{SubscriptionID: "sub-1"}),
			events.Envelope{SubscriptionID: "sub-1"}, true},
		{"a context with no envelope reports not found", context.Background(), events.Envelope{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := events.EnvelopeFrom(tc.ctx)

			if ok != tc.wantOK || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("EnvelopeFrom = %+v, %v; want %+v, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
