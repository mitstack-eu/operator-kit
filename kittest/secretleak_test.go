package kittest_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/webhook"
)

const leakSecret = "sk-live-leak-canary-0f9e8d7c6b5a"

type leakKnobs struct {
	leakToLogs     bool
	leakOBOToLogs  bool
	leakInError    bool
	leakToPrintf   bool
	failAnswersOK  bool
	failAnswers401 bool
	logsStatus     int
	noLogLines     bool
}

type leakyFake struct {
	leakKnobs

	signer  *kittest.Signer
	logs    *webhook.LogBuffer
	private *webhook.LogBuffer
	replay  *webhook.Replay

	mu       sync.Mutex
	accepted []contract.Request
	rejected []string
}

func (f *leakyFake) deps() webhook.Deps {
	return webhook.Deps{
		JWKSURL: "http://jwks.invalid",
		PublicKey: func(kid string) (ed25519.PublicKey, error) {
			if kid != f.signer.Kid {
				return nil, fmt.Errorf("unknown kid %q", kid)
			}
			return f.signer.Public, nil
		},
		Replay: f.replay,
	}
}

func (f *leakyFake) reject(w http.ResponseWriter, status int, reason string) {
	f.mu.Lock()
	f.rejected = append(f.rejected, reason)
	f.mu.Unlock()
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": reason})
}

func (f *leakyFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/hook":
		f.serveHook(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/logs":
		f.serveLogs(w, r)
	default:
		f.reject(w, http.StatusNotFound, "fake: unmodelled "+r.Method+" "+r.URL.Path)
	}
}

func (f *leakyFake) serveHook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.reject(w, http.StatusBadRequest, "fake: reading body: "+err.Error())
		return
	}
	pub, ok := webhook.AuthenticateHook(f.deps(), recorderOnly{}, r, body)
	if !ok {
		f.reject(w, http.StatusUnauthorized, "fake: hook signature or replay check failed")
		return
	}
	req, err := contract.DecodeRequest(body)
	if err != nil {
		f.reject(w, http.StatusBadRequest, "fake: "+err.Error())
		return
	}
	if req.IdempotencyKey == "" {
		f.reject(w, http.StatusBadRequest, "fake: no idempotencyKey")
		return
	}
	claims, err := webhook.VerifyOBO(pub, req.OBO, time.Now())
	if err != nil {
		f.reject(w, http.StatusUnauthorized, "fake: obo: "+err.Error())
		return
	}
	if claims.OrgID != req.OrgID || claims.SubscriptionID != req.SubscriptionID {
		f.reject(w, http.StatusUnauthorized, fmt.Sprintf("fake: obo claims %+v do not match org %q subscription %q", claims, req.OrgID, req.SubscriptionID))
		return
	}
	f.mu.Lock()
	f.accepted = append(f.accepted, req)
	f.mu.Unlock()

	if f.failAnswers401 && req.Params["fail"] == "true" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "fake: signer not trusted"})
		return
	}

	token := req.Secrets["api"]["token"]
	if f.leakToLogs {
		f.logs.Append("fake: using token " + token)
	}
	if f.leakOBOToLogs {
		f.logs.Append("fake: acting on behalf of " + req.OBO)
	}
	if f.leakToPrintf {
		log.Printf("fake: using token %s", token)
		f.private.Append("fake: " + req.Operation + " handled")
	} else {
		log.Printf("fake: %s %s handled", req.Operation, req.SubscriptionID)
	}

	w.Header().Set("Content-Type", "application/json")
	if req.Params["fail"] != "true" {
		_ = json.NewEncoder(w).Encode(map[string]any{"outputs": map[string]string{"id": "res-1"}})
		return
	}
	msg := "upstream refused the credential"
	if f.leakInError {
		msg = "upstream refused the credential " + token
	}
	if f.failAnswersOK {
		_ = json.NewEncoder(w).Encode(map[string]any{"outputs": nil, "error": msg})
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]any{"outputs": nil, "error": msg})
}

