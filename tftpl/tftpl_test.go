package tftpl_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/tftpl"
)

type vars = map[string]string

func TestRender(t *testing.T) {
	const domainBlock = "a\n%{ if domain != \"\" }\n${domain} {\n}\n%{ endif }\nb"
	cases := []struct {
		name, tmpl string
		vars       vars
		want       string
	}{
		{"a placeholder is substituted", "reverse_proxy ${app_name}-app:8080", vars{"app_name": "checkout"}, "reverse_proxy checkout-app:8080"},
		{"an escaped placeholder survives as shell syntax the box resolves at run time", "default_sni $${PUBLIC_IP}", vars{}, "default_sni ${PUBLIC_IP}"},
		{"escaped and interpolated placeholders render together", "https://$${PUBLIC_IP} {\n  reverse_proxy ${app_name}-app:8080\n}", vars{"app_name": "checkout"},
			"https://${PUBLIC_IP} {\n  reverse_proxy checkout-app:8080\n}"},
		{"a substituted value is inserted verbatim, never re-interpolated", "name=${app_name}", vars{"app_name": "${domain}"}, "name=${domain}"},
		{"everything but a placeholder stays byte identical", "#!/bin/bash\nset -euxo pipefail\n\n# a comment with $ and % and {braces}\nexit 0\n", vars{},
			"#!/bin/bash\nset -euxo pipefail\n\n# a comment with $ and % and {braces}\nexit 0\n"},
		{"a conditional keeps its body when the variable is set", domainBlock, vars{"domain": "app.example.com"}, "a\n\napp.example.com {\n}\n\nb"},
		{"a conditional drops its markers and body, keeping the newlines around them, when the variable is empty", domainBlock, vars{"domain": ""}, "a\n\nb"},
		{"a variable consumed only by a condition counts as used", "a\n%{ if domain != \"\" }\n${domain}\n%{ endif }\nb", vars{"domain": ""}, "a\n\nb"},
		{"two conditionals do not merge into one greedy match", "%{ if a != \"\" }A%{ endif }MIDDLE%{ if b != \"\" }B%{ endif }", vars{"a": "", "b": "yes"}, "MIDDLEB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tftpl.Render(tc.tmpl, tc.vars)

			if err != nil || got != tc.want {
				t.Errorf("Render(%q, %v) = %q, %v; want %q", tc.tmpl, tc.vars, got, err, tc.want)
			}
		})
	}
}

func TestRender_Refuses(t *testing.T) {
	cases := []struct {
		name, tmpl string
		vars       vars
		wantNamed  string
	}{
		{"a missing value, rather than rendering an empty string", "reverse_proxy ${app_name}-app:8080", vars{}, "app_name"},
		{"a supplied variable no placeholder uses", "hello", vars{"region": "eu-central-1"}, "region"},
		{"a condition on an unknown variable, rather than reading it as false", "%{ if nope != \"\" }x%{ endif }", vars{}, "nope"},
		{"a bare if directive", "%{ if x }a%{ endif }", vars{}, ""},
		{"a for directive", "%{ for x in y }a%{ endfor }", vars{"domain": "x"}, ""},
		{"an equality condition", "%{ if domain == \"prod\" }a%{ endif }", vars{"domain": "x"}, ""},
		{"a whitespace-stripping directive", "%{~ if domain != \"\" ~}a%{ endif }", vars{"domain": "x"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tftpl.Render(tc.tmpl, tc.vars)

			if err == nil {
				t.Fatalf("Render(%q, %v) = %q; want a refusal", tc.tmpl, tc.vars, got)
			}
			if !strings.Contains(err.Error(), tc.wantNamed) {
				t.Errorf("Render(%q) error = %v; want it to name %q", tc.tmpl, err, tc.wantNamed)
			}
		})
	}
}

func TestLines_MatchesTheOpenTofuSplit(t *testing.T) {
	got, err := tftpl.Lines("a\nb ${x}\nc", vars{"x": "1"})

	if want := []string{"a", "b 1", "c"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("Lines = %q, %v; want %q", got, err, want)
	}
}
