package gitops_test

// A deploy is judged from the Argo CD notification of its exact revision: Succeeded and Healthy succeed, Error, Failed or Degraded fail, and anything else is pending.

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

const platformToken = "evt-tok-0123456789abcdef"

func decodeADR(t *testing.T, body string) gitops.Notification {
	t.Helper()
	var n gitops.Notification
	if err := json.Unmarshal([]byte(body), &n); err != nil {
		t.Fatalf("decoding the notification body: %v", err)
	}
	return n
}

func note(phase, health string) gitops.Notification {
	return gitops.Notification{App: "app-alpha", Revision: adrRevision, Phase: phase, Health: health}
}

func TestNotification_DecodesAndReencodesTheBodyADR0072Quotes(t *testing.T) {
	want := gitops.Notification{
		App: "app-alpha", Project: "app-alpha", Sync: "Synced", Revision: adrRevision, Health: "Healthy", Phase: "Succeeded",
		Message: "successfully synced (all tasks run)", ReconciledAt: "2026-09-28T10:47:23Z",
	}

	n := decodeADR(t, adrBody)
	raw, err := json.Marshal(n)

	if !reflect.DeepEqual(n, want) {
		t.Errorf("decoded %+v, want %+v", n, want)
	}
	var fields map[string]any
	if err != nil || json.Unmarshal(raw, &fields) != nil || fields["reconciledAt"] != "2026-09-28T10:47:23Z" {
		t.Errorf("re-encoded %s, %v; want reconciledAt carried through as 2026-09-28T10:47:23Z", raw, err)
	}
}

