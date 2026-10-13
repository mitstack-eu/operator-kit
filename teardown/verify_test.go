package teardown_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/teardown"
)

func present(kind string, isPresent bool, err error) teardown.Check {
	return teardown.Check{
		Kind: kind,
		Present: func(_ context.Context) (bool, error) {
			return isPresent, err
		},
	}
}

func loggedOptions() (teardown.Options, *[]string) {
	var mu sync.Mutex
	lines := make([]string, 0, 4)
	opts := teardown.Options{
		Logf: func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, fmt.Sprintf(format, args...))
		},
	}
	return opts, &lines
}

func joined(lines *[]string) string {
	return strings.Join(*lines, "\n")
}

func TestSurvivor_ConfidentPositiveReturnsKind(t *testing.T) {
	opts, _ := loggedOptions()
	got := teardown.Survivor(context.Background(), opts, present("instance", true, nil))
	if got != "instance" {
		t.Fatalf("Survivor() = %q, want the confidently-present Kind %q", got, "instance")
	}
}

func TestSurvivor_ErrorIsLoggedAndPassedOver(t *testing.T) {
	opts, lines := loggedOptions()
	boom := errors.New("throttled by provider")
	got := teardown.Survivor(context.Background(), opts, present("instance", false, boom))
	if got != "" {
		t.Fatalf("Survivor() = %q, want \"\" — an error must never be read as a survivor", got)
	}
	if !strings.Contains(joined(lines), "throttled by provider") {
		t.Fatalf("expected the error text to be logged, got log: %v", *lines)
	}
}

func TestSurvivor_EveryCheckErrors_ReturnsEmptyString(t *testing.T) {
	opts, lines := loggedOptions()
	got := teardown.Survivor(context.Background(), opts,
		present("instance", false, errors.New("timeout")),
		present("volume", false, errors.New("access denied")),
		present("eip", true, errors.New("rate limited")), // present=true but errored
	)
	if got != "" {
		t.Fatalf("Survivor() = %q, want \"\" — three errors are three unanswered questions, not one survivor", got)
	}
	out := joined(lines)
	if !strings.Contains(out, "timeout") || !strings.Contains(out, "access denied") || !strings.Contains(out, "rate limited") {
		t.Fatalf("expected all three errors to be logged, got: %v", *lines)
	}
}

func TestSurvivor_ConfidentNegativePassesOver(t *testing.T) {
	opts, _ := loggedOptions()
	got := teardown.Survivor(context.Background(), opts,
		present("instance", false, nil),
		present("volume", true, nil),
	)
	if got != "volume" {
		t.Fatalf("Survivor() = %q, want %q — the first check cleanly passed", got, "volume")
	}
}

// (true, err) is an error, not a positive.
func TestSurvivor_PresentTrueWithError_IsNotASurvivor(t *testing.T) {
	opts, lines := loggedOptions()
	got := teardown.Survivor(context.Background(), opts,
		present("instance", true, errors.New("ambiguous response")))
	if got != "" {
		t.Fatalf("Survivor() = %q, want \"\" — present=true with a non-nil error must not report a survivor", got)
	}
	if !strings.Contains(joined(lines), "ambiguous response") {
		t.Fatalf("expected the error to still be logged, got: %v", *lines)
	}
}

// A nil Present is "could not ask", never "it is gone".
func TestSurvivor_NilPresentIsLoggedNeverPanicsNeverSurvivor(t *testing.T) {
	opts, lines := loggedOptions()
	nilCheck := teardown.Check{Kind: "oauth-app"} // Present left nil on purpose

	var got string
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Survivor panicked on a nil Present func: %v", r)
			}
		}()
		got = teardown.Survivor(context.Background(), opts, nilCheck)
	}()

	if got != "" {
		t.Fatalf("Survivor() = %q, want \"\" — a capability we could not ask about is not evidence of survival", got)
	}
	if !strings.Contains(joined(lines), "oauth-app") {
		t.Fatalf("a nil Present must still be LOGGED by Kind — silent skip is the defect this pins, got: %v", *lines)
	}
}

func TestSurvivor_FirstSurvivorWins_SecondNeverCalled(t *testing.T) {
	opts, _ := loggedOptions()
	secondCalled := false
	second := teardown.Check{
		Kind: "volume",
		Present: func(_ context.Context) (bool, error) {
			secondCalled = true
			return true, nil
		},
	}

	got := teardown.Survivor(context.Background(), opts, present("instance", true, nil), second)

	if got != "instance" {
		t.Fatalf("Survivor() = %q, want the FIRST confirmed survivor %q", got, "instance")
	}
	if secondCalled {
		t.Fatal("Survivor consulted the second check after the first already confirmed a survivor")
	}
}

