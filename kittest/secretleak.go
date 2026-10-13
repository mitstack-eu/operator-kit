package kittest

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/webhook"
)

// SecretLeak is one operator's secret-leak fixture: its handler and the webhook requests that carry the seeded secrets.
type SecretLeak struct {
	// Secrets are the seeded values, and each must appear in at least one request body.
	Secrets []string
	// Serve builds the operator's own handler, serving POST /hook and GET /logs behind the returned signer, with logs as its /logs buffer.
	Serve func(logs *webhook.LogBuffer) (http.Handler, *Signer)
	// Requests are posted to /hook in order.
	Requests []LeakRequest
}

// LeakRequest is one webhook body the harness signs and posts to /hook.
type LeakRequest struct {
	Name string
	// Body holds the envelope fields; the harness adds nonce, createdAt, an idempotencyKey when absent, and an OBO token for the body's orgId and subscriptionId.
	Body map[string]any
	// Fails says the verb must answer 4xx or 5xx, so its error path is driven.
	Fails bool
}

// CheckNoSecretLeak drives f and returns one problem per leak and per check that would hold having read nothing.
func CheckNoSecretLeak(f SecretLeak) []string {
	run, ok := driveLeak(f)
	problems := run.problems
	if !ok {
		return problems
	}
	var guards []guarded
	for i, s := range f.Secrets {
		if s == "" {
			problems = append(problems, fmt.Sprintf("seeded secret %d is empty, so its absence proves nothing", i))
			continue
		}
		g := guarded{label: "seeded secret " + redact(s), value: s}
		if !slices.ContainsFunc(run.sent, g.in) {
			problems = append(problems, g.label+" is in no request body, so its absence proves nothing")
		}
		guards = append(guards, g)
	}
	guards = append(guards, run.obos...)
	if !slices.ContainsFunc(f.Requests, func(r LeakRequest) bool { return r.Fails }) {
		problems = append(problems, "no request is marked Fails, so no verb's error path is driven")
	}
	for _, a := range run.answers {
		switch {
		case a.status == http.StatusUnauthorized:
			problems = append(problems, fmt.Sprintf("request %q: POST /hook answered 401, so the handler did not trust the harness's signer and no verb ran", a.name))
		case a.fails && a.status < 400:
			problems = append(problems, fmt.Sprintf("request %q is marked Fails but POST /hook answered %d, so its error path is not driven", a.name, a.status))
		}
		problems = append(problems, leaks(fmt.Sprintf("request %q: the POST /hook response", a.name), a.body, guards)...)
	}
	problems = append(problems, leaks("the standard logger's log output", run.logOutput, guards)...)
	return append(problems, logsProblems(run.logsStatus, run.logsBody, guards)...)
}

// AssertNoSecretLeak fails tb with each problem CheckNoSecretLeak returns.
func AssertNoSecretLeak(tb testing.TB, f SecretLeak) {
	tb.Helper()
	for _, p := range CheckNoSecretLeak(f) {
		tb.Error(p)
	}
}

type hookAnswer struct {
	name   string
	fails  bool
	status int
	body   string
}

type leakRun struct {
	problems   []string
	sent       []string
	answers    []hookAnswer
	obos       []guarded
	logsStatus int
	logsBody   string
	logOutput  string
}

func driveLeak(f SecretLeak) (run leakRun, ok bool) {
	logs := webhook.NewLogBuffer(0)
	var captured bytes.Buffer
	prev := log.Writer()
	tee := io.MultiWriter(&captured, logs.Writer(), prev)
	log.SetOutput(tee)
	// The previous writer comes back on every path, a panic in Serve included.
	defer func() {
		log.SetOutput(prev)
		run.logOutput = captured.String()
	}()

	h, signer := f.Serve(logs)
	if log.Writer() != tee {
		run.problems = append(run.problems, "Serve replaced the standard logger's writer, so the log output check would read nothing")
	}
	if h == nil || signer == nil {
		run.problems = append(run.problems, "Serve returned no handler or no signer, so nothing was driven")
		return run, false
	}
	for _, lr := range f.Requests {
		body, obo, err := envelope(lr.Body, signer, time.Now())
		if err != nil {
			run.problems = append(run.problems, fmt.Sprintf("request %q: building its signed body: %v", lr.Name, err))
			continue
		}
		// The decoder's error is not quoted, since it can carry a field's value.
		if _, err := contract.DecodeRequest(body); err != nil {
			run.problems = append(run.problems, fmt.Sprintf("request %q: its body is not an envelope the kit's decoder accepts, so no verb ran", lr.Name))
			continue
		}
		run.sent = append(run.sent, string(body))
		run.obos = append(run.obos, guarded{label: fmt.Sprintf("the OBO token minted for request %q", lr.Name), value: obo})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, signer.hookRequest("/hook", body))
		run.answers = append(run.answers, hookAnswer{name: lr.Name, fails: lr.Fails, status: rec.Code, body: rec.Body.String()})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signer.logsRequest("/logs", time.Now()))
	run.logsStatus, run.logsBody = rec.Code, rec.Body.String()
	return run, true
}

func envelope(fields map[string]any, signer *Signer, now time.Time) ([]byte, string, error) {
	body := make(map[string]any, len(fields)+4)
	for k, v := range fields {
		body[k] = v
	}
	orgID, _ := fields["orgId"].(string)
	subscriptionID, _ := fields["subscriptionId"].(string)
	obo, err := webhook.MintOBO(signer.Private, signer.Kid,
		webhook.OBOClaims{OrgID: orgID, SubscriptionID: subscriptionID}, 5*time.Minute, now)
	if err != nil {
		return nil, "", err
	}
	body["nonce"] = freshToken()
	body["createdAt"] = now.UTC().Format(time.RFC3339Nano)
	if _, set := body["idempotencyKey"]; !set {
		body["idempotencyKey"] = freshToken()
	}
	body["obo"] = obo
	raw, err := json.Marshal(body)
	return raw, obo, err
}

func freshToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func logsProblems(status int, body string, guards []guarded) []string {
	if status != http.StatusOK {
		return append([]string{fmt.Sprintf("GET /logs answered %d, not 200, so its lines were never read", status)},
			leaks("the GET /logs body", body, guards)...)
	}
	var served struct {
		Lines *[]string `json:"lines"`
	}
	if err := json.Unmarshal([]byte(body), &served); err != nil || served.Lines == nil {
		return append([]string{`GET /logs served a body that is not {"lines": [...]}, so its lines were never read`},
			leaks("the GET /logs body", body, guards)...)
	}
	if len(*served.Lines) == 0 {
		return []string{"GET /logs served no lines, so their lack of a secret proves nothing"}
	}
	var problems []string
	for i, line := range *served.Lines {
		problems = append(problems, leaks(fmt.Sprintf("GET /logs line %d", i), line, guards)...)
	}
	return problems
}

type guarded struct {
	// label never carries the value it names.
	label string
	value string
}

func (g guarded) in(text string) bool {
	if strings.Contains(text, g.value) {
		return true
	}
	raw, _ := json.Marshal(g.value)
	escaped := strings.Trim(string(raw), `"`)
	return escaped != g.value && strings.Contains(text, escaped)
}

func leaks(channel, text string, guards []guarded) []string {
	var problems []string
	for _, g := range guards {
		if g.in(text) {
			problems = append(problems, channel+" carries "+g.label)
		}
	}
	return problems
}
