package webhook_test

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/kittest"
	"github.com/mitstack-eu/operator-kit/v3/webhook"
)

func TestLogBuffer_KeepsOnlyTheMostRecentLines(t *testing.T) {
	b := webhook.NewLogBuffer(3)
	for _, l := range []string{"one", "two", "three", "four"} {
		b.Append(l)
	}

	got := b.Snapshot()
	if len(got) != 3 || got[0] != "two" || got[2] != "four" {
		t.Errorf("snapshot = %v, want the last three lines oldest-first", got)
	}
}

func TestLogBuffer_SnapshotIsACopy(t *testing.T) {
	b := webhook.NewLogBuffer(0)
	b.Append("line")

	snap := b.Snapshot()
	snap[0] = "mutated"

	if b.Snapshot()[0] != "line" {
		t.Error("mutating a snapshot must not corrupt the buffer")
	}
}

func TestLogBuffer_SnapshotIsNeverNil(t *testing.T) {
	if webhook.NewLogBuffer(0).Snapshot() == nil {
		t.Error("an empty buffer must snapshot to an empty slice, not nil (it is marshalled as [])")
	}
}

func TestLogBuffer_WriterSplitsOnNewlines(t *testing.T) {
	b := webhook.NewLogBuffer(0)
	lg := log.New(b.Writer(), "", 0)

	lg.Print("first")
	lg.Print("second")

	if got := b.Snapshot(); len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Errorf("snapshot = %v, want one entry per log line", got)
	}
}

func TestLogBuffer_Clear(t *testing.T) {
	b := webhook.NewLogBuffer(0)
	b.Append("x")
	b.Clear()

	if len(b.Snapshot()) != 0 {
		t.Error("Clear must empty the buffer")
	}
}

func TestServeLogs_ReturnsBufferedLinesToASignedGET(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 5)
	d := depsFor(s, s.JWKSServer(t))
	b := webhook.NewLogBuffer(0)
	b.Append("provisioning started")

	w := httptest.NewRecorder()
	webhook.ServeLogs(d, b, w, s.LogsRequest(t, "/logs", time.Now()))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body)
	}
	var got struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Lines) != 1 || got.Lines[0] != "provisioning started" {
		t.Errorf("lines = %v, want the buffered line", got.Lines)
	}
}

func TestServeLogs_RefusesAnUnsignedGET(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 5)
	d := depsFor(s, s.JWKSServer(t))
	b := webhook.NewLogBuffer(0)
	b.Append("provisioning started")

	w := httptest.NewRecorder()
	webhook.ServeLogs(d, b, w, httptest.NewRequest(http.MethodGet, "/logs", nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if w.Body.Len() > 0 && json.Valid(w.Body.Bytes()) {
		var got struct {
			Lines []string `json:"lines"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		if len(got.Lines) > 0 {
			t.Error("an unauthenticated /logs must not disclose buffered lines")
		}
	}
}
