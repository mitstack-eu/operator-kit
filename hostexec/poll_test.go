package hostexec_test

// The poll bound is max(SSMPollTimeout, delivery + execution + SSMPollInterval), an unset option counting as zero; these tests shrink SSMPollTimeout below the option floors and release a held command long before any option-derived bound ends.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/hostexec"
)

const (
	heldStdout = "READY=0"
	// pollBoundPhrase is what a poll's error says once its bound ends it, attached or detached.
	pollBoundPhrase   = "timed out waiting for a terminal state after"
	shortPollInterval = 20 * time.Millisecond
	shortPollTimeout  = 100 * time.Millisecond
	// The derived bound ends more than two seconds after the hold, so a call bounded by SSMPollTimeout returns inside the hold and one bounded by the options is released with margin on a loaded runner.
	holdPastShortPoll = 3 * shortPollTimeout
	longExecution     = 3 * time.Second
)

// startHeld sends against a command held InProgress and returns once its poll has begun, cancelling ctx first when cancel is set.
func startHeld(t *testing.T, fake awsFake, ep entryPoint, interval, pollTimeout time.Duration, cancelAfterSend bool, opts ...hostexec.SendOption) (fakes, *string, <-chan error) {
	t.Helper()
	fake.held, fake.stdout = true, heldStdout
	f := newFakes(t, fake, "")
	setSSMPolling(t, interval, pollTimeout)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	out := new(string)
	done := runInBackground(t, f, func() error {
		var err error
		*out, err = ep.call(ctx, ssmHost(), fixtureScript, opts...)
		return err
	})
	waitForPoll(t, f, done)
	if cancelAfterSend {
		cancel()
	}
	return f, out, done
}

func TestSSMPoll_KeepsWaitingOnAHeldCommandUntilItEnds(t *testing.T) {
	detached := hostexec.WithDetachedPoll()
	cases := []struct {
		name              string
		entry             int
		final             string
		opts              []hostexec.SendOption
		pollTimeout, hold time.Duration
		cancel            bool
	}{
		{"a detached poll outlives cancellation until the command succeeds", 0, "Success", []hostexec.SendOption{detached}, 20 * time.Second, 1200 * time.Millisecond, true},
		{"a detached poll reports the terminal state the command ends in", 0, "Failed", []hostexec.SendOption{detached}, 20 * time.Second, 1200 * time.Millisecond, true},
		{"a detached RunOutput reads the output after cancellation, on the detached context", 1, "Success", []hostexec.SendOption{detached}, 20 * time.Second, 1200 * time.Millisecond, true},
		{"an execution timeout keeps Run polling past SSMPollTimeout to Success", 0, "Success", []hostexec.SendOption{hostexec.WithExecutionTimeout(longExecution)}, shortPollTimeout, holdPastShortPoll, false},
		{"an execution timeout keeps Run polling past SSMPollTimeout to Failed", 0, "Failed", []hostexec.SendOption{hostexec.WithExecutionTimeout(longExecution)}, shortPollTimeout, holdPastShortPoll, false},
		{"an execution timeout keeps RunOutput polling past SSMPollTimeout", 1, "Success", []hostexec.SendOption{hostexec.WithExecutionTimeout(longExecution)}, shortPollTimeout, holdPastShortPoll, false},
		{"a delivery timeout alone keeps the poll going past SSMPollTimeout", 0, "Success", []hostexec.SendOption{hostexec.WithDeliveryTimeout(30 * time.Second)}, shortPollTimeout, holdPastShortPoll, false},
		{"a detached Run after cancellation takes the execution timeout's bound to Success", 0, "Success", []hostexec.SendOption{hostexec.WithExecutionTimeout(longExecution), detached}, shortPollTimeout, holdPastShortPoll, true},
		{"a detached Run after cancellation takes the execution timeout's bound to Failed", 0, "Failed", []hostexec.SendOption{hostexec.WithExecutionTimeout(longExecution), detached}, shortPollTimeout, holdPastShortPoll, true},
		{"a detached RunOutput after cancellation takes the execution timeout's bound", 1, "Success", []hostexec.SendOption{hostexec.WithExecutionTimeout(longExecution), detached}, shortPollTimeout, holdPastShortPoll, true},
		{"a detached RunOutput after cancellation takes the delivery timeout's bound", 1, "Success", []hostexec.SendOption{hostexec.WithDeliveryTimeout(30 * time.Second), detached}, shortPollTimeout, holdPastShortPoll, true},
		{"SSMPollTimeout stays the floor above a shorter derived bound of 1.02s", 0, "Success", []hostexec.SendOption{hostexec.WithExecutionTimeout(1 * time.Second)}, 2 * time.Second, 1400 * time.Millisecond, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ep := entryPoints[tc.entry]
			f, out, done := startHeld(t, awsFake{statuses: []string{tc.final}}, ep, shortPollInterval, tc.pollTimeout, tc.cancel, tc.opts...)

			select {
			case err := <-done:
				t.Fatalf("%s returned (err=%v) while the command was still InProgress %s after the poll began, want it polling to its bound", ep.name, err, tc.hold)
			case <-time.After(tc.hold):
			}
			if err := os.Remove(f.path("hold")); err != nil {
				t.Fatalf("releasing the in-progress hold: %v", err)
			}

			select {
			case err := <-done:
				assertTerminalOutcome(t, ep.name, tc.final, *out, err)
				if q := f.queries(); ep.name == "RunOutput" && (len(q) == 0 || q[len(q)-1] != "StandardOutputContent") {
					t.Errorf("get-command-invocation queries = %q, want the last to be the StandardOutputContent read", q)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s did not return within 5s of the held command reaching %s", ep.name, tc.final)
			}
		})
	}
}

