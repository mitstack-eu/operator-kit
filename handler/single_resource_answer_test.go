package handler_test

// The expected bodies are exact bytes, so any change to the single-Resource path, a new key included, turns this file red.

import (
	"errors"
	"net/http"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// guardSecret is a request secret the guard cases send, so the scrub and
// the output refusal are part of what the captured bytes pin.
const guardSecret = "guard-secret-token-value"

func guardSecrets() map[string]any {
	return map[string]any{"github": map[string]any{"token": guardSecret}}
}

func stateOf(s contract.State) *contract.State { return &s }

func TestSingleResource_AnswersEveryVerbWithTheSameBytesAsBefore(t *testing.T) {
	cases := []struct {
		name       string
		operation  string
		fake       func() *kittest.FakeResource
		wantStatus int
		wantBody   string
	}{
		{
			name:      "subscribe present",
			operation: "subscribe",
			fake: func() *kittest.FakeResource {
				return &kittest.FakeResource{
					Outputs:      map[string]string{"endpoint": "https://guard.example", "instanceId": "i-guard"},
					StatusDetail: "running",
				}
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"outputs":{"endpoint":"https://guard.example","instanceId":"i-guard"},"statusDetail":"running","absent":false}`,
		},
		{
			name:      "upgrade present",
			operation: "upgrade",
			fake: func() *kittest.FakeResource {
				return &kittest.FakeResource{
					Count:        1,
					Outputs:      map[string]string{"endpoint": "https://guard.example"},
					StatusDetail: "upgraded",
				}
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"outputs":{"endpoint":"https://guard.example"},"statusDetail":"upgraded","absent":false}`,
		},
		{
			name:      "subscribe pending",
			operation: "subscribe",
			fake: func() *kittest.FakeResource {
				return &kittest.FakeResource{ObserveState: stateOf(contract.Unknown)}
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"outputs":null,"pending":true,"statusDetail":"still unknown after 3 attempts"}`,
		},
		{
			name:      "subscribe ensure error carrying a secret",
			operation: "subscribe",
			fake: func() *kittest.FakeResource {
				return &kittest.FakeResource{EnsureErr: errors.New("cloud API rejected " + guardSecret)}
			},
			wantStatus: http.StatusInternalServerError,
			wantBody:   `{"outputs":null,"error":"cloud API rejected [REDACTED]"}`,
		},
		{
			name:      "subscribe refused",
			operation: "subscribe",
			fake: func() *kittest.FakeResource {
				return &kittest.FakeResource{EnsureErr: handler.Refuse("quota", "no capacity in %s", "eu-west-1")}
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"outputs":null,"error":"refused:quota: no capacity in eu-west-1","statusDetail":"refused:quota: no capacity in eu-west-1"}`,
		},
		{
			name:      "subscribe output carrying a secret",
			operation: "subscribe",
			fake: func() *kittest.FakeResource {
				return &kittest.FakeResource{Outputs: map[string]string{"token": guardSecret}}
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"outputs":null,"error":"refused:secret-in-output: output \"token\" carries a secret value","statusDetail":"refused:secret-in-output: output \"token\" carries a secret value"}`,
		},
		{
			name:      "validate present",
			operation: "validate",
			fake: func() *kittest.FakeResource {
				return &kittest.FakeResource{Count: 1, Outputs: map[string]string{"endpoint": "https://guard.example"}}
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"outputs":{"endpoint":"https://guard.example"},"absent":false}`,
		},
		{
			name:       "validate absent",
			operation:  "validate",
			fake:       func() *kittest.FakeResource { return &kittest.FakeResource{} },
			wantStatus: http.StatusOK,
			wantBody:   `{"outputs":null,"absent":true}`,
		},
		{
			name:      "validate unknown",
			operation: "validate",
			fake: func() *kittest.FakeResource {
				return &kittest.FakeResource{ObserveState: stateOf(contract.Unknown), StatusDetail: "converging"}
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"outputs":null,"pending":true,"statusDetail":"converging"}`,
		},
		{
			name:      "validate observe error",
			operation: "validate",
			fake: func() *kittest.FakeResource {
				return &kittest.FakeResource{ObserveErr: errors.New("describe call failed")}
			},
			wantStatus: http.StatusInternalServerError,
			wantBody:   `{"outputs":null,"error":"describe call failed"}`,
		},
		{
			name:      "unsubscribe absent",
			operation: "unsubscribe",
			fake: func() *kittest.FakeResource {
				return &kittest.FakeResource{Count: 1, StatusDetail: "deleted"}
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"outputs":null,"statusDetail":"deleted","absent":true}`,
		},
		{
			name:      "unsubscribe left standing",
			operation: "unsubscribe",
			fake: func() *kittest.FakeResource {
				return &kittest.FakeResource{Count: 1, LeakOnDelete: true}
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"outputs":null,"statusDetail":"observed present after 3 attempts","absent":false}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-guard", 11)
			h := newHandler(t, c.fake(), s)
			status, raw := postHook(t, h, s, envelope(t, s, "nonce-guard-"+c.name, map[string]any{
				"operation": c.operation,
				"secrets":   guardSecrets(),
			}))
			if status != c.wantStatus || string(raw) != c.wantBody {
				t.Errorf("%s answered %d %s\nwant %d %s", c.operation, status, raw, c.wantStatus, c.wantBody)
			}
		})
	}
}