func TestSurvivor_ZeroChecksLogsAndReturnsEmpty(t *testing.T) {
	opts, lines := loggedOptions()
	got := teardown.Survivor(context.Background(), opts)
	if got != "" {
		t.Fatalf("Survivor() = %q, want \"\" for zero checks", got)
	}
	if len(*lines) == 0 {
		t.Fatal("Survivor with zero checks logged nothing — a silent false here is the same silence MIT-2563 exists to end")
	}
}

func TestSurvivor_ScrubAppliesToLoggedErrors(t *testing.T) {
	credentialText := "not-a-real-token"
	opts, lines := loggedOptions()
	opts.Scrub = func(s string) string {
		return strings.ReplaceAll(s, credentialText, "[redacted]")
	}
	teardown.Survivor(context.Background(), opts,
		present("instance", false, errors.New("auth failed with "+credentialText)))

	out := joined(lines)
	if strings.Contains(out, credentialText) {
		t.Fatalf("Scrub was set but the credential leaked into the log: %q", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("expected the scrubbed placeholder in the log, got: %q", out)
	}
}

func TestSurvivor_ZeroOptionsDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Survivor panicked with a zero Options{}: %v", r)
		}
	}()
	got := teardown.Survivor(context.Background(), teardown.Options{}, present("instance", true, nil))
	if got != "instance" {
		t.Fatalf("Survivor() = %q, want %q even with defaulted Options", got, "instance")
	}
}

// A cancelled ctx passes through to Present unchanged.
func TestSurvivor_ContextPassesThroughUnchanged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var seen context.Context
	check := teardown.Check{
		Kind: "instance",
		Present: func(c context.Context) (bool, error) {
			seen = c
			return false, nil
		},
	}

	teardown.Survivor(ctx, teardown.Options{}, check)

	if seen != ctx {
		t.Fatal("Survivor must pass the exact ctx it was given through to Present, not a derived one")
	}
}

func TestVerify_SurvivorEmitsExactlyOnceAndReturnsTrue(t *testing.T) {
	opts, _ := loggedOptions()
	var emitted []string
	opts.Emit = func(_ context.Context, detail string) error {
		emitted = append(emitted, detail)
		return nil
	}
	opts.Detail = func(kind string) string { return "left behind: " + kind }

	got := teardown.Verify(context.Background(), opts, present("instance", true, nil))

	if !got {
		t.Fatal("Verify() = false, want true — a confirmed survivor must be reported")
	}
	if len(emitted) != 1 {
		t.Fatalf("Emit was called %d times, want exactly 1", len(emitted))
	}
	if emitted[0] != "left behind: instance" {
		t.Fatalf("emitted detail = %q, want the exact string Options.Detail produced", emitted[0])
	}
}

func TestVerify_NoSurvivorEmitsNothingReturnsFalse(t *testing.T) {
	opts, _ := loggedOptions()
	emitCalled := false
	opts.Emit = func(_ context.Context, _ string) error {
		emitCalled = true
		return nil
	}

	got := teardown.Verify(context.Background(), opts, present("instance", false, nil))

	if got {
		t.Fatal("Verify() = true, want false — nothing was confirmed present")
	}
	if emitCalled {
		t.Fatal("Verify called Emit despite there being no survivor")
	}
}

// A failure to deliver the survivor report must be logged, not dropped.
func TestVerify_EmitErrorStillReturnsTrueAndLogsBoth(t *testing.T) {
	opts, lines := loggedOptions()
	deliveryErr := errors.New("ingest endpoint unreachable")
	opts.Emit = func(_ context.Context, _ string) error { return deliveryErr }
	opts.Detail = func(kind string) string { return "teardown left " + kind + " in place" }

	got := teardown.Verify(context.Background(), opts, present("instance", true, nil))

	if !got {
		t.Fatal("Verify() = false, want true — a survivor was still found, delivery failing does not change that")
	}
	out := joined(lines)
	if !strings.Contains(out, "teardown left instance in place") {
		t.Fatalf("expected the survivor detail in the log, got: %q", out)
	}
	if !strings.Contains(out, "ingest endpoint unreachable") {
		t.Fatalf("expected the delivery error in the log, got: %q", out)
	}
}

func TestVerify_ScrubCoversEmittedDetailAndLog(t *testing.T) {
	credentialText := "not-a-real-token"
	opts, lines := loggedOptions()
	opts.Scrub = func(s string) string { return strings.ReplaceAll(s, credentialText, "[redacted]") }
	var emittedDetail string
	opts.Emit = func(_ context.Context, detail string) error {
		emittedDetail = detail
		return nil
	}
	opts.Detail = func(kind string) string { return "found leftover credential " + credentialText + " on " + kind }

	teardown.Verify(context.Background(), opts, present("oauth-app", true, nil))

	if strings.Contains(emittedDetail, credentialText) {
		t.Fatalf("the credential leaked into the emitted detail: %q", emittedDetail)
	}
	if strings.Contains(joined(lines), credentialText) {
		t.Fatalf("the credential leaked into the log: %q", joined(lines))
	}
}

