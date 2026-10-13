package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/events"
)

// Only Phase and Health decide a deploy.
type Notification struct {
	App          string `json:"app"`
	Project      string `json:"project"`
	Sync         string `json:"sync"`
	Revision     string `json:"revision"`
	Health       string `json:"health"`
	Phase        string `json:"phase"`
	Message      string `json:"message"`
	ReconciledAt string `json:"reconciledAt"`
}

type Verdict string

const (
	VerdictPending   Verdict = "pending"
	VerdictSucceeded Verdict = "succeeded"
	VerdictFailed    Verdict = "failed"
)

// Phase Succeeded with health Healthy succeeds, phase Error or Failed or health Degraded fails, and anything else is pending.
func Judge(n Notification, revision string) Verdict {
	if revision == "" || n.Revision != revision {
		return VerdictPending
	}
	switch {
	case n.Phase == "Succeeded" && n.Health == "Healthy":
		return VerdictSucceeded
	case n.Phase == "Error" || n.Phase == "Failed" || n.Health == "Degraded":
		return VerdictFailed
	default:
		return VerdictPending
	}
}

type Notifications interface {
	// Revision "" asks for the newest notification at any revision.
	Latest(ctx context.Context, app, revision string) (Notification, bool, error)
}

type Outcome struct {
	Verdict      Verdict
	Notification Notification
}

// An empty revision is an error.
func Check(ctx context.Context, src Notifications, app, revision string) (Outcome, error) {
	pending := Outcome{Verdict: VerdictPending}
	if revision == "" {
		return pending, errors.New("gitops: no revision to judge")
	}
	n, ok, err := src.Latest(ctx, app, revision)
	if err != nil {
		return pending, err
	}
	if !ok {
		return pending, nil
	}
	return Outcome{Verdict: Judge(n, revision), Notification: n}, nil
}

// A failed deploy is an outcome, not an error.
func Wait(ctx context.Context, src Notifications, app, revision string, poll time.Duration) (Outcome, error) {
	if poll <= 0 {
		return Outcome{Verdict: VerdictPending}, errors.New("gitops: the poll interval must be positive")
	}
	last := Outcome{Verdict: VerdictPending}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-timer.C:
		}
		out, err := Check(ctx, src, app, revision)
		if err != nil || out.Verdict != VerdictPending {
			return out, err
		}
		last = out
		timer.Reset(poll)
	}
}

const notificationsPath = "/v1/gitops-notifications"

const operatorEventsPath = "/v1/webhooks/operator-events"

const maxNotificationBody = 64 << 10

var notificationsClient = &http.Client{Timeout: 10 * time.Second}

// A nil HTTPClient uses a short-timeout default, and no error it returns carries Token.
type PlatformNotifications struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// A 404 is none yet, and any status but 200 an error.
func (p PlatformNotifications) Latest(ctx context.Context, app, revision string) (Notification, bool, error) {
	query := url.Values{"app": {app}}
	if revision != "" {
		query.Set("revision", revision)
	}
	target := strings.TrimSuffix(p.BaseURL, "/") + notificationsPath + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Notification{}, false, fmt.Errorf("gitops: building the notification request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	client := p.HTTPClient
	if client == nil {
		client = notificationsClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Notification{}, false, fmt.Errorf("gitops: reading the notification for %s: %w", app, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxNotificationBody))
	if err != nil {
		return Notification{}, false, fmt.Errorf("gitops: reading the notification for %s: %w", app, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		var n Notification
		if err := json.Unmarshal(body, &n); err != nil {
			return Notification{}, false, fmt.Errorf("gitops: decoding the notification for %s: %w", app, err)
		}
		return n, true, nil
	case http.StatusNotFound:
		return Notification{}, false, nil
	default:
		return Notification{}, false, fmt.Errorf("gitops: the notification for %s answered status %d: %s",
			app, resp.StatusCode, p.scrub(strings.TrimSpace(string(body))))
	}
}

func (p PlatformNotifications) scrub(text string) string {
	if p.Token == "" {
		return text
	}
	return strings.ReplaceAll(text, p.Token, "[REDACTED]")
}

// The events callback URL on ctx must end in /v1/webhooks/operator-events.
func NotificationsFromContext(ctx context.Context) (PlatformNotifications, error) {
	env, ok := events.EnvelopeFrom(ctx)
	if !ok || env.Callback == nil {
		return PlatformNotifications{}, errors.New("gitops: the request carries no events callback to reach the platform through")
	}
	base, found := strings.CutSuffix(env.Callback.URL, operatorEventsPath)
	if !found {
		return PlatformNotifications{}, fmt.Errorf("gitops: the events callback URL does not end in %s", operatorEventsPath)
	}
	return PlatformNotifications{BaseURL: base, Token: env.Callback.Token}, nil
}
