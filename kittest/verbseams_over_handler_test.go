// Only this test file imports handler, which keeps kittest and handler importable in either direction.
package kittest_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/handler"
	"github.com/mitstack-eu/operator-kit/v3/kittest"
)

func TestVerbSeamsOverHandler(t *testing.T) {
	cases := []struct {
		name      string
		fake      *kittest.FakeResource
		wantNamed string
	}{
		{"a conforming FakeResource passes cleanly through a real handler", &kittest.FakeResource{}, ""},
		{"DuplicateOnEnsure fails the idempotent subscribe", &kittest.FakeResource{DuplicateOnEnsure: true}, "must CONVERGE, not duplicate"},
		{"LeakOnDelete fails teardown", &kittest.FakeResource{LeakOnDelete: true}, "survived unsubscribe"},
		{"ErrorWhenAlreadyAbsent fails absence-is-success", &kittest.FakeResource{ErrorWhenAlreadyAbsent: true}, "endless retry"},
		{"ClobberOnUpgrade fails the convergent upgrade", &kittest.FakeResource{Outputs: map[string]string{"url": "https://example.test"}, ClobberOnUpgrade: true}, "destructive upgrade"},
		// DeadReportedPending answers Unknown with no error though ObserveErr is set, so ValidateWhenDead reads pending.
		{"DeadReportedPending fails dead detection", &kittest.FakeResource{DeadReportedPending: true, ObserveErr: errors.New("this would normally be an unrecoverable failure")}, "would wait forever"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)

			problems := kittest.CheckVerbSemantics(kittest.VerbSeamsOverHandler(t, newHandler(t, s, tc.fake), s, tc.fake))

			wantProblem(t, problems, tc.wantNamed)
		})
	}
}

func TestRedriveOverHandler(t *testing.T) {
	cases := []struct {
		name      string
		fake      *kittest.FakeBuilder
		wantNamed string
	}{
		{"a conforming FakeBuilder passes cleanly through a real handler", &kittest.FakeBuilder{}, ""},
		{"HideFinish fails the step signal", &kittest.FakeBuilder{HideFinish: true}, "redrive"},
		{"RebuildOnRedrive fails the resumed re-drive", &kittest.FakeBuilder{RebuildOnRedrive: true}, "changed 1 resource"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := kittest.NewSigner(t, "kid-1", 7)
			plain := &kittest.FakeResource{}
			seams := kittest.VerbSeamsOverHandler(t, newHandler(t, s, plain), s, plain)
			seams.Redrive = kittest.RedriveOverHandler(t, newHandler(t, s, tc.fake), s, tc.fake)

			wantProblem(t, kittest.CheckVerbSemantics(seams), tc.wantNamed)
		})
	}
}

func newHandler(t *testing.T, s *kittest.Signer, r handler.Resource) *handler.Handler {
	t.Helper()
	h, err := handler.New(handler.Config{Resource: r, JWKSURL: s.JWKSServer(t), VerbBudget: 2, PollInterval: time.Millisecond, Sleep: func(context.Context, time.Duration) {}})
	if err != nil {
		t.Fatalf("handler.New: %v", err)
	}
	return h
}

func wantProblem(t *testing.T, problems []string, wantNamed string) {
	t.Helper()
	found := false
	for _, p := range problems {
		found = found || strings.Contains(p, wantNamed)
	}
	if (wantNamed == "" && len(problems) != 0) || (wantNamed != "" && !found) {
		t.Errorf("CheckVerbSemantics = %q, want a problem carrying %q (none when empty)", problems, wantNamed)
	}
}
