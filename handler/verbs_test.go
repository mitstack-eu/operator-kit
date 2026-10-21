// The handler answers each verb over a single Resource with exact bytes, so any change to that path, a new key included, turns a row red.
package handler_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/handler"
	"github.com/mitstack-eu/operator-kit/v3/kittest"
	"github.com/mitstack-eu/operator-kit/v3/webhook"
)

// guardSecret is a request secret every row sends, so the scrub and the output refusal are part of what the bytes pin.
const guardSecret = "guard-secret-token-value"

func guardSecrets() map[string]any {
	return map[string]any{"github": map[string]any{"token": guardSecret}}
}

func stateOf(s contract.State) *contract.State { return &s }

func wantEach(t *testing.T, what string, calls []contract.Desired, check func(contract.Desired) (got, want any)) {
	t.Helper()
	if len(calls) == 0 {
		t.Fatalf("%s was never called", what)
	}
	for i, d := range calls {
		if got, want := check(d); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s call %d saw %v, want %v", what, i+1, got, want)
		}
	}
}

func TestServe_AnswersEachVerbOverOneResource(t *testing.T) {
	present := `{"outputs":null,"absent":false}`
	absent := `{"outputs":null,"absent":true}`
	instances := map[string]any{"instances": awsInstances("eu-central-1")}
	sawInstance := func(t *testing.T, r *resource) {
		wantEach(t, "Observe", r.observed, func(d contract.Desired) (any, any) {
			aws := d.Instances["aws"]
			return []string{aws.Config["region"], aws.Name, aws.Endpoint}, []string{"eu-central-1", "Consumer AWS", "https://aws.example.com"}
		})
	}
	sawSecret := func(what string, calls func(*resource) []contract.Desired) func(*testing.T, *resource) {
		return func(t *testing.T, r *resource) {
			wantEach(t, what, calls(r), func(d contract.Desired) (any, any) { return d.Secrets["github"]["token"], guardSecret })
		}
	}
	cases := []struct {
		name   string
		op     string
		r      *resource
		config []func(*handler.Config)
		fields map[string]any
		status int
		calls  [3]int
		class  string
		body   string
		check  func(t *testing.T, r *resource)
	}{
		{name: "a subscribe observed present answers the block's outputs and detail", op: "subscribe",
			r:      fake(&kittest.FakeResource{Outputs: map[string]string{"endpoint": "https://guard.example", "instanceId": "i-guard"}, StatusDetail: "running"}),
			status: http.StatusOK, calls: [3]int{1, 1, 0},
			body: `{"outputs":{"endpoint":"https://guard.example","instanceId":"i-guard"},"statusDetail":"running","absent":false}`},
		{name: "an upgrade observed present answers the block's outputs and detail", op: "upgrade",
			r:      fake(&kittest.FakeResource{Count: 1, Outputs: map[string]string{"endpoint": "https://guard.example"}, StatusDetail: "upgraded"}),
			status: http.StatusOK, calls: [3]int{1, 1, 0},
			body: `{"outputs":{"endpoint":"https://guard.example"},"statusDetail":"upgraded","absent":false}`},
		{name: "a subscribe observed unknown through the budget answers pending, never an error", op: "subscribe",
			r:      fake(&kittest.FakeResource{ObserveState: stateOf(contract.Unknown)}),
			status: http.StatusOK, calls: [3]int{1, 3, 0},
			body: `{"outputs":null,"pending":true,"statusDetail":"still unknown after 3 attempts"}`},
		{name: "a subscribe never observed present answers pending naming the attempts", op: "subscribe",
			r:      fake(&kittest.FakeResource{ObserveState: stateOf(contract.Absent)}),
			status: http.StatusOK, calls: [3]int{1, 3, 0},
			body: `{"outputs":null,"pending":true,"statusDetail":"still absent after 3 attempts"}`},
		{name: "an upgrade never observed present answers pending", op: "upgrade",
			r:      fake(&kittest.FakeResource{ObserveState: stateOf(contract.Unknown)}),
			status: http.StatusOK, calls: [3]int{1, 3, 0},
			body: `{"outputs":null,"pending":true,"statusDetail":"still unknown after 3 attempts"}`},
		{name: "an ensure error is a 500 scrubbed of the request secret", op: "subscribe",
			r:      fake(&kittest.FakeResource{EnsureErr: errors.New("cloud API rejected " + guardSecret)}),
			status: http.StatusInternalServerError, calls: [3]int{1, 0, 0},
			body: `{"outputs":null,"error":"cloud API rejected [REDACTED]"}`},
		{name: "an ensure refusal is a 400 carrying its class", op: "subscribe",
			r:      fake(&kittest.FakeResource{EnsureErr: handler.Refuse("quota", "no capacity in %s", "eu-west-1")}),
			status: http.StatusBadRequest, calls: [3]int{1, 0, 0}, class: "quota",
			body: `{"outputs":null,"error":"refused:quota: no capacity in eu-west-1","statusDetail":"refused:quota: no capacity in eu-west-1"}`},
		{name: "a wrapped refusal is still a 400 carrying its class", op: "subscribe",
			r:      fake(&kittest.FakeResource{EnsureErr: fmt.Errorf("ensure: %w", handler.Refuse("quota-exceeded", "org quota exhausted"))}),
			status: http.StatusBadRequest, calls: [3]int{1, 0, 0}, class: "quota-exceeded",
			body: `{"outputs":null,"error":"ensure: refused:quota-exceeded: org quota exhausted","statusDetail":"refused:quota-exceeded: org quota exhausted"}`},
		{name: "a refusal with an empty class renders as unclassified", op: "subscribe",
			r:      fake(&kittest.FakeResource{EnsureErr: handler.Refuse("", "no class given")}),
			status: http.StatusBadRequest, calls: [3]int{1, 0, 0}, class: "unclassified",
			body: `{"outputs":null,"error":"refused:unclassified: no class given","statusDetail":"refused:unclassified: no class given"}`},
		{name: "a settled output carrying a request secret is refused naming its key", op: "subscribe",
			r:      fake(&kittest.FakeResource{Outputs: map[string]string{"token": guardSecret}}),
			status: http.StatusBadRequest, calls: [3]int{1, 1, 0}, class: "secret-in-output",
			body: `{"outputs":null,"error":"refused:secret-in-output: output \"token\" carries a secret value","statusDetail":"refused:secret-in-output: output \"token\" carries a secret value"}`},
		{name: "a validate observes once and answers present with outputs and detail", op: "validate",
			r:      fake(&kittest.FakeResource{Count: 1, Outputs: map[string]string{"endpoint": "https://guard.example"}, StatusDetail: "healthy"}),
			status: http.StatusOK, calls: [3]int{0, 1, 0},
			body: `{"outputs":{"endpoint":"https://guard.example"},"statusDetail":"healthy","absent":false}`},
		{name: "a validate observed absent answers absent with outputs and detail", op: "validate",
			r:      fake(&kittest.FakeResource{Outputs: map[string]string{"k": "v"}, StatusDetail: "detail-absent"}),
			status: http.StatusOK, calls: [3]int{0, 1, 0},
			body: `{"outputs":{"k":"v"},"statusDetail":"detail-absent","absent":true}`},
		{name: "a validate observed unknown leaves absent out and adds pending", op: "validate",
			r:      fake(&kittest.FakeResource{ObserveState: stateOf(contract.Unknown), Outputs: map[string]string{"k": "v"}, StatusDetail: "converging"}),
			status: http.StatusOK, calls: [3]int{0, 1, 0},
			body: `{"outputs":{"k":"v"},"pending":true,"statusDetail":"converging"}`},
		{name: "a validate observed unknown with its work finished adds redrive beside pending", op: "validate", r: fake(&kittest.FakeResource{ObserveState: stateOf(contract.Unknown), Redrive: true}),
			status: http.StatusOK, calls: [3]int{0, 1, 0}, body: `{"outputs":null,"pending":true,"redrive":true}`},
		{name: "a validate observe error is classified, never pending", op: "validate",
			r:      fake(&kittest.FakeResource{ObserveErr: errors.New("describe call failed")}),
			status: http.StatusInternalServerError, calls: [3]int{0, 1, 0},
			body: `{"outputs":null,"error":"describe call failed"}`},
		{name: "an unsubscribe answers absent once Observe reports absent", op: "unsubscribe",
			r:      fake(&kittest.FakeResource{Count: 1, StatusDetail: "deleted"}),
			status: http.StatusOK, calls: [3]int{0, 1, 1},
			body: `{"outputs":null,"statusDetail":"deleted","absent":true}`},
		{name: "an unsubscribe leaving the resource present answers absent false with detail after the budget", op: "unsubscribe",
			r:      fake(&kittest.FakeResource{Count: 1, LeakOnDelete: true}),
			status: http.StatusOK, calls: [3]int{0, 3, 1},
			body: `{"outputs":null,"statusDetail":"observed present after 3 attempts","absent":false}`},
		{name: "an unsubscribe observed unknown still answers a definite absent false", op: "unsubscribe",
			r:      fake(&kittest.FakeResource{Count: 1, ObserveState: stateOf(contract.Unknown)}),
			status: http.StatusOK, calls: [3]int{0, 3, 1},
			body: `{"outputs":null,"statusDetail":"observed unknown after 3 attempts","absent":false}`},
		{name: "an observe error after a successful delete is an error with no absent marker", op: "unsubscribe",
			r:      fake(&kittest.FakeResource{Count: 1, ObserveErr: errors.New("observe backend unavailable")}),
			status: http.StatusInternalServerError, calls: [3]int{0, 1, 1},
			body: `{"outputs":null,"error":"observe backend unavailable"}`},
		{name: "a failed delete is an error and the resource is never observed", op: "unsubscribe",
			r:      fake(&kittest.FakeResource{Count: 1, DeleteErr: errors.New("handover: snapshot did not complete")}),
			fields: map[string]any{"params": map[string]string{"dataHandover": "snapshot"}},
			status: http.StatusInternalServerError, calls: [3]int{0, 0, 1},
			body: `{"outputs":null,"error":"handover: snapshot did not complete"}`},
		{name: "a refused teardown is a 400 whose statusDetail carries the class", op: "unsubscribe",
			r:      fake(&kittest.FakeResource{Count: 1, DeleteErr: handler.Refuse("dependents-remain", "two apps still bind this database")}),
			status: http.StatusBadRequest, calls: [3]int{0, 0, 1}, class: "dependents-remain",
			body: `{"outputs":null,"error":"refused:dependents-remain: two apps still bind this database","statusDetail":"refused:dependents-remain: two apps still bind this database"}`},
		{name: "an unsubscribe hands Delete the request's params", op: "unsubscribe", r: fake(&kittest.FakeResource{Count: 1}),
			fields: map[string]any{"params": map[string]string{"dataHandover": "snapshot"}},
			status: http.StatusOK, calls: [3]int{0, 1, 1}, body: absent,
			check: func(t *testing.T, r *resource) {
				wantEach(t, "Delete", r.deleted, func(d contract.Desired) (any, any) { return d.Params["dataHandover"], "snapshot" })
			}},
		{name: "an unsubscribe merges Delete's outputs into the absent answer", op: "unsubscribe",
			r:      &resource{FakeResource: &kittest.FakeResource{Count: 1}, deleteOutputs: map[string]string{"dataSnapshotId": "snap-456"}},
			status: http.StatusOK, calls: [3]int{0, 1, 1}, body: `{"outputs":{"dataSnapshotId":"snap-456"},"absent":true}`},
		{name: "an unsubscribe hands Delete the request's secrets", op: "unsubscribe", r: fake(&kittest.FakeResource{Count: 1}),
			status: http.StatusOK, calls: [3]int{0, 1, 1}, body: absent,
			check: sawSecret("Delete", func(r *resource) []contract.Desired { return r.deleted })},
		{name: "a validate output exactly matching a secret is refused naming its key", op: "validate",
			r:      fake(&kittest.FakeResource{Count: 1, Outputs: map[string]string{"leakedToken": guardSecret}}),
			status: http.StatusBadRequest, calls: [3]int{0, 1, 0}, class: "secret-in-output",
			body: `{"outputs":null,"error":"refused:secret-in-output: output \"leakedToken\" carries a secret value","statusDetail":"refused:secret-in-output: output \"leakedToken\" carries a secret value"}`},
		{name: "a delete output exactly matching a secret is refused naming its key", op: "unsubscribe",
			r:      &resource{FakeResource: &kittest.FakeResource{Count: 1}, deleteOutputs: map[string]string{"leakedOnDelete": guardSecret}},
			status: http.StatusBadRequest, calls: [3]int{0, 1, 1}, class: "secret-in-output",
			body: `{"outputs":null,"error":"refused:secret-in-output: output \"leakedOnDelete\" carries a secret value","statusDetail":"refused:secret-in-output: output \"leakedOnDelete\" carries a secret value"}`},
		{name: "an output merely containing a non-secret request value is allowed", op: "validate",
			r:      fake(&kittest.FakeResource{Count: 1, Outputs: map[string]string{"repo": "acme-co/weather-app"}}),
			fields: map[string]any{"secrets": map[string]any{"github": map[string]any{"owner": "acme-co", "token": guardSecret}}},
			status: http.StatusOK, calls: [3]int{0, 1, 0}, body: `{"outputs":{"repo":"acme-co/weather-app"},"absent":false}`},
		{name: "a SecretOutputs value carrying a secret is not refused", op: "validate",
			r:      &resource{FakeResource: &kittest.FakeResource{Count: 1}, secretOutputs: map[string]string{"apiToken": guardSecret}},
			status: http.StatusOK, calls: [3]int{0, 1, 0},
			body: `{"outputs":null,"secretOutputs":{"apiToken":"` + guardSecret + `"},"absent":false}`},
		{name: "an empty output is not refused beside an unset secret", op: "validate",
			r:      fake(&kittest.FakeResource{Count: 1, Outputs: map[string]string{"blankField": "", "status": "ok"}}),
			fields: map[string]any{"secrets": map[string]any{"github": map[string]any{"unsetSecret": "", "token": guardSecret}}},
			status: http.StatusOK, calls: [3]int{0, 1, 0}, body: `{"outputs":{"blankField":"","status":"ok"},"absent":false}`},
		{name: "a validate's Observe receives the request's secrets", op: "validate", r: fake(&kittest.FakeResource{Count: 1}),
			status: http.StatusOK, calls: [3]int{0, 1, 0}, body: present,
			check: sawSecret("Observe", func(r *resource) []contract.Desired { return r.observed })},
		{name: "a validate's Observe receives the request's params", op: "validate", r: fake(&kittest.FakeResource{Count: 1}),
			fields: map[string]any{"params": map[string]string{"region": "eu-central-1"}},
			status: http.StatusOK, calls: [3]int{0, 1, 0}, body: present,
			check: func(t *testing.T, r *resource) {
				wantEach(t, "Observe", r.observed, func(d contract.Desired) (any, any) { return d.Params["region"], "eu-central-1" })
			}},
		{name: "every settle poll of a subscribe receives the request's secrets", op: "subscribe",
			r:      &resource{FakeResource: &kittest.FakeResource{}, settleAfter: 3},
			status: http.StatusOK, calls: [3]int{1, 3, 0}, body: present,
			check: sawSecret("Observe", func(r *resource) []contract.Desired { return r.observed })},
		{name: "every settle poll of an unsubscribe receives the request's secrets", op: "unsubscribe",
			r:      &resource{FakeResource: &kittest.FakeResource{Count: 1}, settleAfter: 3},
			status: http.StatusOK, calls: [3]int{0, 3, 1}, body: absent,
			check: sawSecret("Observe", func(r *resource) []contract.Desired { return r.observed })},
		{name: "a validate's Observe receives the request's instance config", op: "validate",
			r: fake(&kittest.FakeResource{ObserveState: stateOf(contract.Unknown)}), fields: instances,
			status: http.StatusOK, calls: [3]int{0, 1, 0}, body: `{"outputs":null,"pending":true}`, check: sawInstance},
		{name: "every settle poll of a subscribe receives the request's instance config", op: "subscribe",
			r: fake(&kittest.FakeResource{ObserveState: stateOf(contract.Unknown)}), fields: instances,
			status: http.StatusOK, calls: [3]int{1, 3, 0},
			body: `{"outputs":null,"pending":true,"statusDetail":"still unknown after 3 attempts"}`, check: sawInstance},
		{name: "every settle poll of an unsubscribe receives the request's instance config", op: "unsubscribe",
			r: fake(&kittest.FakeResource{ObserveState: stateOf(contract.Unknown)}), fields: instances,
			status: http.StatusOK, calls: [3]int{0, 3, 1},
			body: `{"outputs":null,"statusDetail":"observed unknown after 3 attempts","absent":false}`, check: sawInstance},
		{name: "Ensure receives the request's instance config through Desired and its Identity", op: "subscribe",
			r: fake(&kittest.FakeResource{ObserveState: stateOf(contract.Unknown)}), fields: instances,
			status: http.StatusOK, calls: [3]int{1, 3, 0},
			body: `{"outputs":null,"pending":true,"statusDetail":"still unknown after 3 attempts"}`,
			check: func(t *testing.T, r *resource) {
				wantEach(t, "Ensure", r.ensured, func(d contract.Desired) (any, any) {
					return []string{instanceRegion(d.Instances), instanceRegion(d.Identity.Instances)}, []string{"eu-central-1", "eu-central-1"}
				})
			}},
		{name: "an unsubscribe carrying no instances succeeds with a nil instance map", op: "unsubscribe",
			r:      fake(&kittest.FakeResource{ObserveState: stateOf(contract.Absent)}),
			status: http.StatusOK, calls: [3]int{0, 1, 1}, body: absent,
			check: func(t *testing.T, r *resource) {
				nilInstances := func(d contract.Desired) (any, any) { return d.Instances == nil, true }
				wantEach(t, "Delete", r.deleted, nilInstances)
				wantEach(t, "Observe", r.observed, nilInstances)
			}},
		{name: "an unsubscribe hands Delete the request's priorOutputs", op: "unsubscribe", r: fake(&kittest.FakeResource{Count: 1}),
			fields: map[string]any{"priorOutputs": map[string]string{"region": "eu-west-1"}},
			status: http.StatusOK, calls: [3]int{0, 1, 1}, body: absent,
			check: func(t *testing.T, r *resource) {
				wantEach(t, "Delete", r.deleted, func(d contract.Desired) (any, any) {
					return []any{d.PriorOutputs["region"], d.Instances == nil}, []any{"eu-west-1", true}
				})
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logged := captureLog(t)
			x := newHarness(t, tc.r, tc.config...)
			fields := map[string]any{"operation": tc.op, "secrets": guardSecrets()}
			for k, v := range tc.fields {
				fields[k] = v
			}

			status, raw := x.post(fields)

			if status != tc.status || string(raw) != tc.body {
				t.Errorf("%s answered %d %s\nwant %d %s", tc.op, status, raw, tc.status, tc.body)
			}
			assertConforms(t, raw)
			assertCalls(t, tc.r.FakeResource, tc.calls)
			if got := handler.RefusedClass(decodeResponse(t, raw).StatusDetail); got != tc.class {
				t.Errorf("refusal class = %q, want %q", got, tc.class)
			}
			kittest.AssertNoSecret(t, "log output", logged.String(), guardSecret)
			if tc.check != nil {
				tc.check(t, tc.r)
			}
		})
	}
}

