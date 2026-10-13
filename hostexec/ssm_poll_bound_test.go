package hostexec_test

// bound = max(SSMPollTimeout, delivery + execution + SSMPollInterval), an unset option counting as zero.
//
// These tests shrink SSMPollTimeout below the option floors and release the held command long before any option-derived bound ends, so none waits out a real timeout option.

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
	pollBoundCommandID = "cmd-poll-bound-1"
	pollBoundStdout    = "READY=0"
)

type heldPastBound struct {
	ep              entryPoint
	finalStatus     string
	opts            []hostexec.SendOption
	interval        time.Duration
	pollTimeout     time.Duration
	hold            time.Duration
	cancelAfterSend bool
}

func assertOutlivesHold(t *testing.T, h heldPastBound) {
	t.Helper()
	setSSMPolling(t, h.interval, h.pollTimeout)
	dir, _, pollStarted, holdFile := fakeSSMHeld(t, pollBoundCommandID, h.finalStatus, pollBoundStdout)
	setPath(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out string
	done := runInBackground(t, holdFile, func() error {
		var err error
		out, err = h.ep.call(ctx, optionsHost(), fixtureScript, h.opts...)
		return err
	})

	waitForFile(t, pollStarted, done)
	if h.cancelAfterSend {
		cancel()
	}

	select {
	case err := <-done:
		t.Fatalf("%s returned (err=%v) while the command was still InProgress %s after the poll began, want it to keep polling until its bound, the longer of SSMPollTimeout (%s) and the bound the timeout options derive", h.ep.name, err, h.hold, h.pollTimeout)
	case <-time.After(h.hold):
	}

	if err := os.Remove(holdFile); err != nil {
		t.Fatalf("releasing the in-progress hold: %v", err)
	}
	select {
	case err := <-done:
		assertTerminalOutcome(t, h.ep.name, h.finalStatus, out, err)
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return within 5s of the held command reaching %s", h.ep.name, h.finalStatus)
	}
}

func assertTerminalOutcome(t *testing.T, name, finalStatus, out string, err error) {
	t.Helper()
	if finalStatus == "Success" {
		if err != nil {
			t.Fatalf("%s() = %v once the command reached Success, want nil", name, err)
		}
		if name == "RunOutput" && out != pollBoundStdout {
			t.Errorf("RunOutput() = %q, want %q", out, pollBoundStdout)
		}
		return
	}
	if err == nil {
		t.Fatalf("%s() = nil once the command ended %s, want an error", name, finalStatus)
	}
	if !strings.Contains(err.Error(), finalStatus) {
		t.Errorf("%s() error = %v, want it to name the terminal state %s", name, err, finalStatus)
	}
	if strings.Contains(err.Error(), pollBoundPhrase) {
		t.Errorf("%s() error = %v, want the terminal state %s rather than a poll timeout", name, err, finalStatus)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("%s() error = %v wraps context.Canceled, want the terminal state %s", name, err, finalStatus)
	}
	if out != "" {
		t.Errorf("%s() output = %q alongside a %s error, want empty", name, out, finalStatus)
	}
}

func entryPointNamed(t *testing.T, name string) entryPoint {
	t.Helper()
	for _, ep := range entryPoints {
		if ep.name == name {
			return ep
		}
	}
	t.Fatalf("no entry point named %q", name)
	return entryPoint{}
}

// The derived bound ends more than two seconds after the hold, so a call bounded by SSMPollTimeout returns inside the hold and one bounded by the options is released with margin on a loaded runner.
const (
	shortPollInterval = 20 * time.Millisecond
	shortPollTimeout  = 100 * time.Millisecond
	holdPastShortPoll = 3 * shortPollTimeout
	longExecution     = 3 * time.Second
)

func TestSSMRun_ExecutionTimeoutKeepsThePollGoingPastSSMPollTimeoutToTheTerminalState(t *testing.T) {
	cases := []struct {
		entry       string
		finalStatus string
	}{
		{"Run", "Success"},
		{"Run", "Failed"},
		{"RunOutput", "Success"},
	}
	for _, c := range cases {
		t.Run(c.entry+"/"+c.finalStatus, func(t *testing.T) {
			assertOutlivesHold(t, heldPastBound{
				ep:          entryPointNamed(t, c.entry),
				finalStatus: c.finalStatus,
				opts:        []hostexec.SendOption{hostexec.WithExecutionTimeout(longExecution)},
				interval:    shortPollInterval,
				pollTimeout: shortPollTimeout,
				hold:        holdPastShortPoll,
			})
		})
	}
}

func TestSSMRun_DeliveryTimeoutAloneKeepsThePollGoingPastSSMPollTimeout(t *testing.T) {
	assertOutlivesHold(t, heldPastBound{
		ep:          entryPointNamed(t, "Run"),
		finalStatus: "Success",
		opts:        []hostexec.SendOption{hostexec.WithDeliveryTimeout(30 * time.Second)},
		interval:    shortPollInterval,
		pollTimeout: shortPollTimeout,
		hold:        holdPastShortPoll,
	})
}

func TestSSMRun_DetachedPollAfterCancellationTakesTheTimeoutOptionBound(t *testing.T) {
	cases := []struct {
		entry       string
		finalStatus string
		optName     string
		opt         hostexec.SendOption
	}{
		{"Run", "Success", "execution timeout", hostexec.WithExecutionTimeout(longExecution)},
		{"Run", "Failed", "execution timeout", hostexec.WithExecutionTimeout(longExecution)},
		{"RunOutput", "Success", "execution timeout", hostexec.WithExecutionTimeout(longExecution)},
		{"RunOutput", "Success", "delivery timeout", hostexec.WithDeliveryTimeout(30 * time.Second)},
	}
	for _, c := range cases {
		t.Run(c.entry+"/"+c.optName+"/"+c.finalStatus, func(t *testing.T) {
			assertOutlivesHold(t, heldPastBound{
				ep:              entryPointNamed(t, c.entry),
				finalStatus:     c.finalStatus,
				opts:            []hostexec.SendOption{c.opt, hostexec.WithDetachedPoll()},
				interval:        shortPollInterval,
				pollTimeout:     shortPollTimeout,
				hold:            holdPastShortPoll,
				cancelAfterSend: true,
			})
		})
	}
}

func TestSSMRun_SSMPollTimeoutStaysTheFloorAboveAShorterTimeoutOptionBound(t *testing.T) {
	// The derived 1.02 s is below the 2 s SSMPollTimeout, so SSMPollTimeout governs and the call must still be waiting 1.4 s in.
	assertOutlivesHold(t, heldPastBound{
		ep:          entryPointNamed(t, "Run"),
		finalStatus: "Success",
		opts:        []hostexec.SendOption{hostexec.WithExecutionTimeout(1 * time.Second)},
		interval:    shortPollInterval,
		pollTimeout: 2 * time.Second,
		hold:        1400 * time.Millisecond,
	})
}

func assertGivesUpAtBound(t *testing.T, opts []hostexec.SendOption, interval, pollTimeout, bound, slack time.Duration, cancelAfterSend bool) {
	t.Helper()
	setSSMPolling(t, interval, pollTimeout)
	dir, _, pollStarted, holdFile := fakeSSMHeld(t, pollBoundCommandID, "Success", pollBoundStdout)
	setPath(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	done := runInBackground(t, holdFile, func() error {
		return hostexec.Run(ctx, optionsHost(), fixtureScript, opts...)
	})

	waitForFile(t, pollStarted, done)
	if cancelAfterSend {
		cancel()
	}

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if elapsed < bound {
			t.Errorf("Run gave up after %s, want it to wait out the %s bound first", elapsed, bound)
		}
		assertPollBoundError(t, "Run", err)
		if err != nil && !strings.Contains(err.Error(), bound.String()) {
			t.Errorf("Run() error = %v, want it to name the %s bound that ended the wait", err, bound)
		}
	case <-time.After(bound + slack):
		t.Fatalf("Run did not give up within %s of the %s bound; the poll must stay bounded", slack, bound)
	}
}

func TestSSMRun_TimeoutOptionPollGivesUpAfterExecutionTimeoutPlusOnePollInterval(t *testing.T) {
	// A long SSMPollInterval makes the one-interval allowance observable.
	const (
		interval    = 200 * time.Millisecond
		pollTimeout = 100 * time.Millisecond
		execution   = 1 * time.Second
	)
	for _, detached := range []bool{false, true} {
		name := "attached"
		opts := []hostexec.SendOption{hostexec.WithExecutionTimeout(execution)}
		if detached {
			name = "detached after cancellation"
			opts = append(opts, hostexec.WithDetachedPoll())
		}
		t.Run(name, func(t *testing.T) {
			assertGivesUpAtBound(t, opts, interval, pollTimeout, execution+interval, 4*time.Second, detached)
		})
	}
}

func TestSSMRun_NoTimeoutOptionGivesUpAtSSMPollTimeout(t *testing.T) {
	const pollTimeout = 300 * time.Millisecond
	for _, detached := range []bool{false, true} {
		name := "attached"
		var opts []hostexec.SendOption
		if detached {
			name = "detached after cancellation"
			opts = []hostexec.SendOption{hostexec.WithDetachedPoll()}
		}
		t.Run(name, func(t *testing.T) {
			assertGivesUpAtBound(t, opts, shortPollInterval, pollTimeout, pollTimeout, 3*time.Second, detached)
		})
	}
}
