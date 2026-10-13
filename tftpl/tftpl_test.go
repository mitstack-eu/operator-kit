package tftpl_test

import (
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/tftpl"
)

func TestRender_SubstitutesPlaceholders(t *testing.T) {
	got, err := tftpl.Render("reverse_proxy ${app_name}-app:8080", map[string]string{"app_name": "checkout"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "reverse_proxy checkout-app:8080" {
		t.Fatalf("got %q", got)
	}
}

// `$${X}` is a literal `${X}` that the box resolves at run time.
func TestRender_EscapedPlaceholderSurvivesAsShellSyntax(t *testing.T) {
	got, err := tftpl.Render("default_sni $${PUBLIC_IP}", map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "default_sni ${PUBLIC_IP}" {
		t.Fatalf("got %q, want the shell expansion preserved verbatim", got)
	}
}

func TestRender_EscapedAndInterpolatedTogether(t *testing.T) {
	got, err := tftpl.Render(
		"https://$${PUBLIC_IP} {\n  reverse_proxy ${app_name}-app:8080\n}",
		map[string]string{"app_name": "checkout"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://${PUBLIC_IP} {\n  reverse_proxy checkout-app:8080\n}"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A value containing `${...}` must not be interpolated a second time.
func TestRender_SubstitutedValueIsNotRescanned(t *testing.T) {
	got, err := tftpl.Render("name=${app_name}", map[string]string{"app_name": "${domain}"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "name=${domain}" {
		t.Fatalf("got %q — a substituted value must be inserted verbatim, never re-interpolated", got)
	}
}

func TestRender_MissingValueIsAnErrorNotAnEmptyString(t *testing.T) {
	_, err := tftpl.Render("reverse_proxy ${app_name}-app:8080", map[string]string{})
	if err == nil {
		t.Fatal("a missing value must be an error")
	}
	if !strings.Contains(err.Error(), "app_name") {
		t.Fatalf("the error must name the placeholder, got %v", err)
	}
}

func TestRender_UnusedVariableIsAnError(t *testing.T) {
	_, err := tftpl.Render("hello", map[string]string{"region": "eu-central-1"})
	if err == nil {
		t.Fatal("a supplied-but-unused variable must be an error")
	}
	if !strings.Contains(err.Error(), "region") {
		t.Fatalf("the error must name the variable, got %v", err)
	}
}

func TestRender_UnsupportedDirectiveIsRefused(t *testing.T) {
	_, err := tftpl.Render("%{ if x }a%{ endif }", map[string]string{})
	if err == nil {
		t.Fatal("an unimplemented directive must be refused, not passed through")
	}
}

func TestLines_MatchesTheOpenTofuSplit(t *testing.T) {
	got, err := tftpl.Lines("a\nb ${x}\nc", map[string]string{"x": "1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"a", "b 1", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRender_LeavesEverythingElseByteIdentical(t *testing.T) {
	src := "#!/bin/bash\nset -euxo pipefail\n\n# a comment with $ and % and {braces}\nexit 0\n"
	got, err := tftpl.Render(src, map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != src {
		t.Fatalf("renderer altered non-placeholder text:\n got %q\nwant %q", got, src)
	}
}

func TestRender_ConditionalIncludesTheBodyWhenSet(t *testing.T) {
	got, err := tftpl.Render(
		"a\n%{ if domain != \"\" }\n${domain} {\n}\n%{ endif }\nb",
		map[string]string{"domain": "app.example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "a\n\napp.example.com {\n}\n\nb"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRender_ConditionalDropsTheBodyWhenEmpty(t *testing.T) {
	got, err := tftpl.Render(
		"a\n%{ if domain != \"\" }\n${domain} {\n}\n%{ endif }\nb",
		map[string]string{"domain": ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "a\n\nb"
	if got != want {
		t.Fatalf("got %q, want %q — the markers and the guarded body go, the surrounding newlines stay", got, want)
	}
}

// The no-domain path is the default, so a variable consumed only by a condition must not read as unused.
func TestRender_ConditionOnlyVariableIsNotReportedUnused(t *testing.T) {
	_, err := tftpl.Render(
		"a\n%{ if domain != \"\" }\n${domain}\n%{ endif }\nb",
		map[string]string{"domain": ""})
	if err != nil {
		t.Fatalf("a variable consumed only by a condition must count as used: %v", err)
	}
}

func TestRender_UnknownConditionVariableIsAnError(t *testing.T) {
	_, err := tftpl.Render("%{ if nope != \"\" }x%{ endif }", map[string]string{})
	if err == nil {
		t.Fatal("an unknown condition variable must be an error, not false")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("the error must name it, got %v", err)
	}
}

func TestRender_TwoConditionalsDoNotMerge(t *testing.T) {
	got, err := tftpl.Render(
		"%{ if a != \"\" }A%{ endif }MIDDLE%{ if b != \"\" }B%{ endif }",
		map[string]string{"a": "", "b": "yes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "MIDDLEB" {
		t.Fatalf("got %q, want %q — a greedy match would have eaten MIDDLE", got, "MIDDLEB")
	}
}

func TestRender_UnimplementedDirectiveFormIsRefused(t *testing.T) {
	for _, src := range []string{
		"%{ for x in y }a%{ endfor }",
		"%{ if domain == \"prod\" }a%{ endif }",
		"%{~ if domain != \"\" ~}a%{ endif }",
	} {
		if _, err := tftpl.Render(src, map[string]string{"domain": "x"}); err == nil {
			t.Errorf("must refuse unimplemented form: %q", src)
		}
	}
}
