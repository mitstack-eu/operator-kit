package gitops_test

// notification_test.go pins how a deploy is judged from Argo CD's
// notification, the way ADR 0072 decides: the notification whose revision
// equals the commit, with phase Succeeded and health Healthy, is a success;
// phase Error or Failed, or health Degraded, is a failure; anything else,
// a different revision included, is still pending. sync and images are
// never read. It also pins Check and Wait over a Notifications source, which
// keep that exact-revision rule, the platform reader PlatformNotifications,
// which asks for an app's newest notification when handed revision "", and
// NotificationsFromContext, which builds that reader from the events
// callback the handler stashes.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/events"
	"github.com/mitstack-eu/operator-kit/v2/gitops"
	"github.com/mitstack-eu/operator-kit/v2/gitops/gitopstest"
)

// adrBody is the notification body ADR 0072 quotes, verbatim.
const adrBody = `{"app":"app-alpha","project":"app-alpha","sync":"Synced","revision":"9ee9b7e28c000b16a89b7b717e5f3b9fa6fcf611","health":"Healthy","phase":"Succeeded","message":"successfully synced (all tasks run)","images":null,"reconciledAt":"2026-09-28T10:47:23Z"}`

const adrRevision = "9ee9b7e28c000b16a89b7b717e5f3b9fa6fcf611"

func decodeADR(t *testing.T, body string) gitops.Notification {
	t.Helper()
	var n gitops.Notification
	if err := json.Unmarshal([]byte(body), &n); err != nil {
		t.Fatalf("decoding the notification body: %v", err)
	}
	return n
}

func TestNotification_DecodesTheBodyADR0072Quotes(t *testing.T) {
	n := decodeADR(t, adrBody)

	want := gitops.Notification{
		App: "app-alpha", Project: "app-alpha", Sync: "Synced", Revision: adrRevision,
		Health: "Healthy", Phase: "Succeeded", Message: "successfully synced (all tasks run)",
	}
	got := n
	got.ReconciledAt = want.ReconciledAt
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decoded %+v, want %+v", n, want)
	}
	raw, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("re-encoding: %v", err)
	}
	if !strings.Contains(string(raw), `"reconciledAt":"2026-09-28T10:47:23Z"`) {
		t.Errorf("re-encoded %s, want reconciledAt carried through as 2026-09-28T10:47:23Z", raw)
	}
}

func TestJudge_TheADRBodyForItsOwnRevisionIsSucceeded(t *testing.T) {
	if got := gitops.Judge(decodeADR(t, adrBody), adrRevision); got != gitops.VerdictSucceeded {
		t.Errorf("Judge = %v, want succeeded", got)
	}
}

func TestJudge_OutOfSyncWithASucceededPhaseAndHealthyIsSucceededSinceSyncIsNeverRead(t *testing.T) {
	body := strings.Replace(adrBody, `"sync":"Synced"`, `"sync":"OutOfSync"`, 1)
	if got := gitops.Judge(decodeADR(t, body), adrRevision); got != gitops.VerdictSucceeded {
		t.Errorf("Judge = %v, want succeeded: sync is never read", got)
	}
}

func TestJudge_HealthDegradedIsFailed(t *testing.T) {
	body := strings.Replace(adrBody, `"health":"Healthy"`, `"health":"Degraded"`, 1)
	if got := gitops.Judge(decodeADR(t, body), adrRevision); got != gitops.VerdictFailed {
		t.Errorf("Judge = %v, want failed", got)
	}
}

func TestJudge_PhaseErrorIsFailed(t *testing.T) {
	body := strings.Replace(adrBody, `"phase":"Succeeded"`, `"phase":"Error"`, 1)
	if got := gitops.Judge(decodeADR(t, body), adrRevision); got != gitops.VerdictFailed {
		t.Errorf("Judge = %v, want failed", got)
	}
}

func TestJudge_ANotificationForAnotherRevisionIsPendingWhateverItSays(t *testing.T) {
	const ours = "1111111111111111111111111111111111111111"
	for name, body := range map[string]string{
		"succeeded": adrBody,
		"failed":    strings.Replace(adrBody, `"phase":"Succeeded"`, `"phase":"Failed"`, 1),
	} {
		if got := gitops.Judge(decodeADR(t, body), ours); got != gitops.VerdictPending {
			t.Errorf("%s notification for another revision: Judge = %v, want pending", name, got)
		}
	}
}