func TestJudge(t *testing.T) {
	adr := func(old, replacement string) gitops.Notification {
		return decodeADR(t, strings.Replace(adrBody, old, replacement, 1))
	}
	const other = "1111111111111111111111111111111111111111"
	cases := []struct {
		name     string
		n        gitops.Notification
		revision string
		want     gitops.Verdict
	}{
		{"the ADR body for its own revision succeeds", adr("", ""), adrRevision, gitops.VerdictSucceeded},
		{"OutOfSync with Succeeded and Healthy succeeds, since sync is never read", adr(`"sync":"Synced"`, `"sync":"OutOfSync"`), adrRevision, gitops.VerdictSucceeded},
		{"health Degraded fails", adr(`"health":"Healthy"`, `"health":"Degraded"`), adrRevision, gitops.VerdictFailed},
		{"phase Error fails", adr(`"phase":"Succeeded"`, `"phase":"Error"`), adrRevision, gitops.VerdictFailed},
		{"a success for another revision is pending", adr("", ""), other, gitops.VerdictPending},
		{"a failure for another revision is pending", adr(`"phase":"Succeeded"`, `"phase":"Failed"`), other, gitops.VerdictPending},
		{"Failed and Healthy fails", note("Failed", "Healthy"), adrRevision, gitops.VerdictFailed},
		{"Error and Progressing fails", note("Error", "Progressing"), adrRevision, gitops.VerdictFailed},
		{"Succeeded and Degraded fails", note("Succeeded", "Degraded"), adrRevision, gitops.VerdictFailed},
		{"Running and Degraded fails", note("Running", "Degraded"), adrRevision, gitops.VerdictFailed},
		{"Running and Progressing is pending", note("Running", "Progressing"), adrRevision, gitops.VerdictPending},
		{"Succeeded and Progressing is pending", note("Succeeded", "Progressing"), adrRevision, gitops.VerdictPending},
		{"Succeeded and Missing is pending", note("Succeeded", "Missing"), adrRevision, gitops.VerdictPending},
		{"Running and Healthy is pending", note("Running", "Healthy"), adrRevision, gitops.VerdictPending},
		{"no phase and no health is pending", note("", ""), adrRevision, gitops.VerdictPending},
		{"the zero notification is pending", gitops.Notification{}, adrRevision, gitops.VerdictPending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gitops.Judge(tc.n, tc.revision); got != tc.want {
				t.Errorf("Judge(%+v, %s) = %v, want %v", tc.n, tc.revision, got, tc.want)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	boom := errors.New("notification store unreachable")
	cases := []struct {
		name      string
		seed      func(*gitopstest.Notifications)
		want      gitops.Outcome
		wantErr   error
		wantReads int
	}{
		{"it reads once and returns the verdict with the notification", func(s *gitopstest.Notifications) { s.Set(decodeADR(t, adrBody)) },
			gitops.Outcome{Verdict: gitops.VerdictSucceeded, Notification: decodeADR(t, adrBody)}, nil, 1},
		{"no notification yet is pending", func(*gitopstest.Notifications) {}, gitops.Outcome{Verdict: gitops.VerdictPending}, nil, 1},
		{"a source error is returned", func(s *gitopstest.Notifications) { s.FailWith(boom) }, gitops.Outcome{Verdict: gitops.VerdictPending}, boom, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			notes := gitopstest.NewNotifications()
			tc.seed(notes)

			out, err := gitops.Check(context.Background(), notes, "app-alpha", adrRevision)

			if !reflect.DeepEqual(out, tc.want) || !errors.Is(err, tc.wantErr) {
				t.Errorf("Check = %+v, %v; want %+v, %v", out, err, tc.want, tc.wantErr)
			}
			if got := notes.Reads("app-alpha", adrRevision); tc.wantReads > 0 && got != tc.wantReads {
				t.Errorf("Check read %d times, want %d", got, tc.wantReads)
			}
		})
	}
}

func TestWait(t *testing.T) {
	failed := gitops.Notification{App: "app-alpha", Revision: adrRevision, Phase: "Failed", Health: "Degraded", Message: "ImagePullBackOff"}
	cases := []struct {
		name     string
		sequence []gitops.Notification
		timeout  time.Duration
		want     gitops.Outcome
		wantErr  error
		minReads int
		exact    bool
	}{
		{"it polls through none and pending until succeeded, and reads no more", []gitops.Notification{{}, note("Running", "Progressing"), decodeADR(t, adrBody)},
			5 * time.Second, gitops.Outcome{Verdict: gitops.VerdictSucceeded, Notification: decodeADR(t, adrBody)}, nil, 3, true},
		{"a failed verdict ends the wait as an outcome, not an error", []gitops.Notification{note("Running", "Progressing"), failed},
			5 * time.Second, gitops.Outcome{Verdict: gitops.VerdictFailed, Notification: failed}, nil, 2, true},
		{"a context ending while pending returns its error with a pending outcome after polling", []gitops.Notification{note("Running", "Progressing")},
			30 * time.Millisecond, gitops.Outcome{Verdict: gitops.VerdictPending, Notification: note("Running", "Progressing")}, context.DeadlineExceeded, 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			notes := gitopstest.NewNotifications()
			notes.Sequence("app-alpha", adrRevision, tc.sequence...)
			ctx, cancel := context.WithTimeout(context.Background(), tc.timeout)
			defer cancel()

			out, err := gitops.Wait(ctx, notes, "app-alpha", adrRevision, time.Millisecond)

			if out.Verdict != tc.want.Verdict || (tc.wantErr == nil && !reflect.DeepEqual(out, tc.want)) || !errors.Is(err, tc.wantErr) {
				t.Errorf("Wait = %+v, %v; want %+v, %v", out, err, tc.want, tc.wantErr)
			}
			if reads := notes.Reads("app-alpha", adrRevision); reads < tc.minReads || (tc.exact && reads != tc.minReads) {
				t.Errorf("Wait read %d times, want %d (exactly: %v)", reads, tc.minReads, tc.exact)
			}
		})
	}
}

type seenRequest struct{ method, path, auth, app, revision string }

func TestPlatformNotifications_Latest(t *testing.T) {
	cases := []struct {
		name      string
		revision  string
		status    int
		body      string
		nilClient bool
		wantOK    bool
		wantReq   seenRequest
		wantInErr []string
	}{
		{"it gets the app's notification at the revision with the bearer token", adrRevision, 200, adrBody, false, true,
			seenRequest{http.MethodGet, "/v1/gitops-notifications", "Bearer " + platformToken, "app-alpha", adrRevision}, nil},
		{"revision \"\" asks for the app's newest with no revision parameter", "", 200, adrBody, false, true,
			seenRequest{http.MethodGet, "/v1/gitops-notifications", "Bearer " + platformToken, "app-alpha", "<none>"}, nil},
		{"a nil HTTPClient uses a default", adrRevision, 200, adrBody, true, true, seenRequest{}, nil},
		{"a 404 means no notification yet", adrRevision, 404, `{"error":"no notification"}`, false, false, seenRequest{}, nil},
		{"any other status is an error carrying status and body but never the token", adrRevision, 502, "upstream rejected ", false, false, seenRequest{},
			[]string{"502", "upstream rejected"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got seenRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = seenRequest{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.URL.Query().Get("app"), r.URL.Query().Get("revision")}
				if !r.URL.Query().Has("revision") {
					got.revision = "<none>"
				}
				body := tc.body
				if tc.status == 502 {
					body += r.Header.Get("Authorization") // a misbehaving upstream echoes the credential it was handed
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			src := gitops.PlatformNotifications{BaseURL: srv.URL, Token: platformToken, HTTPClient: srv.Client()}
			if tc.nilClient {
				src.HTTPClient = nil
			}

			n, ok, err := src.Latest(context.Background(), "app-alpha", tc.revision)

			if ok != tc.wantOK || (err != nil) != (tc.wantInErr != nil) {
				t.Fatalf("Latest = %+v, %v, %v; want found %v, error %v", n, ok, err, tc.wantOK, tc.wantInErr != nil)
			}
			if tc.wantOK && !reflect.DeepEqual(n, decodeADR(t, adrBody)) {
				t.Errorf("Latest = %+v, want the ADR notification", n)
			}
			if tc.wantReq != (seenRequest{}) && got != tc.wantReq {
				t.Errorf("request = %+v, want %+v", got, tc.wantReq)
			}
			for _, want := range tc.wantInErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not carry %q", err, want)
				}
			}
			if err != nil && strings.Contains(err.Error(), platformToken) {
				t.Errorf("error %q carries the bearer token", err)
			}
		})
	}
}

