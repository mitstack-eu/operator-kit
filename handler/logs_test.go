package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/webhook"
)

func TestServe_LogsAnswers200WhenSignedAndConfigured(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	buf := webhook.NewLogBuffer(10)
	buf.Append("hello from the operator")
	h := newHandler(t, f, s, func(c *handler.Config) { c.Logs = buf })

	w := httptest.NewRecorder()
	h.ServeHTTP(w, s.LogsRequest(t, "/logs", time.Now()))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	var doc struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("response is not valid JSON: %v\nbody: %s", err, w.Body.String())
	}
	found := false
	for _, l := range doc.Lines {
		if l == "hello from the operator" {
			found = true
		}
	}
	if !found {
		t.Errorf("lines = %v, want the appended log line", doc.Lines)
	}
}

func TestServe_LogsAnswers401WhenUnsigned(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	buf := webhook.NewLogBuffer(10)
	h := newHandler(t, f, s, func(c *handler.Config) { c.Logs = buf })

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/logs", nil))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an unsigned GET /logs", w.Code)
	}
}

func TestServe_LogsAnswers404WhenConfigNil(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s) // Config.Logs left nil

	w := httptest.NewRecorder()
	h.ServeHTTP(w, s.LogsRequest(t, "/logs", time.Now()))

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when Config.Logs is nil", w.Code)
	}
}
