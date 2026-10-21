// A panic is a fault in the block, not a refusal, so it answers HTTP 500 with a fixed error, is never cached, and leaks no secret to the answer or the log.
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

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/handler"
	"github.com/mitstack-eu/operator-kit/v3/kittest"
)

const panicSecret = "top-secret-panic-db-password"

const panicMessage = "backend exploded mid-converge"

type panickingResource struct {
	mu               sync.Mutex
	calls            int
	entered, release chan struct{}
	value            any
}

func (r *panickingResource) panics() {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	if r.entered != nil {
		r.entered <- struct{}{}
	}
	if r.release != nil {
		<-r.release
	}
	if r.value != nil {
		panic(r.value)
	}
	panic(fmt.Sprintf("%s using credential %s", panicMessage, panicSecret))
}

func (r *panickingResource) Ensure(context.Context, contract.Desired) error { r.panics(); return nil }

func (r *panickingResource) Observe(context.Context, contract.Desired) (contract.Observation, error) {
	r.panics()
	return contract.Observation{}, nil
}

func (r *panickingResource) Delete(context.Context, contract.Desired) (map[string]string, error) {
	r.panics()
	return nil, nil
}

func (r *panickingResource) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// servedResult's escaped holds the panic value when ServeHTTP panicked instead of answering.
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

func panicRequest(x *harness, extra map[string]any) *http.Request {
	x.t.Helper()
	fields := map[string]any{"blockId": "block-panicky", "subscriptionId": "sub-panicky", "secrets": map[string]any{"db": map[string]any{"password": panicSecret}}}
	for k, v := range extra {
		fields[k] = v
	}
	return x.request(fields)
}

func assertPanicAnswer(t *testing.T, who string, res servedResult) contract.Response {
	t.Helper()
	if res.escaped != nil {
		t.Fatalf("%s: the Resource's panic escaped ServeHTTP instead of being answered: %v", who, res.escaped)
	}
	if res.status != http.StatusInternalServerError {
		t.Fatalf("%s: status = %d, want 500 for a panicking Resource, body: %s", who, res.status, res.body)
	}
	assertConforms(t, res.body)
	resp := decodeResponse(t, res.body)
	if !strings.Contains(strings.ToLower(resp.Error), "panic") {
		t.Errorf("%s: error = %q, want a message saying the block panicked", who, resp.Error)
	}
	if class := handler.RefusedClass(resp.StatusDetail); class != "" {
		t.Errorf("%s: statusDetail carries refusal class %q, want none: a panic is a fault, not a refusal", who, class)
	}
	kittest.AssertNoSecret(t, who+" response body", string(res.body), panicSecret)
	return resp
}

func TestServe_APanickingResourceAnswers500WithABody(t *testing.T) {
	for _, op := range []string{"subscribe", "upgrade", "validate", "unsubscribe"} {
		for _, key := range []string{"", "idem-panic-" + op} {
			t.Run(fmt.Sprintf("%s with idempotency key %q", op, key), func(t *testing.T) {
				x := newHarness(t, &panickingResource{})

				res := serveRecovering(x.h, panicRequest(x, map[string]any{"operation": op, "idempotencyKey": key}))

				assertPanicAnswer(t, "calling delivery", res)
			})
		}
	}
}

func TestServe_APanicIsNotCachedSoARetryReachesTheResourceAgain(t *testing.T) {
	r := &panickingResource{}
	x := newHarness(t, r)
	extra := map[string]any{"idempotencyKey": "idem-panic-retry"}

	assertPanicAnswer(t, "first delivery", serveRecovering(x.h, panicRequest(x, extra)))
	assertPanicAnswer(t, "retry", serveRecovering(x.h, panicRequest(x, extra)))

	if got := r.count(); got != 2 {
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
			if strings.Contains(g, "operator-kit/v3/handler.(*idempotencyCache).") && strings.Contains(g, "sync.(*WaitGroup).Wait") &&
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

// Every waiter on a panicking call is released, and fails through its own timeout rather than hanging.
func TestServe_AWaiterOnAPanickingCallIsReleasedWithTheSameAnswer(t *testing.T) {
	r := &panickingResource{entered: make(chan struct{}, 8), release: make(chan struct{})}
	x := newHarness(t, r)
	extra := map[string]any{"idempotencyKey": "idem-panic-waiter"}
	reqA, reqB := panicRequest(x, extra), panicRequest(x, extra)
	firstCh, waiterCh := make(chan servedResult, 1), make(chan servedResult, 1)
	go func() { firstCh <- serveRecovering(x.h, reqA) }()
	select {
	case <-r.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first delivery never reached Ensure")
	}
	go func() { waiterCh <- serveRecovering(x.h, reqB) }()
	waitForSingleFlightWaiters(t, 1, 5*time.Second)

	close(r.release)
	var first, waiter servedResult
	timeout := time.After(5 * time.Second)
	for got := 0; got < 2; got++ {
		select {
		case first = <-firstCh:
		case waiter = <-waiterCh:
		case <-timeout:
			t.Fatalf("a delivery was left waiting after the Resource panicked (%d of 2 returned)", got)
		}
	}

	if calls := r.count(); calls != 1 {
		t.Fatalf("Ensure called %d times, want 1: the second delivery must have waited on the first, not run the verb", calls)
	}
	if waiter.escaped == nil && waiter.status == 0 {
		t.Fatalf("waiter answered status 0, the zero value the in-flight call held when the Resource panicked")
	}
	assertPanicAnswer(t, "waiter", waiter)
	assertPanicAnswer(t, "calling delivery", first)
	if waiter.status != first.status || !bytes.Equal(waiter.body, first.body) {
		t.Errorf("waiter answered %d %s, want the calling delivery's exact %d %s", waiter.status, waiter.body, first.status, first.body)
	}
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

// The platform stores the response body, so the answer is a fixed message whatever the panic value, and the log keeps only the type of a value that is not a string.
func TestServe_APanicIsAnsweredAndLoggedWithoutTheSecret(t *testing.T) {
	rendered := decimalBytes(panicSecret, 5)
	cases := []struct {
		name   string
		value  any
		extra  map[string]any
		logged string
	}{
		{"a string panic value", nil, map[string]any{"idempotencyKey": "idem-panic-log"}, panicMessage},
		{"a value that is not a string", secretHolder{Pw: []byte(panicSecret)}, nil, "secretHolder"},
	}
	errs := map[string]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logBuf := captureLog(t)
			x := newHarness(t, &panickingResource{value: tc.value})

			res := serveRecovering(x.h, panicRequest(x, tc.extra))

			resp := assertPanicAnswer(t, "calling delivery", res)
			errs[tc.name] = resp.Error
			if !strings.Contains(resp.Error, "subscribe") {
				t.Errorf("error = %q, want it to name the verb subscribe", resp.Error)
			}
			logged := logBuf.String()
			for what, haystack := range map[string]string{"response body": string(res.body), "log output": logged} {
				kittest.AssertNoSecret(t, what, haystack, panicSecret, rendered)
			}
			for _, want := range []string{"panic", "block-panicky", "sub-panicky", "subscribe", tc.logged} {
				if !strings.Contains(strings.ToLower(logged), strings.ToLower(want)) {
					t.Errorf("log does not carry %q, want it to identify the panicking block; log:\n%s", want, logged)
				}
			}
		})
	}
	if errs[cases[0].name] != errs[cases[1].name] {
		t.Errorf("error differs by panic value: %q vs %q, want one fixed message per verb", errs[cases[0].name], errs[cases[1].name])
	}
}
