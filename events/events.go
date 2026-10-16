// Package events, an optional helper, lets an operator push advisory lifecycle events back to mitstack's ingest endpoint, as a no-op when the envelope carries no callback.
package events

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"time"
)

// Callback carries the ingest URL and bearer token mitstack injects into the signed webhook envelope.
type Callback struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// Envelope is the subset of the operator's decoded request the emit helpers need, built by the operator so the kit never knows its request type.
type Envelope struct {
	SubscriptionID string
	Operation      string
	IdempotencyKey string
	Callback       *Callback
}

type envelopeKey struct{}

// WithEnvelope stashes the events envelope on ctx so downstream code can emit without threading the request.
func WithEnvelope(ctx context.Context, env Envelope) context.Context {
	return context.WithValue(ctx, envelopeKey{}, &env)
}

// EnvelopeFrom returns the envelope WithEnvelope stashed on ctx, which may still hold a nil Callback, and false when there is none.
func EnvelopeFrom(ctx context.Context) (Envelope, bool) {
	env, ok := ctx.Value(envelopeKey{}).(*Envelope)
	if !ok || env == nil {
		return Envelope{}, false
	}
	return *env, true
}

// Short timeout so a slow or dead ingest endpoint never blocks lifecycle handling for long.
var httpClient = &http.Client{Timeout: 10 * time.Second}

type operatorEvent struct {
	SubscriptionID string `json:"subscriptionId"`
	Token          string `json:"token"`
	Operation      string `json:"operation"`
	Status         string `json:"status"`
	Detail         string `json:"detail"`
	IdempotencyKey string `json:"idempotencyKey"`
}

// EmitProgress reports a "progress" event with the given detail.
func EmitProgress(ctx context.Context, detail string) error { return emit(ctx, "progress", detail) }

// EmitCompleted reports a "completed" event with no detail.
func EmitCompleted(ctx context.Context) error { return emit(ctx, "completed", "") }

// EmitFailed reports a "failed" event carrying the given failure detail.
func EmitFailed(ctx context.Context, detail string) error { return emit(ctx, "failed", detail) }

func emit(ctx context.Context, status, detail string) error {
	env, ok := ctx.Value(envelopeKey{}).(*Envelope)
	if !ok || env == nil || env.Callback == nil {
		return nil
	}

	ev := operatorEvent{
		SubscriptionID: env.SubscriptionID,
		Token:          env.Callback.Token,
		Operation:      env.Operation,
		Status:         status,
		Detail:         ScrubDetail(detail),
		IdempotencyKey: env.IdempotencyKey,
	}

	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.Callback.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

var secretLikeKeyPattern = regexp.MustCompile(`(?i)(token|secret|password|passwd|apikey|api_key|credential|private_key|privatekey)\s*[:=]\s*\S+`)

var assignSepPattern = regexp.MustCompile(`[:=]`)

// ScrubDetail redacts secret-shaped `key=value` and `key: value` assignments from a detail before it leaves the process.
func ScrubDetail(detail string) string {
	return secretLikeKeyPattern.ReplaceAllStringFunc(detail, func(match string) string {
		idx := assignSepPattern.FindStringIndex(match)
		if idx == nil {
			return match
		}
		return match[:idx[1]] + " [REDACTED]"
	})
}
