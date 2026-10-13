package handler_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// A panic is a fault in the block, not a refusal, so it answers HTTP 500 with an "error" and no "refused:<class>" statusDetail.

const panicSecret = "top-secret-panic-db-password"

const panicMessage = "backend exploded mid-converge"

type panickingResource struct {
	mu          sync.Mutex
	ensureCalls int
	observeCall int
	deleteCalls int
	entered     chan struct{}
	release     chan struct{}
	value       any
}

func (r *panickingResource) panicValue() any {
	if r.value != nil {
		return r.value
	}
	return fmt.Sprintf("%s using credential %s", panicMessage, panicSecret)
}

func (r *panickingResource) Ensure(ctx context.Context, d contract.Desired) error {
	r.mu.Lock()
	r.ensureCalls++
	r.mu.Unlock()
	if r.entered != nil {
		r.entered <- struct{}{}
	}
	if r.release != nil {
		<-r.release
	}
	panic(r.panicValue())
}

func (r *panickingResource) Observe(ctx context.Context, d contract.Desired) (contract.Observation, error) {
	r.mu.Lock()
	r.observeCall++
	r.mu.Unlock()
	panic(r.panicValue())
}

func (r *panickingResource) Delete(ctx context.Context, d contract.Desired) (map[string]string, error) {
	r.mu.Lock()
	r.deleteCalls++
	r.mu.Unlock()
	panic(r.panicValue())
}

func (r *panickingResource) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ensureCalls
}

var _ handler.Resource = (*panickingResource)(nil)

// servedResult's escaped holds the panic value when ServeHTTP panicked instead of answering, which an answered request never sets.
type servedResult struct {
	status  int
	body    []byte
	escaped any
}

// serveRecovering touches no *testing.T, so it is safe to call from a goroutine.
func serveRecovering(h http.Handler, req *http.Request) (res servedResult) {
	w := httptest.NewRecorder()
	defer func() {
		if p := recover(); p != nil {
			res = servedResult{status: w.Code, body: w.Body.Bytes(), escaped: p}
		}
	}()
	h.ServeHTTP(w, req)
	return servedResult{status: w.Code, body: w.Body.Bytes()}
}

func panicEnvelope(t *testing.T, s *kittest.Signer, nonce string, extra map[string]any) *http.Request {
	t.Helper()
	fields := map[string]any{
		"blockId":        "block-panicky",
		"subscriptionId": "sub-panicky",
		"secrets":        map[string]any{"db": map[string]any{"password": panicSecret}},
	}
	for k, v := range extra {
		fields[k] = v
	}
	return s.HookRequest(t, "/hook", envelope(t, s, nonce, fields))
}

func assertPanicAnswer(t *testing.T, who string, res servedResult) {
	t.Helper()
	if res.escaped != nil {
		t.Fatalf("%s: the Resource's panic escaped ServeHTTP instead of being answered: %v", who, res.escaped)
	}
	if res.status != http.StatusInternalServerError {
		t.Fatalf("%s: status = %d, want 500 for a panicking Resource, body: %s", who, res.status, res.body)
	}
	assertConforms(t, res.body)
	resp := decodeResponse(t, res.body)
	if resp.Error == "" {
		t.Errorf("%s: error is empty, want a message the engine can surface, body: %s", who, res.body)
	}
	if !strings.Contains(strings.ToLower(resp.Error), "panic") {
		t.Errorf("%s: error = %q, want it to say the block panicked", who, resp.Error)
	}
	if class := handler.RefusedClass(resp.StatusDetail); class != "" {
		t.Errorf("%s: statusDetail carries refusal class %q, want none: a panic is a fault, not a refusal", who, class)
	}
	kittest.AssertNoSecret(t, who+" response body", string(res.body), panicSecret)
}

