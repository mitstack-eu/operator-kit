package awsnames_test

import (
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/awsnames"
)

func TestNames_MatchWhatTheEnginesActuallyCreate(t *testing.T) {
	for _, tc := range []struct{ got, want, engineSource string }{
		{awsnames.ECRRepository("checkout"), "mitstack-app-checkout", `template.yaml !Sub "mitstack-app-${AppName}"`},
		{awsnames.InstanceRole("checkout"), "mitstack-app-checkout-instance", `template.yaml RoleName !Sub "...-instance"`},
		{awsnames.DeployRole("checkout"), "mitstack-app-checkout-deploy", `template.yaml RoleName !Sub "...-deploy"`},
		{awsnames.InstanceProfile("checkout"), "mitstack-app-checkout-instance", `template.yaml InstanceProfileName`},
		{awsnames.Prefix("checkout"), "mitstack-app-checkout", `perapp/data.tf local.prefix`},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q — this must match %s", tc.got, tc.want, tc.engineSource)
		}
	}
}

func TestNames_TrimTheAppName(t *testing.T) {
	if got := awsnames.ECRRepository("  checkout "); got != "mitstack-app-checkout" {
		t.Fatalf("got %q", got)
	}
}

func TestColliding_ListsEveryAccountUniqueName(t *testing.T) {
	got := awsnames.Colliding("checkout")
	want := []string{"mitstack-app-checkout", "mitstack-app-checkout-instance", "mitstack-app-checkout-deploy"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v — a name missing here is a name a switch will not wait for", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}
