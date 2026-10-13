// An Outputs value exactly equal to a request secret is refused with class "secret-in-output", naming the key and never the value; a value that merely contains a secret is left alone, and SecretOutputs is never inspected.
package handler_test

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

const wantRefusedClass = "secret-in-output"

type leakyHandoverFake struct {
	OutputKey   string
	OutputValue string
}

func (f *leakyHandoverFake) Ensure(ctx context.Context, d contract.Desired) error { return nil }

func (f *leakyHandoverFake) Observe(ctx context.Context, d contract.Desired) (contract.Observation, error) {
	return contract.Observation{State: contract.Absent}, nil
}

func (f *leakyHandoverFake) Delete(ctx context.Context, d contract.Desired) (map[string]string, error) {
	return map[string]string{f.OutputKey: f.OutputValue}, nil
}

var _ handler.Resource = (*leakyHandoverFake)(nil)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

func TestServe_ValidateOutputExactlyMatchingASecretValueIsRefused(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	const tokenValue = "ghp_leaked1234567890abcdef"
	f := &kittest.FakeResource{Count: 1, Outputs: map[string]string{"leakedToken": tokenValue}}
	h := newHandler(t, f, s)
	logBuf := captureLog(t)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-out-secret-validate", map[string]any{
		"operation": "validate",
		"secrets":   map[string]any{"github": map[string]any{"token": tokenValue}},
	}))
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (a refused response), body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if !strings.Contains(resp.Error, "leakedToken") {
		t.Errorf("Error = %q, want it to name the output key %q", resp.Error, "leakedToken")
	}
	if got := handler.RefusedClass(resp.StatusDetail); got != wantRefusedClass {
		t.Errorf("RefusedClass(StatusDetail) = %q, want %q (StatusDetail: %q)", got, wantRefusedClass, resp.StatusDetail)
	}

	kittest.AssertNoSecret(t, "response body", string(raw), tokenValue)
	kittest.AssertNoSecret(t, "log output", logBuf.String(), tokenValue)
}

func TestServe_UnsubscribeDeleteOutputExactlyMatchingASecretValueIsRefused(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	const tokenValue = "ghp_leakedondelete1234567890"
	f := &leakyHandoverFake{OutputKey: "leakedOnDelete", OutputValue: tokenValue}
	h := newHandler(t, f, s)
	logBuf := captureLog(t)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-out-secret-delete", map[string]any{
		"operation": "unsubscribe",
		"secrets":   map[string]any{"github": map[string]any{"token": tokenValue}},
	}))
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (a refused response), body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if !strings.Contains(resp.Error, "leakedOnDelete") {
		t.Errorf("Error = %q, want it to name the output key %q", resp.Error, "leakedOnDelete")
	}
	if got := handler.RefusedClass(resp.StatusDetail); got != wantRefusedClass {
		t.Errorf("RefusedClass(StatusDetail) = %q, want %q (StatusDetail: %q)", got, wantRefusedClass, resp.StatusDetail)
	}

	kittest.AssertNoSecret(t, "response body", string(raw), tokenValue)
	kittest.AssertNoSecret(t, "log output", logBuf.String(), tokenValue)
}

func TestServe_ObserveOutputContainingASecretSubstringLikeOwnerLoginIsAllowed(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	const ownerValue = "acme-co"
	const tokenValue = "ghp_realtoken1234567890abcdef"
	const repoOutput = ownerValue + "/weather-app"
	f := &kittest.FakeResource{Count: 1, Outputs: map[string]string{"repo": repoOutput}}
	h := newHandler(t, f, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-out-owner-allowed", map[string]any{
		"operation": "validate",
		"secrets":   map[string]any{"github": map[string]any{"owner": ownerValue, "token": tokenValue}},
	}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a non-secret value embedded in an output must be allowed, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Outputs["repo"] != repoOutput {
		t.Errorf(`Outputs["repo"] = %q, want %q intact`, resp.Outputs["repo"], repoOutput)
	}
}

// kittest.FakeResource has no SecretOutputs field, so this uses requestObserverFake.
func TestServe_SecretOutputsCarryingASecretValueIsNotRefused(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	const tokenValue = "ghp_legitsecretoutput1234567890"
	f := &requestObserverFake{SecretOutputs: map[string]string{"apiToken": tokenValue}}
	h := newHandler(t, f, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-secretoutputs-ok", map[string]any{
		"operation": "validate",
		"secrets":   map[string]any{"github": map[string]any{"token": tokenValue}},
	}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 — SecretOutputs is where a secret belongs, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.SecretOutputs["apiToken"] != tokenValue {
		t.Errorf(`SecretOutputs["apiToken"] = %q, want the block's own secret output, untouched`, resp.SecretOutputs["apiToken"])
	}
}

// An unset optional secret is an empty string and a blank output is legitimate, so the two must not combine into a false refusal.
func TestServe_EmptyOutputValueIsNotRefusedEvenWithAnUnsetSecretPresent(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	const tokenValue = "ghp_realtoken_for_empty_case_1234567890"
	f := &kittest.FakeResource{Count: 1, Outputs: map[string]string{"blankField": "", "status": "ok"}}
	h := newHandler(t, f, s)

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-out-empty-secret-allowed", map[string]any{
		"operation": "validate",
		"secrets":   map[string]any{"github": map[string]any{"unsetSecret": "", "token": tokenValue}},
	}))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 — an empty output value must not be refused just because an unset secret is also empty, body: %s", status, raw)
	}
	assertConforms(t, raw)

	resp := decodeResponse(t, raw)
	if resp.Outputs["blankField"] != "" {
		t.Errorf(`Outputs["blankField"] = %q, want "" intact`, resp.Outputs["blankField"])
	}
	if resp.Outputs["status"] != "ok" {
		t.Errorf(`Outputs["status"] = %q, want %q intact`, resp.Outputs["status"], "ok")
	}
}