// A Handler that stashed one verb's secrets in its own state would hand them to a later, unrelated request here.
func TestServe_OneVerbsSecretsNeverReachALaterRequest(t *testing.T) {
	r := fake(&kittest.FakeResource{Count: 1})
	x := newHarness(t, r)

	first, _ := x.post(map[string]any{"operation": "validate", "subscriptionId": "sub-scope-a", "secrets": guardSecrets()})
	second, _ := x.post(map[string]any{"operation": "validate", "subscriptionId": "sub-scope-b"})

	if first != http.StatusOK || second != http.StatusOK || len(r.observed) != 2 {
		t.Fatalf("statuses %d, %d with %d observes, want 200, 200 and one observe each", first, second, len(r.observed))
	}
	if got := r.observed[0].Secrets["github"]["token"]; got != guardSecret {
		t.Errorf("the first request's Observe saw token %q, want its own secret", got)
	}
	if got := r.observed[1].Secrets; len(got) != 0 {
		t.Errorf("the second request's Observe saw Secrets %v, want none", got)
	}
}

func TestRefusedClass(t *testing.T) {
	cases := map[string]string{
		"refused:dependents-remain: two apps still bind this database":               "dependents-remain",
		handler.Refuse(handler.ClassHostCannotRun, "Docker does not answer").Error(): "host-cannot-run",
		"":                   "",
		"some other detail":  "",
		"refusedwithnocolon": "",
		"REFUSED:upper-case": "",
	}
	for detail, want := range cases {
		if got := handler.RefusedClass(detail); got != want {
			t.Errorf("RefusedClass(%q) = %q, want %q", detail, got, want)
		}
	}
}