func (f *leakyFake) serveLogs(w http.ResponseWriter, r *http.Request) {
	if f.logsStatus != 0 {
		w.WriteHeader(f.logsStatus)
		return
	}
	if _, ok := webhook.AuthenticateLogsGET(f.deps(), recorderOnly{}, r); !ok {
		f.reject(w, http.StatusUnauthorized, "fake: logs signature check failed")
		return
	}
	lines := f.logs.Snapshot()
	switch {
	case f.noLogLines:
		lines = []string{}
	case f.leakToPrintf:
		lines = f.private.Snapshot()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"lines": lines})
}

// recorderOnly swallows the webhook package's own 401 so the fake writes exactly one response.
type recorderOnly struct{}

func (recorderOnly) Header() http.Header         { return http.Header{} }
func (recorderOnly) Write(p []byte) (int, error) { return len(p), nil }
func (recorderOnly) WriteHeader(int)             {}

func leakBody(extra map[string]any) map[string]any {
	body := map[string]any{
		"operation":      "subscribe",
		"subscriptionId": "sub-1",
		"blockId":        "block-1",
		"orgId":          "org-1",
		"secrets":        map[string]any{"api": map[string]any{"token": leakSecret}},
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func leakFixture(t *testing.T, f *leakyFake) kittest.SecretLeak {
	t.Helper()
	f.signer = kittest.NewSigner(t, "kid-leak", 11)
	f.private = webhook.NewLogBuffer(0)
	f.replay = webhook.NewReplay()
	return kittest.SecretLeak{
		Secrets: []string{leakSecret},
		Serve: func(logs *webhook.LogBuffer) (http.Handler, *kittest.Signer) {
			f.logs = logs
			return f, f.signer
		},
		Requests: []kittest.LeakRequest{
			{Name: "subscribe", Body: leakBody(nil)},
			{Name: "subscribe-refused", Body: leakBody(map[string]any{"params": map[string]string{"fail": "true"}}), Fails: true},
		},
	}
}

// quietLog sends the standard logger to a buffer for the test, so a deliberate leak never reaches test output.
func quietLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var sink bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&sink)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &sink
}

func containsAllFold(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(strings.ToLower(s), strings.ToLower(p)) {
			return false
		}
	}
	return true
}

