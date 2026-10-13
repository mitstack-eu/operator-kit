package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// TestServe_UnknownPathIs404 pins that Serve routes only POST /hook and
// GET /logs — anything else is 404.
func TestServe_UnknownPathIs404(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown path", w.Code)
	}
	zeroCalls(t, f)
}

// TestServe_WrongMethodOnHookIs405 pins that a known path with the wrong
// method is 405, not 404 or a silent 200.
func TestServe_WrongMethodOnHookIs405(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/hook", nil))

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405 for GET /hook", w.Code)
	}
	zeroCalls(t, f)
}

// TestServe_WrongMethodOnLogsIs405 mirrors the above for /logs.
func TestServe_WrongMethodOnLogsIs405(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/logs", nil))

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405 for POST /logs", w.Code)
	}
}

// TestServeHTTP_DelegatesToServe pins that Handler.ServeHTTP is not a
// separate implementation: it must behave exactly as Serve for the same
// request.
func TestServeHTTP_DelegatesToServe(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s)

	body := envelope(t, s, "nonce-servehttp", nil)
	status, raw := postHook(t, h, s, body)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", status, raw)
	}
	assertConforms(t, raw)
}