func TestJudge_EveryPhaseAndHealthCombination(t *testing.T) {
	for _, c := range []struct {
		phase, health string
		want          gitops.Verdict
	}{
		{"Succeeded", "Healthy", gitops.VerdictSucceeded},
		{"Failed", "Healthy", gitops.VerdictFailed},
		{"Error", "Progressing", gitops.VerdictFailed},
		{"Succeeded", "Degraded", gitops.VerdictFailed},
		{"Running", "Degraded", gitops.VerdictFailed},
		{"Running", "Progressing", gitops.VerdictPending},
		{"Succeeded", "Progressing", gitops.VerdictPending},
		{"Succeeded", "Missing", gitops.VerdictPending},
		{"Running", "Healthy", gitops.VerdictPending},
		{"", "", gitops.VerdictPending},
	} {
		n := gitops.Notification{App: "app-alpha", Revision: adrRevision, Phase: c.phase, Health: c.health}
		if got := gitops.Judge(n, adrRevision); got != c.want {
			t.Errorf("phase %q health %q: Judge = %v, want %v", c.phase, c.health, got, c.want)
		}
	}
}

func TestJudge_TheZeroNotificationIsPending(t *testing.T) {
	if got := gitops.Judge(gitops.Notification{}, adrRevision); got != gitops.VerdictPending {
		t.Errorf("Judge = %v, want pending", got)
	}
}

func TestCheck_ReadsOnceAndReturnsTheVerdictWithTheNotification(t *testing.T) {
	notes := gitopstest.NewNotifications()
	n := decodeADR(t, adrBody)
	notes.Set(n)

	out, err := gitops.Check(context.Background(), notes, "app-alpha", adrRevision)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if out.Verdict != gitops.VerdictSucceeded || !reflect.DeepEqual(out.Notification, n) {
		t.Errorf("Check = %+v, want succeeded with the notification read", out)
	}
	if got := notes.Reads("app-alpha", adrRevision); got != 1 {
		t.Errorf("Check read %d times, want 1", got)
	}
}

func TestCheck_NoNotificationYetIsPending(t *testing.T) {
	out, err := gitops.Check(context.Background(), gitopstest.NewNotifications(), "app-alpha", adrRevision)
	if err != nil || out.Verdict != gitops.VerdictPending {
		t.Errorf("Check = %+v, %v, want pending, nil", out, err)
	}
}

func TestCheck_ASourceErrorIsReturned(t *testing.T) {
	notes := gitopstest.NewNotifications()
	boom := errors.New("notification store unreachable")
	notes.FailWith(boom)

	if _, err := gitops.Check(context.Background(), notes, "app-alpha", adrRevision); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the source's error", err)
	}
}

func TestWait_PollsThroughNoneAndPendingUntilSucceeded(t *testing.T) {
	notes := gitopstest.NewNotifications()
	done := decodeADR(t, adrBody)
	notes.Sequence("app-alpha", adrRevision,
		gitops.Notification{},
		gitops.Notification{App: "app-alpha", Revision: adrRevision, Phase: "Running", Health: "Progressing"},
		done)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := gitops.Wait(ctx, notes, "app-alpha", adrRevision, time.Millisecond)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if out.Verdict != gitops.VerdictSucceeded || !reflect.DeepEqual(out.Notification, done) {
		t.Errorf("Wait = %+v, want succeeded with the final notification", out)
	}
	if got := notes.Reads("app-alpha", adrRevision); got != 3 {
		t.Errorf("Wait read %d times, want 3: none, pending, then succeeded, and no read after", got)
	}
}