// events.EmitFailed is a no-op when ctx carries no events envelope.
func TestVerify_ZeroOptionsDefaultsToEventsEmitFailed_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Verify panicked with a zero Options{} and no events envelope on ctx: %v", r)
		}
	}()
	got := teardown.Verify(context.Background(), teardown.Options{}, present("instance", true, nil))
	if !got {
		t.Fatal("Verify() = false, want true — a survivor was confirmed even though Emit defaulted")
	}
}

func TestAbsent_EveryCheckGone_ReturnsTrue(t *testing.T) {
	opts, _ := loggedOptions()
	got := teardown.Absent(context.Background(), opts,
		present("instance", false, nil),
		present("volume", false, nil),
	)
	if !got {
		t.Fatal("Absent() = false, want true — every check was asked and every one said gone")
	}
}

func TestAbsent_AnyConfidentPresence_ReturnsFalse(t *testing.T) {
	opts, _ := loggedOptions()
	got := teardown.Absent(context.Background(), opts,
		present("instance", false, nil),
		present("volume", true, nil),
	)
	if got {
		t.Fatal("Absent() = true, want false — one confirmed survivor must defeat absence")
	}
}

// An error defeats absence even when every other check said gone.
func TestAbsent_AnyError_ReturnsFalseEvenWhenOthersAreGone(t *testing.T) {
	opts, lines := loggedOptions()
	got := teardown.Absent(context.Background(), opts,
		present("instance", false, nil),
		present("volume", false, errors.New("throttled")),
	)
	if got {
		t.Fatal("Absent() = true, want false — an unanswered check must never be read as absence")
	}
	if !strings.Contains(joined(lines), "throttled") {
		t.Fatalf("expected the error to be logged, got: %v", *lines)
	}
}

func TestAbsent_PresentTrueWithError_ReturnsFalse(t *testing.T) {
	opts, _ := loggedOptions()
	got := teardown.Absent(context.Background(), opts,
		present("instance", true, errors.New("ambiguous response")))
	if got {
		t.Fatal("Absent() = true, want false — present=true with a non-nil error is not evidence of anything")
	}
}

func TestAbsent_NilPresentIsLoggedAndReturnsFalse(t *testing.T) {
	opts, lines := loggedOptions()
	nilCheck := teardown.Check{Kind: "oauth-app"}
	got := teardown.Absent(context.Background(), opts, nilCheck)
	if got {
		t.Fatal("Absent() = true, want false — a capability we could not ask about is not evidence of absence")
	}
	if !strings.Contains(joined(lines), "oauth-app") {
		t.Fatalf("a nil Present must still be logged by Kind, got: %v", *lines)
	}
}

func TestAbsent_ZeroChecksReturnsFalseAndLogs(t *testing.T) {
	opts, lines := loggedOptions()
	got := teardown.Absent(context.Background(), opts)
	if got {
		t.Fatal("Absent() = true, want false for zero checks — there is no evidence to assert absence from")
	}
	if len(*lines) == 0 {
		t.Fatal("Absent with zero checks logged nothing — a silent false here hides the same defect as a silent Survivor")
	}
}

func TestAbsent_ScrubAppliesToLoggedErrors(t *testing.T) {
	credentialText := "not-a-real-token"
	opts, lines := loggedOptions()
	opts.Scrub = func(s string) string { return strings.ReplaceAll(s, credentialText, "[redacted]") }
	teardown.Absent(context.Background(), opts,
		present("instance", false, errors.New("auth failed with "+credentialText)))

	out := joined(lines)
	if strings.Contains(out, credentialText) {
		t.Fatalf("Scrub was set but the credential leaked into the log: %q", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("expected the scrubbed placeholder in the log, got: %q", out)
	}
}

// One erroring check must make Survivor report no survivor and Absent report not absent.
func TestSurvivorAndAbsent_SingleErroringCheck_NeitherReportsAnAnswer(t *testing.T) {
	opts, _ := loggedOptions()
	check := present("instance", false, errors.New("throttled"))

	survivor := teardown.Survivor(context.Background(), opts, check)
	absent := teardown.Absent(context.Background(), opts, check)

	if survivor != "" {
		t.Fatalf("Survivor() = %q, want \"\" — an error is not evidence of a survivor", survivor)
	}
	if absent {
		t.Fatal("Absent() = true, want false — the same error is not evidence of absence either")
	}
}