func TestCheckNoSecretLeak_ReportsEachLeakAndVacuousFixture(t *testing.T) {
	const neverSent = "never-sent-canary-value-91"
	cases := []struct {
		name   string
		knobs  leakKnobs
		adjust func(fx *kittest.SecretLeak)
		// match is nil when the fixture must yield no problem at all.
		match func(problem string) bool
		desc  string
	}{
		{
			name: "clean handler",
		},
		{
			name:  "leaks a seeded secret through /logs",
			knobs: leakKnobs{leakToLogs: true},
			match: func(p string) bool { return containsAllFold(p, "/logs") },
			desc:  "a problem naming /logs",
		},
		{
			name:  "leaks the minted OBO token through /logs",
			knobs: leakKnobs{leakOBOToLogs: true},
			match: func(p string) bool { return containsAllFold(p, "/logs") },
			desc:  "a problem naming /logs",
		},
		{
			name:  "leaks a seeded secret through a verb's error",
			knobs: leakKnobs{leakInError: true},
			match: func(p string) bool { return containsAllFold(p, "response", "subscribe-refused") },
			desc:  "a response problem naming the request subscribe-refused",
		},
		{
			name:  "leaks a seeded secret through log.Printf",
			knobs: leakKnobs{leakToPrintf: true},
			match: func(p string) bool {
				return containsAllFold(p, "log output") || containsAllFold(p, "standard logger")
			},
			desc: "a problem naming the log output or the standard logger",
		},
		{
			name:   "a seeded secret in no request body",
			adjust: func(fx *kittest.SecretLeak) { fx.Secrets = append(fx.Secrets, neverSent) },
			match:  func(p string) bool { return containsAllFold(p, "in no request body") },
			desc:   "a problem saying the secret is in no request body",
		},
		{
			name:   "an empty seeded secret",
			adjust: func(fx *kittest.SecretLeak) { fx.Secrets = append(fx.Secrets, "") },
			match:  func(p string) bool { return containsAllFold(p, "proves nothing") },
			desc:   "a problem saying the empty secret's absence proves nothing",
		},
		{
			name:   "no request marked Fails",
			adjust: func(fx *kittest.SecretLeak) { fx.Requests = fx.Requests[:1] },
			match:  func(p string) bool { return containsAllFold(p, "no request is marked Fails") },
			desc:   "a problem saying no request is marked Fails",
		},
		{
			name:  "a Fails request answering 200",
			knobs: leakKnobs{failAnswersOK: true},
			match: func(p string) bool { return containsAllFold(p, "subscribe-refused", "200") },
			desc:  "a problem naming subscribe-refused and its status 200",
		},
		{
			name:  "/logs answering 401",
			knobs: leakKnobs{logsStatus: http.StatusUnauthorized},
			match: func(p string) bool { return containsAllFold(p, "/logs", "401") },
			desc:  "a problem naming /logs and its status 401",
		},
		{
			name:  "a Fails request answering 401 on /hook",
			knobs: leakKnobs{failAnswers401: true},
			match: func(p string) bool { return containsAllFold(p, "subscribe-refused", "401") },
			desc:  "a problem naming subscribe-refused and its status 401",
		},
		{
			name:  "/logs serving no lines",
			knobs: leakKnobs{noLogLines: true},
			match: func(p string) bool { return containsAllFold(p, "/logs", "lines") },
			desc:  "a problem saying /logs served no lines",
		},
		{
			name:   "a Fails request whose body is not an envelope",
			adjust: func(fx *kittest.SecretLeak) { fx.Requests[1].Body["operation"] = "subscrib" },
			match: func(p string) bool {
				return containsAllFold(p, "subscribe-refused", "not an envelope", "no verb ran")
			},
			desc: "a problem naming subscribe-refused, saying it is not an envelope and no verb ran",
		},
		{
			name: "a Serve that replaces the standard logger",
			adjust: func(fx *kittest.SecretLeak) {
				serve := fx.Serve
				fx.Serve = func(logs *webhook.LogBuffer) (http.Handler, *kittest.Signer) {
					h, s := serve(logs)
					log.SetOutput(io.Discard)
					return h, s
				}
			},
			match: func(p string) bool { return containsAllFold(p, "replaced the standard logger") },
			desc:  "a problem saying Serve replaced the standard logger",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := quietLog(t)
			f := &leakyFake{leakKnobs: tc.knobs}
			fx := leakFixture(t, f)
			if tc.adjust != nil {
				tc.adjust(&fx)
			}

			problems := kittest.CheckNoSecretLeak(fx)

			if log.Writer() != io.Writer(sink) {
				t.Errorf("log.Writer() after CheckNoSecretLeak is %T, want the writer set before it ran", log.Writer())
			}
			if tc.match == nil {
				if len(problems) != 0 {
					t.Errorf("problems = %q, want none for a clean handler", problems)
				}
			} else {
				found := false
				for _, p := range problems {
					if tc.match(p) {
						found = true
					}
				}
				if !found {
					t.Errorf("problems = %q, want %s", problems, tc.desc)
				}
			}

			f.mu.Lock()
			values := []string{leakSecret, neverSent}
			for _, req := range f.accepted {
				values = append(values, req.OBO)
			}
			f.mu.Unlock()
			for _, p := range problems {
				for _, v := range values {
					if v != "" && strings.Contains(p, v) {
						t.Errorf("problem %q carries a seeded secret or OBO token in the clear", p)
					}
				}
			}
		})
	}
}

