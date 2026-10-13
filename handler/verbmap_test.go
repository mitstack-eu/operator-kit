package handler_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// callSnapshot is one hook invocation, paired with the fake's own call
// counters at that instant. Verb dispatch is single-threaded and
// synchronous, so this snapshot is an accurate position in the call
// sequence, not just the hook's own name.
type callSnapshot struct {
	hook                                   string
	ensureCalls, observeCalls, deleteCalls int
}

func hookNames(seq []callSnapshot) []string {
	out := make([]string, len(seq))
	for i, s := range seq {
		out[i] = s.hook
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// newRecordingHandler builds a Handler over f with every hook point declared
// in the manifest and registered, each recording a callSnapshot when it
// runs. A verb that reaches a hook it should not proves itself through the
// returned sequence.
func newRecordingHandler(t *testing.T, f *kittest.FakeResource) (*handler.Handler, *kittest.Signer, *[]callSnapshot) {
	t.Helper()
	s := kittest.NewSigner(t, "kid-1", 7)
	var seq []callSnapshot
	record := func(name string) handler.Hook {
		return func(context.Context, contract.Desired) error {
			seq = append(seq, callSnapshot{name, f.EnsureCalls, f.ObserveCalls, f.DeleteCalls})
			return nil
		}
	}
	h := newHandler(t, f, s, func(c *handler.Config) {
		c.Manifest = manifestDeclaring(handler.HookBeforeEnsure, handler.HookAfterEnsure, handler.HookBeforeDelete, handler.HookAfterDelete)
		c.Hooks = map[string]handler.Hook{
			handler.HookBeforeEnsure: record(handler.HookBeforeEnsure),
			handler.HookAfterEnsure:  record(handler.HookAfterEnsure),
			handler.HookBeforeDelete: record(handler.HookBeforeDelete),
			handler.HookAfterDelete:  record(handler.HookAfterDelete),
		}
	})
	return h, s, &seq
}

// TestServe_MapsEachVerbToItsCallSequence is the one table test the CONTRACT
// asks for: one row per verb, asserting the exact hook/Resource call
// sequence and call counts the fake recorded.
func TestServe_MapsEachVerbToItsCallSequence(t *testing.T) {
	t.Run("subscribe", func(t *testing.T) {
		f := &kittest.FakeResource{}
		h, s, seq := newRecordingHandler(t, f)

		status, raw := postHook(t, h, s, envelope(t, s, "nonce-sub", map[string]any{"operation": "subscribe"}))
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200, body: %s", status, raw)
		}
		assertConforms(t, raw)

		want := []string{handler.HookBeforeEnsure, handler.HookAfterEnsure}
		if got := hookNames(*seq); !equalStrings(got, want) {
			t.Errorf("hook sequence = %v, want %v", got, want)
		}
		if (*seq)[0].ensureCalls != 0 {
			t.Errorf("beforeEnsure saw EnsureCalls=%d, want 0 (Ensure not yet called)", (*seq)[0].ensureCalls)
		}
		if (*seq)[1].ensureCalls != 1 {
			t.Errorf("afterEnsure saw EnsureCalls=%d, want 1 (Ensure must run between the two hooks)", (*seq)[1].ensureCalls)
		}
		if f.EnsureCalls != 1 || f.DeleteCalls != 0 {
			t.Errorf("EnsureCalls=%d DeleteCalls=%d, want 1 and 0", f.EnsureCalls, f.DeleteCalls)
		}
		if f.ObserveCalls != 1 {
			t.Errorf("ObserveCalls=%d, want 1 — settle must stop as soon as Present is observed", f.ObserveCalls)
		}
	})

	t.Run("upgrade", func(t *testing.T) {
		f := &kittest.FakeResource{}
		h, s, seq := newRecordingHandler(t, f)

		status, raw := postHook(t, h, s, envelope(t, s, "nonce-upg", map[string]any{"operation": "upgrade"}))
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200, body: %s", status, raw)
		}
		assertConforms(t, raw)

		want := []string{handler.HookBeforeEnsure, handler.HookAfterEnsure}
		if got := hookNames(*seq); !equalStrings(got, want) {
			t.Errorf("hook sequence = %v, want %v (upgrade is the same call sequence as subscribe)", got, want)
		}
		if f.EnsureCalls != 1 || f.DeleteCalls != 0 {
			t.Errorf("EnsureCalls=%d DeleteCalls=%d, want 1 and 0", f.EnsureCalls, f.DeleteCalls)
		}
		if f.ObserveCalls != 1 {
			t.Errorf("ObserveCalls=%d, want 1", f.ObserveCalls)
		}
	})

	t.Run("validate", func(t *testing.T) {
		f := &kittest.FakeResource{Count: 1} // already provisioned
		h, s, seq := newRecordingHandler(t, f)

		status, raw := postHook(t, h, s, envelope(t, s, "nonce-val", map[string]any{"operation": "validate"}))
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200, body: %s", status, raw)
		}
		assertConforms(t, raw)

		if len(*seq) != 0 {
			t.Errorf("validate ran hooks %v, want none — validate calls Observe only", hookNames(*seq))
		}
		if f.EnsureCalls != 0 || f.DeleteCalls != 0 {
			t.Errorf("EnsureCalls=%d DeleteCalls=%d, want 0 and 0 for validate", f.EnsureCalls, f.DeleteCalls)
		}
		if f.ObserveCalls != 1 {
			t.Errorf("ObserveCalls=%d, want exactly 1 — validate never polls", f.ObserveCalls)
		}
	})

	t.Run("unsubscribe", func(t *testing.T) {
		f := &kittest.FakeResource{Count: 1} // already provisioned
		h, s, seq := newRecordingHandler(t, f)

		status, raw := postHook(t, h, s, envelope(t, s, "nonce-uns", map[string]any{"operation": "unsubscribe"}))
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200, body: %s", status, raw)
		}
		assertConforms(t, raw)

		want := []string{handler.HookBeforeDelete, handler.HookAfterDelete}
		if got := hookNames(*seq); !equalStrings(got, want) {
			t.Errorf("hook sequence = %v, want %v", got, want)
		}
		if (*seq)[0].deleteCalls != 0 {
			t.Errorf("beforeDelete saw DeleteCalls=%d, want 0 (Delete not yet called)", (*seq)[0].deleteCalls)
		}
		if (*seq)[1].deleteCalls != 1 {
			t.Errorf("afterDelete saw DeleteCalls=%d, want 1 (Delete must run between the two hooks)", (*seq)[1].deleteCalls)
		}
		if f.DeleteCalls != 1 || f.EnsureCalls != 0 {
			t.Errorf("DeleteCalls=%d EnsureCalls=%d, want 1 and 0", f.DeleteCalls, f.EnsureCalls)
		}
		if f.ObserveCalls != 1 {
			t.Errorf("ObserveCalls=%d, want 1 — settle must stop as soon as Absent is observed", f.ObserveCalls)
		}
	})
}
