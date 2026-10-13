package awsnames_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/awsnames"
)

var cfnStackNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)

func TestStackName_EmptyIDIsMitstackSub(t *testing.T) {
	if got := awsnames.StackName(""); got != "mitstack-sub" {
		t.Fatalf(`StackName("") = %q, want "mitstack-sub"`, got)
	}
}

func TestStackName_TurnsEveryCharacterOutsideAlphanumericsIntoADash(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"sub_demo", "mitstack-sub-demo"},
		{"sub_demo_12345", "mitstack-sub-demo-12345"},
		{"sub/DEMO_123:x", "mitstack-sub-DEMO-123-x"},
		{"123startsWithDigit", "mitstack-123startsWithDigit"},
		{"AZaz09", "mitstack-AZaz09"},
		{"a.b c-d", "mitstack-a-b-c-d"},
		{"___", "mitstack----"},
		{"café", "mitstack-caf-"},
		{"sub\x00\n", "mitstack-sub--"},
	} {
		if got := awsnames.StackName(tc.id); got != tc.want {
			t.Errorf("StackName(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestStackName_IsDeterministic(t *testing.T) {
	if a, b := awsnames.StackName("sub_demo_12345"), awsnames.StackName("sub_demo_12345"); a != b {
		t.Fatalf("StackName gave %q then %q for the same id", a, b)
	}
}

func TestStackName_IsCloudFormationSafe(t *testing.T) {
	for _, id := range []string{"", "sub/DEMO_123:x", "123startsWithDigit", "___", "café", "sub_" + strings.Repeat("\x00", 200)} {
		name := awsnames.StackName(id)
		if !cfnStackNameRE.MatchString(name) {
			t.Errorf("StackName(%q) = %q, not a CloudFormation stack name", id, name)
		}
		if len(name) > 128 {
			t.Errorf("StackName(%q) is %d characters, over CloudFormation's 128", id, len(name))
		}
	}
}

func TestStackName_StartsWithALetterForAnIDStartingWithADigit(t *testing.T) {
	name := awsnames.StackName("123startsWithDigit")
	if !cfnStackNameRE.MatchString(name) {
		t.Fatalf("StackName(%q) = %q, want it to start with a letter", "123startsWithDigit", name)
	}
}

func TestStackName_IsNeverLongerThan128(t *testing.T) {
	long := "sub_" + strings.Repeat("\x00", 200)
	if got := awsnames.StackName(long); len(got) != 128 {
		t.Errorf("StackName of a %d-character id is %d characters, want exactly 128", len(long), len(got))
	}
	if got, want := awsnames.StackName(long), "mitstack-sub-"+strings.Repeat("-", 115); got != want {
		t.Errorf("StackName of a long id = %q, want %q", got, want)
	}

	fits := strings.Repeat("a", 119)
	if got := awsnames.StackName(fits); got != "mitstack-"+fits {
		t.Errorf("StackName of a 119-character id = %q, want it whole after the prefix", got)
	}
	over := strings.Repeat("a", 119) + "b"
	if got := awsnames.StackName(over); got != "mitstack-"+fits {
		t.Errorf("StackName of a 120-character id = %q, want it cut to 128 characters", got)
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
