package kittest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/webhook"
)

var verbSeamRequestSeq int64

// VerbSeamsOverHandler drives h as an http.Handler to avoid depending on the package it tests, with a fresh nonce and idempotency key per request so the idempotency cache cannot make a property pass.
func VerbSeamsOverHandler(t *testing.T, h http.Handler, s *Signer, f *FakeResource) VerbSeams {
	t.Helper()
	post := func(operation string) (int, map[string]any) { return postVerb(t, h, s, operation) }

	return VerbSeams{
		Subscribe: func() (int, error) {
			status, doc := post("subscribe")
			return f.Count, errFromStatus(status, doc)
		},

		Unsubscribe: func() (int, error) {
			status, doc := post("unsubscribe")
			return f.Count, errFromStatus(status, doc)
		},

		UnsubscribeWhenAbsent: func() error {
			f.Count = 0
			status, doc := post("unsubscribe")
			return errFromStatus(status, doc)
		},

		Upgrade: func() (bool, error) {
			// A preservation check over an empty output set observes nothing, so seed one marker.
			if len(f.Outputs) == 0 {
				f.Outputs = map[string]string{"verbseam-marker": "present"}
			}
			snapshot := make(map[string]string, len(f.Outputs))
			for k, v := range f.Outputs {
				snapshot[k] = v
			}

			status, doc := post("upgrade")
			if err := errFromStatus(status, doc); err != nil {
				return false, err
			}

			preserved := true
			for k, v := range snapshot {
				if f.Outputs[k] != v {
					preserved = false
				}
			}
			return preserved, nil
		},

		ValidateWhileConverging: func() (bool, bool) {
			savedState, savedErr := f.ObserveState, f.ObserveErr
			unknown := contract.Unknown
			f.ObserveState, f.ObserveErr = &unknown, nil

			status, doc := post("validate")

			f.ObserveState, f.ObserveErr = savedState, savedErr
			pending, _ := doc["pending"].(bool)
			return pending, status >= http.StatusBadRequest
		},

		ValidateWhenDead: func() (bool, bool) {
			savedState, savedErr := f.ObserveState, f.ObserveErr
			f.ObserveErr = errors.New("verb seam: this resource will not recover")

			status, doc := post("validate")

			f.ObserveState, f.ObserveErr = savedState, savedErr
			pending, _ := doc["pending"].(bool)
			return pending, status >= http.StatusBadRequest
		},
	}
}

// RedriveOverHandler drives f's long step through h: a subscribe, FinishBuild, a validate, and the subscribe the engine re-drives.
func RedriveOverHandler(t *testing.T, h http.Handler, s *Signer, f *FakeBuilder) func() (RedriveRun, error) {
	t.Helper()
	post := func(operation string) (map[string]any, error) {
		status, doc := postVerb(t, h, s, operation)
		return doc, errFromStatus(status, doc)
	}
	return func() (RedriveRun, error) {
		first, err := post("subscribe")
		if err != nil {
			return RedriveRun{}, err
		}
		f.FinishBuild()
		validate, err := post("validate")
		if err != nil {
			return RedriveRun{}, err
		}
		builds := f.Builds
		redriven, err := post("subscribe")
		if err != nil {
			return RedriveRun{}, err
		}
		return RedriveRun{
			FirstPending: answered(first, "pending"),
			Redrive:      answered(validate, "pending") && answered(validate, "redrive"),
			Settled:      !answered(redriven, "pending"),
			Changed:      f.Builds - builds,
		}, nil
	}
}

func answered(doc map[string]any, flag string) bool {
	set, _ := doc[flag].(bool)
	return set
}

func postVerb(t *testing.T, h http.Handler, s *Signer, operation string) (int, map[string]any) {
	t.Helper()
	n := atomic.AddInt64(&verbSeamRequestSeq, 1)
	tok, err := webhook.MintOBO(s.Private, s.Kid, webhook.OBOClaims{
		OrgID:          "org-verbseam",
		SubscriptionID: "sub-verbseam",
	}, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("minting an OBO token for a verb seam request: %v", err)
	}
	body := Envelope(fmt.Sprintf("verbseam-nonce-%d", n), time.Now(), map[string]any{
		"operation":      operation,
		"subscriptionId": "sub-verbseam",
		"blockId":        "block-verbseam",
		"orgId":          "org-verbseam",
		"idempotencyKey": fmt.Sprintf("verbseam-idem-%d", n),
		"obo":            tok,
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, s.HookRequest(t, "/hook", body))
	var doc map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &doc)
	return w.Code, doc
}

func errFromStatus(status int, doc map[string]any) error {
	if status < http.StatusBadRequest {
		return nil
	}
	msg, _ := doc["error"].(string)
	if msg == "" {
		msg = fmt.Sprintf("status %d", status)
	}
	return fmt.Errorf("verb seam: %s", msg)
}