func TestWait_AFailedVerdictEndsTheWaitAsAnOutcomeNotAnError(t *testing.T) {
	notes := gitopstest.NewNotifications()
	failed := gitops.Notification{App: "app-alpha", Revision: adrRevision, Phase: "Failed", Health: "Degraded", Message: "ImagePullBackOff"}
	notes.Sequence("app-alpha", adrRevision,
		gitops.Notification{App: "app-alpha", Revision: adrRevision, Phase: "Running", Health: "Progressing"},
		failed)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := gitops.Wait(ctx, notes, "app-alpha", adrRevision, time.Millisecond)
	if err != nil {
		t.Fatalf("Wait: %v, want the failure reported as the outcome", err)
	}
	if out.Verdict != gitops.VerdictFailed || !reflect.DeepEqual(out.Notification, failed) {
		t.Errorf("Wait = %+v, want failed with the failing notification", out)
	}
}

func TestWait_AContextThatEndsWhilePendingReturnsItsErrorWithAPendingOutcome(t *testing.T) {
	notes := gitopstest.NewNotifications()
	notes.Set(gitops.Notification{App: "app-alpha", Revision: adrRevision, Phase: "Running", Health: "Progressing"})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	out, err := gitops.Wait(ctx, notes, "app-alpha", adrRevision, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want one wrapping context.DeadlineExceeded", err)
	}
	if out.Verdict != gitops.VerdictPending {
		t.Errorf("Verdict = %v, want pending", out.Verdict)
	}
	if got := notes.Reads("app-alpha", adrRevision); got < 2 {
		t.Errorf("Wait read %d time(s) before the deadline, want it to have polled", got)
	}
}

const platformToken = "evt-tok-0123456789abcdef"

func TestPlatformNotifications_GetsTheNotificationForAppAndRevisionWithTheBearerToken(t *testing.T) {
	var gotMethod, gotPath, gotApp, gotRevision, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		gotApp, gotRevision = r.URL.Query().Get("app"), r.URL.Query().Get("revision")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(adrBody))
	}))
	defer srv.Close()
	src := gitops.PlatformNotifications{BaseURL: srv.URL, Token: platformToken, HTTPClient: srv.Client()}

	n, ok, err := src.Latest(context.Background(), "app-alpha", adrRevision)
	if err != nil || !ok {
		t.Fatalf("Latest = %v, %v, want a notification", ok, err)
	}
	if gotMethod != http.MethodGet || gotPath != "/v1/gitops-notifications" {
		t.Errorf("request %s %s, want GET /v1/gitops-notifications", gotMethod, gotPath)
	}
	if gotApp != "app-alpha" || gotRevision != adrRevision {
		t.Errorf("query app=%q revision=%q, want app-alpha and %s", gotApp, gotRevision, adrRevision)
	}
	if gotAuth != "Bearer "+platformToken {
		t.Errorf("Authorization = %q, want the bearer token", gotAuth)
	}
	if want := decodeADR(t, adrBody); !reflect.DeepEqual(n, want) {
		t.Errorf("Latest = %+v, want %+v", n, want)
	}
}

func TestPlatformNotifications_RevisionEmptyAsksForTheAppsNewestWithNoRevisionParameter(t *testing.T) {
	var gotApp string
	var hasRevision bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotApp, hasRevision = r.URL.Query().Get("app"), r.URL.Query().Has("revision")
		_, _ = w.Write([]byte(adrBody))
	}))
	defer srv.Close()
	src := gitops.PlatformNotifications{BaseURL: srv.URL, Token: platformToken, HTTPClient: srv.Client()}

	n, ok, err := src.Latest(context.Background(), "app-alpha", "")
	if err != nil || !ok {
		t.Fatalf("Latest(app-alpha, \"\") = %v, %v, want a notification", ok, err)
	}
	if gotApp != "app-alpha" || hasRevision {
		t.Errorf("query app=%q with revision parameter %v, want app-alpha and no revision parameter at all", gotApp, hasRevision)
	}
	if n.Revision != adrRevision {
		t.Errorf("Revision = %q, want the newest notification's own %s", n.Revision, adrRevision)
	}
}

func TestPlatformNotifications_ANilHTTPClientUsesADefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(adrBody))
	}))
	defer srv.Close()
	src := gitops.PlatformNotifications{BaseURL: srv.URL, Token: platformToken}

	if _, ok, err := src.Latest(context.Background(), "app-alpha", adrRevision); err != nil || !ok {
		t.Errorf("Latest with no HTTPClient = %v, %v, want a notification", ok, err)
	}
}

