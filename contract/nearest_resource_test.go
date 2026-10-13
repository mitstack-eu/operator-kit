package contract_test

// nearest_resource_test.go pins how NearestResource picks the registered name
// to suggest for a name the resource registry does not hold: the entry name
// or alias with the smallest edit distance, the alphabetically first on a tie.

import (
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// requireRegistered fails the test when name does not resolve through the
// registry, so a case whose fixture names drift reports the drift rather than
// a misleading nearest-name mismatch.
func requireRegistered(t *testing.T, name string) {
	t.Helper()
	if _, ok := contract.LookupResource(name); !ok {
		t.Fatalf("the fixture needs %q to be a registered name or alias", name)
	}
}

// hostnme is three edits from both host and hostId, and runsOnP is one edit
// from both runsOn and runsOnPr, so each tie goes to the alphabetically first.
// regon is one edit from region, which is an alias rather than an entry name.
func TestNearestResource_ReturnsTheClosestNameOrAliasAlphabeticallyFirstOnATie(t *testing.T) {
	cases := []struct {
		label, name, want, loser string
	}{
		{"tie broken alphabetically", "hostnme", "host", "hostId"},
		{"tie broken alphabetically at distance one", "runsOnP", "runsOn", "runsOnPr"},
		{"nearest is an alias", "regon", "region", "repo"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if _, ok := contract.LookupResource(tc.name); ok {
				t.Fatalf("the fixture needs %q to be absent from the registry", tc.name)
			}
			requireRegistered(t, tc.want)
			requireRegistered(t, tc.loser)
			if got := contract.NearestResource(tc.name); got != tc.want {
				t.Errorf("NearestResource(%q) = %q, want %q (not %q)", tc.name, got, tc.want, tc.loser)
			}
		})
	}
}

// A registered entry name or alias is at distance zero from itself, so it is
// its own nearest name.
func TestNearestResource_ARegisteredNameOrAliasIsItsOwnNearest(t *testing.T) {
	for _, r := range registry(t) {
		for _, name := range append([]string{r.Name}, r.Aliases...) {
			if got := contract.NearestResource(name); got != name {
				t.Errorf("NearestResource(%q) = %q, want %q itself", name, got, name)
			}
		}
	}
}

// The suggestion is always something the registry resolves, so a caller can
// quote it to a block author as a name that validates.
func TestNearestResource_AlwaysReturnsANameTheRegistryResolves(t *testing.T) {
	for _, name := range []string{"", "x", "noSuchOutput", "Region", "HOSTNAME", "cloud.regoin"} {
		got := contract.NearestResource(name)
		if _, ok := contract.LookupResource(got); !ok {
			t.Errorf("NearestResource(%q) = %q, which the registry does not resolve", name, got)
		}
	}
}