func TestServe_PanickingResourceAnswers500WithABody(t *testing.T) {
	for _, op := range []string{"subscribe", "upgrade", "validate", "unsubscribe"} {
		for _, key := range []string{"", "idem-panic-" + op} {
			name := op + "/no-idempotency-key"
			if key != "" {
				name = op + "/with-idempotency-key"
			}
			t.Run(name, func(t *testing.T) {
				s := kittest.NewSigner(t, "kid-1", 7)
				r := &panickingResource{}
				h := newHandler(t, r, s)

				extra := map[string]any{"operation": op}
				if key != "" {
					extra["idempotencyKey"] = key
				}
				res := serveRecovering(h, panicEnvelope(t, s, "nonce-panic-"+name, extra))
				assertPanicAnswer(t, "calling delivery", res)
			})
		}
	}
}

func TestServe_PanickingResourceIsNotCachedRetryReachesTheResourceAgain(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	r := &panickingResource{}
	h := newHandler(t, r, s)

	extra := map[string]any{"idempotencyKey": "idem-panic-retry"}
	first := serveRecovering(h, panicEnvelope(t, s, "nonce-panic-retry-a", extra))
	assertPanicAnswer(t, "first delivery", first)

	second := serveRecovering(h, panicEnvelope(t, s, "nonce-panic-retry-b", extra))
	assertPanicAnswer(t, "retry", second)
	if got := r.calls(); got != 2 {
		t.Errorf("Ensure called %d times, want 2: a panic's 500 must not be cached under the key", got)
	}
}

// waitForSingleFlightWaiters counts a goroutine only once its stack is parked in sync.(*WaitGroup).Wait, so one that has entered the cache but not yet joined the call is never mistaken for a waiter.
func waitForSingleFlightWaiters(t *testing.T, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		got := 0
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			if strings.Contains(g, "operator-kit/v2/handler.(*idempotencyCache).") &&
				strings.Contains(g, "sync.(*WaitGroup).Wait") &&
				!strings.Contains(g, "(*panickingResource).Ensure") {
				got++
			}
		}
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d delivery(ies) joined the in-flight call as a waiter within %s, want %d", got, timeout, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// overlappingPanic fails through its own timeout rather than hanging if either delivery never returns.
func overlappingPanic(t *testing.T, key string) (first, waiter servedResult, r *panickingResource) {
	t.Helper()
	s := kittest.NewSigner(t, "kid-1", 7)
	r = &panickingResource{entered: make(chan struct{}, 8), release: make(chan struct{})}
	h := newHandler(t, r, s)

	extra := map[string]any{"idempotencyKey": key}
	reqA := panicEnvelope(t, s, "nonce-"+key+"-a", extra)
	reqB := panicEnvelope(t, s, "nonce-"+key+"-b", extra)

	firstCh := make(chan servedResult, 1)
	waiterCh := make(chan servedResult, 1)

	go func() { firstCh <- serveRecovering(h, reqA) }()
	select {
	case <-r.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first delivery never reached Ensure")
	}

	go func() { waiterCh <- serveRecovering(h, reqB) }()
	waitForSingleFlightWaiters(t, 1, 5*time.Second)

	close(r.release)

	timeout := time.After(5 * time.Second)
	for got := 0; got < 2; got++ {
		select {
		case first = <-firstCh:
		case waiter = <-waiterCh:
		case <-timeout:
			t.Fatalf("a delivery was left waiting after the Resource panicked (%d of 2 returned)", got)
		}
	}
	if calls := r.calls(); calls != 1 {
		t.Fatalf("Ensure called %d times, want 1: the second delivery must have waited on the first, not run the verb", calls)
	}
	return first, waiter, r
}

func TestServe_PanickingResourceWaiterOnTheSameKeyGetsTheSameResponse(t *testing.T) {
	first, waiter, _ := overlappingPanic(t, "idem-panic-waiter")

	if waiter.escaped == nil && waiter.status == 0 {
		t.Fatalf("waiter answered status 0, the zero value the in-flight call held when the Resource panicked")
	}
	assertPanicAnswer(t, "waiter", waiter)
	assertPanicAnswer(t, "calling delivery", first)
	if waiter.status != first.status {
		t.Errorf("waiter status = %d, want the calling delivery's %d", waiter.status, first.status)
	}
	if !bytes.Equal(waiter.body, first.body) {
		t.Errorf("waiter body = %s, want the calling delivery's exact body %s", waiter.body, first.body)
	}
}