func assertTerminalOutcome(t *testing.T, name, final, out string, err error) {
	t.Helper()
	if final == "Success" {
		if err != nil || (name == "RunOutput" && out != heldStdout) {
			t.Errorf("%s() = %q, %v once the command reached Success, want nil and output %q for RunOutput", name, out, err, heldStdout)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), final) || strings.Contains(err.Error(), pollBoundPhrase) || errors.Is(err, context.Canceled) || out != "" {
		t.Errorf("%s() = %q, %v once the command ended %s, want no output and an error naming %s, neither a poll timeout nor the cancellation", name, out, err, final, final)
	}
}

func TestSSMPoll_GivesUpAtItsBound(t *testing.T) {
	detached := []hostexec.SendOption{hostexec.WithDetachedPoll()}
	execution := []hostexec.SendOption{hostexec.WithExecutionTimeout(1 * time.Second)}
	cases := []struct {
		name                         string
		entry                        int
		opts                         []hostexec.SendOption
		interval, pollTimeout, bound time.Duration
		slack                        time.Duration
		cancel, hang                 bool
	}{
		{"an attached poll with an execution timeout gives up after it plus one poll interval", 0, execution, 200 * time.Millisecond, 100 * time.Millisecond, 1200 * time.Millisecond, 4 * time.Second, false, false},
		{"a detached poll with an execution timeout gives up after it plus one poll interval", 0, append(execution, detached...), 200 * time.Millisecond, 100 * time.Millisecond, 1200 * time.Millisecond, 4 * time.Second, true, false},
		{"an attached poll with no timeout option gives up at SSMPollTimeout", 0, nil, shortPollInterval, 300 * time.Millisecond, 300 * time.Millisecond, 3 * time.Second, false, false},
		{"a detached poll with no timeout option stays bounded by SSMPollTimeout", 0, detached, shortPollInterval, 300 * time.Millisecond, 300 * time.Millisecond, 3 * time.Second, true, false},
		{"a detached Run ends a hung Status call at SSMPollTimeout through the poll context", 0, detached, shortPollInterval, time.Second, time.Second, 3 * time.Second, true, true},
		{"a detached RunOutput ends a hung Status call at SSMPollTimeout with no output", 1, detached, shortPollInterval, time.Second, time.Second, 3 * time.Second, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ep := entryPoints[tc.entry]
			start := time.Now()
			_, out, done := startHeld(t, awsFake{hang: tc.hang}, ep, tc.interval, tc.pollTimeout, tc.cancel, tc.opts...)

			select {
			case err := <-done:
				if elapsed := time.Since(start); elapsed < tc.bound {
					t.Errorf("%s gave up after %s, want it to wait out the %s bound first", ep.name, elapsed, tc.bound)
				}
				if err == nil || !strings.Contains(err.Error(), pollBoundPhrase+" "+tc.bound.String()) || errors.Is(err, context.Canceled) || *out != "" {
					t.Errorf("%s() = %q, %v; want no output and an error saying %q %s, not the cancellation", ep.name, *out, err, pollBoundPhrase, tc.bound)
				}
			case <-time.After(tc.bound + tc.slack):
				t.Fatalf("%s did not give up within %s of the %s bound; the poll must stay bounded", ep.name, tc.slack, tc.bound)
			}
		})
	}
}

func TestSSMPoll_WithoutTheDetachedPollCancellationReturnsPromptly(t *testing.T) {
	for _, ep := range entryPoints {
		for _, set := range []struct {
			name string
			opts []hostexec.SendOption
		}{
			{"no options", nil},
			{"every option but the detached poll", []hostexec.SendOption{hostexec.WithComment(fixtureComment),
				hostexec.WithDeliveryTimeout(600 * time.Second), hostexec.WithExecutionTimeout(1800 * time.Second)}},
		} {
			t.Run(ep.name+"/"+set.name, func(t *testing.T) {
				_, _, done := startHeld(t, awsFake{}, ep, shortPollInterval, 20*time.Second, true, set.opts...)

				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Errorf("%s() error = %v, want it to wrap context.Canceled", ep.name, err)
					}
				case <-time.After(1 * time.Second):
					t.Fatalf("%s did not return promptly after the cancel; without WithDetachedPoll the poll stops on the caller's cancellation", ep.name)
				}
			})
		}
	}
}