func TestPlatformNotifications_A404MeansNoNotificationYet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"no notification"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	src := gitops.PlatformNotifications{BaseURL: srv.URL, Token: platformToken, HTTPClient: srv.Client()}

	n, ok, err := src.Latest(context.Background(), "app-alpha", adrRevision)
	if err != nil || ok {
		t.Errorf("Latest on 404 = %+v, %v, %v, want none and no error", n, ok, err)
	}
}

func TestPlatformNotifications_AnyOtherStatusIsAnErrorCarryingStatusAndBodyButNeverTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A misbehaving upstream echoes the credential it was handed.
		http.Error(w, "upstream rejected "+r.Header.Get("Authorization"), http.StatusBadGateway)
	}))
	defer srv.Close()
	src := gitops.PlatformNotifications{BaseURL: srv.URL, Token: platformToken, HTTPClient: srv.Client()}

	_, ok, err := src.Latest(context.Background(), "app-alpha", adrRevision)
	if err == nil {
		t.Fatalf("Latest on 502 returned no error (found %v)", ok)
	}
	msg := err.Error()
	if !strings.Contains(msg, "502") {
		t.Errorf("error %q does not carry the status 502", msg)
	}
	if !strings.Contains(msg, "upstream rejected") {
		t.Errorf("error %q does not carry the response body", msg)
	}
	if strings.Contains(msg, platformToken) {
		t.Error("the error carries the bearer token")
	}
}

func TestNotificationsFromContext_TrimsTheOperatorEventsPathFromTheCallbackURL(t *testing.T) {
	ctx := events.WithEnvelope(context.Background(), events.Envelope{
		SubscriptionID: "sub-1",
		Callback:       &events.Callback{URL: "https://api.mitstack.dev/v1/webhooks/operator-events", Token: platformToken},
	})

	src, err := gitops.NotificationsFromContext(ctx)
	if err != nil {
		t.Fatalf("NotificationsFromContext: %v", err)
	}
	if src.BaseURL != "https://api.mitstack.dev" || src.Token != platformToken {
		t.Errorf("BaseURL %q Token-set %v, want https://api.mitstack.dev and the callback's token", src.BaseURL, src.Token == platformToken)
	}
}

func TestNotificationsFromContext_TheBuiltReaderReachesThePlatformTheCallbackNames(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(adrBody))
	}))
	defer srv.Close()
	ctx := events.WithEnvelope(context.Background(), events.Envelope{
		Callback: &events.Callback{URL: srv.URL + "/v1/webhooks/operator-events", Token: platformToken},
	})

	src, err := gitops.NotificationsFromContext(ctx)
	if err != nil {
		t.Fatalf("NotificationsFromContext: %v", err)
	}
	if _, ok, err := src.Latest(ctx, "app-alpha", adrRevision); err != nil || !ok {
		t.Fatalf("Latest = %v, %v, want a notification", ok, err)
	}
	if gotPath != "/v1/gitops-notifications" || gotAuth != "Bearer "+platformToken {
		t.Errorf("request reached %q with bearer-set %v, want /v1/gitops-notifications with the callback token", gotPath, gotAuth == "Bearer "+platformToken)
	}
}

func TestNotificationsFromContext_IsAnErrorWithNoCallback(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"no envelope":           context.Background(),
		"envelope, no callback": events.WithEnvelope(context.Background(), events.Envelope{SubscriptionID: "sub-1"}),
	} {
		if _, err := gitops.NotificationsFromContext(ctx); err == nil {
			t.Errorf("%s: NotificationsFromContext returned no error", name)
		}
	}
}

func TestNotificationsFromContext_IsAnErrorWhenTheCallbackURLLacksTheOperatorEventsPath(t *testing.T) {
	ctx := events.WithEnvelope(context.Background(), events.Envelope{
		Callback: &events.Callback{URL: "https://api.mitstack.dev/v1/webhooks/something-else", Token: platformToken},
	})

	_, err := gitops.NotificationsFromContext(ctx)
	if err == nil {
		t.Fatal("NotificationsFromContext returned no error for a callback URL without /v1/webhooks/operator-events")
	}
	if strings.Contains(err.Error(), platformToken) {
		t.Error("the error carries the callback token")
	}
}