func TestNotificationsFromContext(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(adrBody))
	}))
	defer srv.Close()
	withCallback := func(url string) context.Context {
		return events.WithEnvelope(context.Background(), events.Envelope{SubscriptionID: "sub-1", Callback: &events.Callback{URL: url, Token: platformToken}})
	}
	cases := []struct {
		name     string
		ctx      context.Context
		wantBase string
	}{
		{"it trims the operator-events path from the callback URL", withCallback("https://api.mitstack.dev/v1/webhooks/operator-events"), "https://api.mitstack.dev"},
		{"the reader it builds reaches the platform the callback names", withCallback(srv.URL + "/v1/webhooks/operator-events"), srv.URL},
		{"no envelope is an error", context.Background(), ""},
		{"an envelope with no callback is an error", events.WithEnvelope(context.Background(), events.Envelope{SubscriptionID: "sub-1"}), ""},
		{"a callback URL without the operator-events path is an error", withCallback("https://api.mitstack.dev/v1/webhooks/something-else"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, err := gitops.NotificationsFromContext(tc.ctx)

			if tc.wantBase == "" {
				if err == nil || strings.Contains(err.Error(), platformToken) {
					t.Errorf("NotificationsFromContext = %+v, %v; want an error carrying no token", src, err)
				}
				return
			}
			if err != nil || src.BaseURL != tc.wantBase || src.Token != platformToken {
				t.Fatalf("NotificationsFromContext = base %q, token set %v, %v; want %q and the callback's token", src.BaseURL, src.Token == platformToken, err, tc.wantBase)
			}
			if tc.wantBase != srv.URL {
				return
			}
			if _, ok, err := src.Latest(tc.ctx, "app-alpha", adrRevision); err != nil || !ok || gotPath != "/v1/gitops-notifications" || gotAuth != "Bearer "+platformToken {
				t.Errorf("Latest = %v, %v at %q with bearer set %v; want a notification from /v1/gitops-notifications with the callback token", ok, err, gotPath, gotAuth == "Bearer "+platformToken)
			}
		})
	}
}
