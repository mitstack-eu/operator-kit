package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// A *Refused, wrapped or not, is 400 with "refused:<class>: <msg>", a plain error is 500, and an empty Class renders as "unclassified".
func TestServe_ClassifiesEnsureErrors(t *testing.T) {
	cases := []struct {
		name          string
		err           error
		wantStatus    int
		wantDetailHas string
		wantClass     string
		refusedPrefix bool
	}{
		{
			name:          "refused",
			err:           handler.Refuse("dependents-remain", "two apps still bind this database"),
			wantStatus:    http.StatusBadRequest,
			wantDetailHas: "refused:dependents-remain: two apps still bind this database",
			wantClass:     "dependents-remain",
			refusedPrefix: true,
		},
		{
			name:          "wrapped refused",
			err:           fmt.Errorf("ensure: %w", handler.Refuse("quota-exceeded", "org quota exhausted")),
			wantStatus:    http.StatusBadRequest,
			wantDetailHas: "refused:quota-exceeded: org quota exhausted",
			wantClass:     "quota-exceeded",
			refusedPrefix: true,
		},
		{
			name:          "plain error",
			err:           errors.New("boom: backend unavailable"),
			wantStatus:    http.StatusInternalServerError,
			wantDetailHas: "",
			wantClass:     "",
			refusedPrefix: false,
		},
		{
			name:          "refused with empty class",
			err:           handler.Refuse("", "no class given"),
			wantStatus:    http.StatusBadRequest,
			wantDetailHas: "refused:unclassified: no class given",
			wantClass:     "unclassified",
			refusedPrefix: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			f := &kittest.FakeResource{EnsureErr: c.err}
			h := newHandler(t, f, s)

			status, raw := postHook(t, h, s, envelope(t, s, "nonce-err-"+c.name, nil))
			if status != c.wantStatus {
				t.Fatalf("status = %d, want %d, body: %s", status, c.wantStatus, raw)
			}
			assertConforms(t, raw)

			resp := decodeResponse(t, raw)
			if resp.Error == "" {
				t.Error("Error is empty, want the failure message")
			}
			if c.refusedPrefix {
				if !strings.Contains(resp.StatusDetail, c.wantDetailHas) {
					t.Errorf("StatusDetail = %q, want it to contain %q", resp.StatusDetail, c.wantDetailHas)
				}
			} else if strings.HasPrefix(resp.StatusDetail, "refused:") {
				t.Errorf("StatusDetail = %q, must not carry a refused: prefix for a plain error", resp.StatusDetail)
			}
			if got := handler.RefusedClass(resp.StatusDetail); got != c.wantClass {
				t.Errorf("RefusedClass(%q) = %q, want %q", resp.StatusDetail, got, c.wantClass)
			}
		})
	}
}

// TestServe_ClassifiesAHookError proves the classification applies to a
// hook's own error, not just Ensure/Observe/Delete, and that the Ensure it
// would have guarded never runs.
func TestServe_ClassifiesAHookError(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{}
	h := newHandler(t, f, s, func(c *handler.Config) {
		c.Manifest = manifestDeclaring(handler.HookBeforeEnsure)
		c.Hooks = map[string]handler.Hook{
			handler.HookBeforeEnsure: func(context.Context, contract.Desired) error {
				return handler.Refuse("policy-blocked", "org policy forbids this block")
			},
		}
	})

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-hook-err", nil))
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a refused hook, body: %s", status, raw)
	}
	resp := decodeResponse(t, raw)
	if got := handler.RefusedClass(resp.StatusDetail); got != "policy-blocked" {
		t.Errorf("RefusedClass(%q) = %q, want %q", resp.StatusDetail, got, "policy-blocked")
	}
	if f.EnsureCalls != 0 {
		t.Errorf("EnsureCalls = %d, want 0 — Ensure must never run after its guarding hook refuses", f.EnsureCalls)
	}
}

func TestRefusedClass_RoundTripsTheClass(t *testing.T) {
	got := handler.RefusedClass("refused:dependents-remain: two apps still bind this database")
	if got != "dependents-remain" {
		t.Errorf("RefusedClass = %q, want %q", got, "dependents-remain")
	}
}

func TestRefusedClass_EmptyForNonRefusalDetail(t *testing.T) {
	for _, detail := range []string{"", "some other detail", "refusedwithnocolon", "REFUSED:upper-case"} {
		if got := handler.RefusedClass(detail); got != "" {
			t.Errorf("RefusedClass(%q) = %q, want empty for a non-refusal detail", detail, got)
		}
	}
}

// The assertions run on the raw response bytes the handler wrote, not a re-encoded struct.
func TestServe_RefusedTeardownRawResponseBytes(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	f := &kittest.FakeResource{Count: 1, DeleteErr: handler.Refuse("dependents-remain", "two apps still bind this database")}
	h := newHandler(t, f, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-refused-teardown", map[string]any{"operation": "unsubscribe"}))
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a refused teardown, body: %s", status, raw)
	}
	assertConforms(t, raw)

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("response is not valid JSON: %v\nbody: %s", err, raw)
	}
	statusDetail, _ := doc["statusDetail"].(string)
	if statusDetail == "" {
		t.Fatalf("decoded statusDetail is empty; body: %s", raw)
	}
	if got := handler.RefusedClass(statusDetail); got != "dependents-remain" {
		t.Errorf("RefusedClass(decoded statusDetail %q) = %q, want %q", statusDetail, got, "dependents-remain")
	}
}