// The block's own error quotes the secret, so scrubbing must cover block-authored text in the answer and the log alike.
func TestServe_NeverLeaksAnySecretTheRequestCarries(t *testing.T) {
	const secret, input, appSecret, eventsToken = "top-secret-db-password-value", "top-secret-input-value-here", "top-secret-app-secret-value", "top-secret-events-callback-token"
	logged := captureLog(t)
	x := newHarness(t, &kittest.FakeResource{EnsureErr: fmt.Errorf("upstream rejected credential %s", secret)})
	obo, err := webhook.MintOBO(x.s.Private, x.s.Kid, webhook.OBOClaims{OrgID: "org-1", AppID: "app-1", SubscriptionID: "sub-1"}, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("minting a test OBO token: %v", err)
	}

	status, raw := x.post(map[string]any{
		"obo":            obo,
		"secrets":        map[string]any{"db": map[string]any{"password": secret}},
		"secretInputs":   map[string]any{"apiKey": input},
		"appSecrets":     map[string]any{"masterKey": appSecret},
		"eventsCallback": map[string]any{"url": "http://127.0.0.1:1/events", "token": eventsToken},
	})

	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for the Ensure error, body: %s", status, raw)
	}
	for what, haystack := range map[string]string{"response body": string(raw), "log output": logged.String()} {
		kittest.AssertNoSecret(t, what, haystack, secret, input, appSecret, obo, eventsToken)
	}
}