func TestServe_PanickingResourceReleasesEveryWaiter(t *testing.T) {
	overlappingPanic(t, "idem-panic-liveness")
}

func TestServe_PanickingResourceIsLoggedWithTheBlockAndNoSecret(t *testing.T) {
	logBuf := captureLog(t)

	s := kittest.NewSigner(t, "kid-1", 7)
	r := &panickingResource{}
	h := newHandler(t, r, s)

	res := serveRecovering(h, panicEnvelope(t, s, "nonce-panic-log", map[string]any{"idempotencyKey": "idem-panic-log"}))
	assertPanicAnswer(t, "calling delivery", res)

	logged := logBuf.String()
	for _, want := range []string{"panic", "block-panicky", "sub-panicky", "subscribe", panicMessage} {
		if !strings.Contains(strings.ToLower(logged), strings.ToLower(want)) {
			t.Errorf("log does not carry %q, want it to identify the panicking block; log:\n%s", want, logged)
		}
	}
	kittest.AssertNoSecret(t, "log output", logged, panicSecret)
}

// secretHolder renders the secret as decimal bytes when formatted, which a literal substring scrub cannot find.
type secretHolder struct {
	Pw []byte
}

func decimalBytes(v string, n int) string {
	parts := make([]string, 0, n)
	for _, b := range []byte(v)[:n] {
		parts = append(parts, fmt.Sprint(b))
	}
	return strings.Join(parts, " ")
}

func panicAnswerFor(t *testing.T, nonce string, value any) (servedResult, contract.Response) {
	t.Helper()
	s := kittest.NewSigner(t, "kid-1", 7)
	r := &panickingResource{value: value}
	h := newHandler(t, r, s)
	res := serveRecovering(h, panicEnvelope(t, s, nonce, map[string]any{"operation": "subscribe"}))
	assertPanicAnswer(t, "calling delivery", res)
	return res, decodeResponse(t, res.body)
}

// The platform stores the response body, so the answer must be a fixed message.
func TestServe_PanicAnswerCarriesNothingFromANonStringPanicValue(t *testing.T) {
	res, resp := panicAnswerFor(t, "nonce-panic-holder", secretHolder{Pw: []byte(panicSecret)})

	rendered := decimalBytes(panicSecret, 5)
	if strings.Contains(string(res.body), rendered) {
		t.Errorf("response body carries the secret's decimal byte rendering %q: %s", rendered, res.body)
	}
	kittest.AssertNoSecret(t, "response body", string(res.body), panicSecret)
	if !strings.Contains(resp.Error, "subscribe") {
		t.Errorf("error = %q, want it to name the verb subscribe", resp.Error)
	}
}

func TestServe_PanicAnswerIsTheSameForDifferentPanicValues(t *testing.T) {
	_, fromString := panicAnswerFor(t, "nonce-panic-value-a", "an unrelated failure")
	_, fromHolder := panicAnswerFor(t, "nonce-panic-value-b", secretHolder{Pw: []byte(panicSecret)})

	if fromString.Error != fromHolder.Error {
		t.Errorf("error differs by panic value: %q vs %q, want one fixed message per verb", fromString.Error, fromHolder.Error)
	}
}

func TestServe_PanicLogKeepsOnlyTheTypeOfANonStringPanicValue(t *testing.T) {
	logBuf := captureLog(t)

	panicAnswerFor(t, "nonce-panic-holder-log", secretHolder{Pw: []byte(panicSecret)})

	logged := logBuf.String()
	rendered := decimalBytes(panicSecret, 5)
	if strings.Contains(logged, rendered) {
		t.Errorf("log carries the secret's decimal byte rendering %q; log:\n%s", rendered, logged)
	}
	kittest.AssertNoSecret(t, "log output", logged, panicSecret)
	for _, want := range []string{"panic", "block-panicky", "sub-panicky", "subscribe", "secretHolder"} {
		if !strings.Contains(strings.ToLower(logged), strings.ToLower(want)) {
			t.Errorf("log does not carry %q, want it to identify the block and the panic value's type; log:\n%s", want, logged)
		}
	}
}
