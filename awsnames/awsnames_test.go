package awsnames_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/awsnames"
)

func TestNames_MatchWhatTheEnginesActuallyCreate(t *testing.T) {
	for _, tc := range []struct{ got, want, engineSource string }{
		{awsnames.ECRRepository("checkout"), "mitstack-app-checkout", `template.yaml !Sub "mitstack-app-${AppName}"`},
		{awsnames.InstanceRole("checkout"), "mitstack-app-checkout-instance", `template.yaml RoleName !Sub "...-instance"`},
		{awsnames.DeployRole("checkout"), "mitstack-app-checkout-deploy", `template.yaml RoleName !Sub "...-deploy"`},
		{awsnames.InstanceProfile("checkout"), "mitstack-app-checkout-instance", `template.yaml InstanceProfileName`},
		{awsnames.Prefix("checkout"), "mitstack-app-checkout", `perapp/data.tf local.prefix`},
		{awsnames.ECRRepository("  checkout "), "mitstack-app-checkout", "the app name trimmed"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q: this must match %s", tc.got, tc.want, tc.engineSource)
		}
	}
}

func TestColliding_ListsEveryAccountUniqueName(t *testing.T) {
	want := []string{"mitstack-app-checkout", "mitstack-app-checkout-instance", "mitstack-app-checkout-deploy"}

	got := awsnames.Colliding("checkout")

	if !slices.Equal(got, want) {
		t.Errorf("Colliding(checkout) = %q, want %q: a name missing here is a name a switch will not wait for", got, want)
	}
}

var cfnStackNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)

func TestStackName_IsADeterministicCloudFormationSafeName(t *testing.T) {
	fits := strings.Repeat("a", 119)
	for _, tc := range []struct{ name, id, want string }{
		{"an empty id is mitstack-sub", "", "mitstack-sub"},
		{"an underscore becomes a dash", "sub_demo", "mitstack-sub-demo"},
		{"every underscore becomes a dash", "sub_demo_12345", "mitstack-sub-demo-12345"},
		{"slashes, underscores and colons become dashes", "sub/DEMO_123:x", "mitstack-sub-DEMO-123-x"},
		{"an id starting with a digit still yields a name starting with a letter", "123startsWithDigit", "mitstack-123startsWithDigit"},
		{"alphanumerics are kept", "AZaz09", "mitstack-AZaz09"},
		{"dots and spaces become dashes", "a.b c-d", "mitstack-a-b-c-d"},
		{"an id of punctuation alone", "___", "mitstack----"},
		{"a non-ASCII rune becomes one dash", "café", "mitstack-caf-"},
		{"control characters become dashes", "sub\x00\n", "mitstack-sub--"},
		{"a long id is cut to exactly 128 characters", "sub_" + strings.Repeat("\x00", 200), "mitstack-sub-" + strings.Repeat("-", 115)},
		{"a 119-character id fits whole after the prefix", fits, "mitstack-" + fits},
		{"a 120-character id is cut to 128 characters", fits + "b", "mitstack-" + fits},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, again := awsnames.StackName(tc.id), awsnames.StackName(tc.id)

			if got != tc.want || again != got {
				t.Errorf("StackName(%q) = %q then %q, want %q both times", tc.id, got, again, tc.want)
			}
			if !cfnStackNameRE.MatchString(got) || len(got) > 128 {
				t.Errorf("StackName(%q) = %q (%d characters), not a CloudFormation stack name of at most 128", tc.id, got, len(got))
			}
		})
	}
}

func TestSanitiseCFNName_TurnsEachRuneOutsideAlphanumericsIntoOneDash(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"AZaz09", "AZaz09"},
		{"sub/DEMO_123:x", "sub-DEMO-123-x"},
		{"café", "caf-"},
		{"a  b", "a--b"},
	} {
		if got := awsnames.SanitiseCFNName(tc.in); got != tc.want {
			t.Errorf("SanitiseCFNName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