func TestCheckNoSecretLeak_SignsEveryRequestWithAFreshEnvelope(t *testing.T) {
	quietLog(t)
	f := &leakyFake{}
	fx := leakFixture(t, f)
	fx.Requests[0].Body["idempotencyKey"] = "caller-chosen-key"

	if problems := kittest.CheckNoSecretLeak(fx); len(problems) != 0 {
		t.Fatalf("problems = %q, want none for a clean handler", problems)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.rejected) != 0 {
		t.Errorf("the fake refused requests the harness sent: %q", f.rejected)
	}
	if len(f.accepted) != len(fx.Requests) {
		t.Fatalf("the fake accepted %d signed /hook requests, want %d", len(f.accepted), len(fx.Requests))
	}
	if got := f.accepted[0].IdempotencyKey; got != "caller-chosen-key" {
		t.Errorf("first request idempotencyKey = %q, want the body's own caller-chosen-key kept", got)
	}
	if f.accepted[1].IdempotencyKey == "" || f.accepted[1].IdempotencyKey == f.accepted[0].IdempotencyKey {
		t.Errorf("second request idempotencyKey = %q, want a fresh one", f.accepted[1].IdempotencyKey)
	}
	if f.accepted[0].Nonce == f.accepted[1].Nonce {
		t.Errorf("both requests carried nonce %q, want a fresh nonce per request", f.accepted[0].Nonce)
	}
	if got := f.accepted[1].Secrets["api"]["token"]; got != leakSecret {
		t.Errorf("second request secrets.api.token was not carried from the fixture body")
	}
}

func TestCheckNoSecretLeak_TeesTheStandardLoggerIntoLogsAndThePreviousWriter(t *testing.T) {
	sink := quietLog(t)
	f := &leakyFake{}

	if problems := kittest.CheckNoSecretLeak(leakFixture(t, f)); len(problems) != 0 {
		t.Fatalf("problems = %q, want none: the clean fake logs only through log.Printf, so /logs has lines only through the tee", problems)
	}
	if !strings.Contains(sink.String(), "fake: subscribe sub-1 handled") {
		t.Errorf("the writer set before CheckNoSecretLeak got %q, want the handler's log.Printf line teed to it", sink.String())
	}
	if lines := f.logs.Snapshot(); len(lines) == 0 {
		t.Error("the LogBuffer handed to Serve holds no lines, want the standard logger teed into it")
	}
}

func TestAssertNoSecretLeak_FailsTBForALeakAndNotForACleanFixture(t *testing.T) {
	quietLog(t)
	leakyFx := leakFixture(t, &leakyFake{leakKnobs: leakKnobs{leakToLogs: true}})
	cleanFx := leakFixture(t, &leakyFake{})

	leaky := runAndCollect(func(tb testing.TB) { kittest.AssertNoSecretLeak(tb, leakyFx) })
	if !leaky.failed {
		t.Error("AssertNoSecretLeak did not fail tb for a handler leaking a seeded secret through /logs")
	}
	logs := strings.Join(leaky.logs, "\n")
	if !strings.Contains(logs, "/logs") {
		t.Errorf("failure %q does not name /logs", logs)
	}
	if strings.Contains(logs, leakSecret) {
		t.Errorf("failure %q carries the seeded secret in the clear", logs)
	}

	clean := runAndCollect(func(tb testing.TB) { kittest.AssertNoSecretLeak(tb, cleanFx) })
	if clean.failed {
		t.Errorf("AssertNoSecretLeak failed tb for a clean handler: %q", clean.logs)
	}
}

func TestSecretLeakSample_ModulePassesItsOwnLeakTest(t *testing.T) {
	gobin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(gobin); err != nil {
		found, lookErr := exec.LookPath("go")
		if lookErr != nil {
			t.Fatalf("no go binary at %s and none on PATH: %v", gobin, errors.Join(err, lookErr))
		}
		gobin = found
	}
	cmd := exec.Command(gobin, "test", "./...")
	cmd.Dir = filepath.Join("testdata", "secretleak-sample")
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test ./... in %s: %v\n%s", cmd.Dir, err, out)
	}
}
