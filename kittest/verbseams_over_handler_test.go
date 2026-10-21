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
			h, err := handler.New(handler.Config{Resource: tc.fake, JWKSURL: s.JWKSServer(t), VerbBudget: 2, PollInterval: time.Millisecond, Sleep: func(context.Context, time.Duration) {}})
			if err != nil {
				t.Fatalf("handler.New: %v", err)
			}

			problems := kittest.CheckVerbSemantics(kittest.VerbSeamsOverHandler(t, h, s, tc.fake))

			found := false
			for _, p := range problems {
				found = found || strings.Contains(p, tc.wantNamed)
			}
			if (tc.wantNamed == "" && len(problems) != 0) || (tc.wantNamed != "" && !found) {
				t.Errorf("CheckVerbSemantics = %q, want a problem carrying %q (none when empty)", problems, tc.wantNamed)
			}
		})
	}
}
