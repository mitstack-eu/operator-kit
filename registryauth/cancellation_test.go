package registryauth_test

// Login checks ctx.Err() exactly once, up front (see
// TestLogin_HonoursContextCancellation in registryauth_test.go, which only
// exercises an already-cancelled context). A context cancelled while the aws
// or docker subprocess is actually running is never rechecked, so it
// surfaces as a bare subprocess failure instead of context.Canceled. These
// pin the fix, following the bounded-select shape hostexec's ssh driver test
// already uses so a regression fails the test rather than hanging the suite.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/registryauth"
)

func TestLogin_ECR_ContextCancelledMidGetLoginPasswordIsAttributedToTheContext(t *testing.T) {
	fakeRegistryBins(t, "sleep 5", dockerOK())

	r := ecrRegistry("123456789012.dkr.ecr.eu-central-1.amazonaws.com", "eu-central-1")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	done := make(chan error, 1)
	go func() { done <- registryauth.Login(ctx, r) }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Login() error = %v, want it to wrap context.Canceled — a cancellation mid aws ecr get-login-password must not surface as a bare subprocess failure", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Login did not return promptly after ctx was cancelled mid get-login-password — the aws subprocess was not killed")
	}
}

func TestLogin_ECR_ContextCancelledMidDockerLoginIsAttributedToTheContext(t *testing.T) {
	fakeRegistryBins(t, awsEmits("pw"), "sleep 5")

	r := ecrRegistry("123456789012.dkr.ecr.eu-central-1.amazonaws.com", "eu-central-1")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	done := make(chan error, 1)
	go func() { done <- registryauth.Login(ctx, r) }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Login() error = %v, want it to wrap context.Canceled — a cancellation mid docker login must not surface as a bare subprocess failure", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Login did not return promptly after ctx was cancelled mid docker login — the docker subprocess was not killed")
	}
}

func TestLogin_OCIBasic_ContextCancelledMidDockerLoginIsAttributedToTheContext(t *testing.T) {
	fakeRegistryBins(t, "", "sleep 5")

	r := ociRegistry("ghcr.io", "deploy-bot", "the-credential")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	done := make(chan error, 1)
	go func() { done <- registryauth.Login(ctx, r) }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Login() error = %v, want it to wrap context.Canceled — a cancellation mid docker login must not surface as a bare subprocess failure", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Login did not return promptly after ctx was cancelled mid docker login — the docker subprocess was not killed")
	}
}
