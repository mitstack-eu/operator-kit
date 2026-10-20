package contract_test

// The resource registry holds the output names a block may declare and read; a check over every entry pins a property, never the registry's content.

import (
	"reflect"
	"regexp"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

func TestResources_EveryEntryHoldsTheRegistrysInvariants(t *testing.T) {
	nineKinds := map[string]bool{
		"App": true, "Host & network": true, "Data": true, "Build & deploy": true, "Access": true,
		"Model": true, "Coding agent": true, "Work": true, "Source": true,
	}
	cases := []struct {
		invariant string
		check     func(t *testing.T, rs []contract.ResourceEntry)
	}{
		{"each entry is complete", func(t *testing.T, rs []contract.ResourceEntry) {
			for _, r := range rs {
				if !nineKinds[r.Kind] || r.Meaning == "" || r.Shape == "" || (!r.Secret && r.Example == "") {
					t.Errorf("%q: kind %q (one of the nine labels?), meaning %q, shape %q, example %q (needed unless secret)",
						r.Name, r.Kind, r.Meaning, r.Shape, r.Example)
				}
			}
		}},
		{"aliases are never nil", func(t *testing.T, rs []contract.ResourceEntry) {
			for _, r := range rs {
				if r.Aliases == nil {
					t.Errorf("%q: Aliases is nil, want an empty list when the entry has none", r.Name)
				}
			}
		}},
		{"entries sort by name, each name once", func(t *testing.T, rs []contract.ResourceEntry) {
			for i := 1; i < len(rs); i++ {
				if rs[i-1].Name >= rs[i].Name {
					t.Errorf("entry %d (%q) must sort strictly before entry %d (%q)", i-1, rs[i-1].Name, i, rs[i].Name)
				}
			}
		}},
		{"no alias equals an entry's name", func(t *testing.T, rs []contract.ResourceEntry) {
			names := map[string]bool{}
			for _, r := range rs {
				names[r.Name] = true
			}
			for _, r := range rs {
				for _, a := range r.Aliases {
					if names[a] {
						t.Errorf("%q: alias %q is also an entry's name", r.Name, a)
					}
				}
			}
		}},
		{"a secret entry carries no example, which would show the secret", func(t *testing.T, rs []contract.ResourceEntry) {
			for _, r := range rs {
				if r.Secret && r.Example != "" {
					t.Errorf("secret entry %q carries example %q, want none", r.Name, r.Example)
				}
			}
		}},
		{"LookupResource finds every entry by its name and each alias", func(t *testing.T, rs []contract.ResourceEntry) {
			for _, r := range rs {
				for _, name := range append([]string{r.Name}, r.Aliases...) {
					if got, ok := contract.LookupResource(name); !ok || !reflect.DeepEqual(got, r) {
						t.Errorf("LookupResource(%q) = %+v, %v; want the entry %+v", name, got, ok, r)
					}
				}
			}
		}},
		{"a registered name or alias is its own nearest", func(t *testing.T, rs []contract.ResourceEntry) {
			for _, r := range rs {
				for _, name := range append([]string{r.Name}, r.Aliases...) {
					if got := contract.NearestResource(name); got != name {
						t.Errorf("NearestResource(%q) = %q, want %q itself", name, got, name)
					}
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.invariant, func(t *testing.T) {
			rs := registry(t)

			tc.check(t, rs)
		})
	}
}

type lookupCase struct {
	name, behaviour string
	check           func(t *testing.T, r contract.ResourceEntry)
}

func TestLookupResource_ResolvesEachNameToItsEntry(t *testing.T) {
	plain := func(name, kind string) func(t *testing.T, r contract.ResourceEntry) {
		return func(t *testing.T, r contract.ResourceEntry) {
			if r.Name != name || (kind != "" && r.Kind != kind) || r.Secret || len(r.Aliases) != 0 {
				t.Errorf("entry %+v, want %q itself, kind %q, not secret, with no aliases", r, name, kind)
			}
		}
	}
	shaped := func(name string, shape *regexp.Regexp) func(t *testing.T, r contract.ResourceEntry) {
		return func(t *testing.T, r contract.ResourceEntry) {
			if !shape.MatchString(r.Example) {
				t.Errorf("%q: example %q does not match the published shape %s", name, r.Example, shape)
			}
		}
	}
	entry := func(want contract.ResourceEntry) func(t *testing.T, r contract.ResourceEntry) {
		return func(t *testing.T, r contract.ResourceEntry) { wantField(t, "the entry", r, want) }
	}
	https := regexp.MustCompile(`^https://`)
	cases := []lookupCase{
		{"region", "resolves to cloud.region", func(t *testing.T, r contract.ResourceEntry) { wantField(t, "Name", r.Name, "cloud.region") }},
		{"instanceType", "resolves to host.type", func(t *testing.T, r contract.ResourceEntry) { wantField(t, "Name", r.Name, "host.type") }},
		{"cloud.region", "is unchanged for the kube-cluster block", entry(contract.ResourceEntry{Name: "cloud.region", Kind: "Host & network",
			Meaning: "The cloud region the app runs in.", Shape: "cloud region code", Example: "eu-central-1", Aliases: []string{"region"}})},
		{"host.type", "is unchanged for the kube-cluster block", entry(contract.ResourceEntry{Name: "host.type", Kind: "Host & network",
			Meaning: "The machine type the app runs on.", Shape: "cloud instance type", Example: "t3.small", Aliases: []string{"instanceType"}})},
		{"namespace", "is a plain canonical entry", plain("namespace", "Host & network")},
		{"serviceAccount", "is a plain canonical entry", plain("serviceAccount", "Access")},
	}
	for _, out := range []struct {
		name  string
		shape *regexp.Regexp
	}{
		{"privateEndpoint", https}, {"issuerUrl", https}, {"issuerBucket", regexp.MustCompile(`^mitstack-oidc-[a-z2-7]{26}$`)},
		{"clusterRepoUrl", https}, {"serverId", regexp.MustCompile(`^[0-9]+$`)}, {"enrolmentTokenSha256", regexp.MustCompile(`^[a-z2-7]{52}$`)},
	} {
		cases = append(cases,
			lookupCase{out.name, "is a plain entry the kube-cluster block registers", plain(out.name, "")},
			lookupCase{out.name, "has an example of the published shape", shaped(out.name, out.shape)})
	}
	for _, tc := range cases {
		t.Run(tc.name+" "+tc.behaviour, func(t *testing.T) {
			r, ok := contract.LookupResource(tc.name)

			if !ok {
				t.Fatalf("LookupResource(%q) found nothing", tc.name)
			}
			tc.check(t, r)
		})
	}
}

// location and serverType are published as cloud.region and host.type, and a match is case-sensitive.
func TestLookupResource_FindsNoUnknownOrMiscasedName(t *testing.T) {
	for _, name := range []string{"", "noSuchOutput", "Region", "cloud.Region", "Host", "location", "serverType"} {
		t.Run(name, func(t *testing.T) {
			r, ok := contract.LookupResource(name)

			if ok {
				t.Errorf("LookupResource(%q) found %q, want not found", name, r.Name)
			}
		})
	}
}

func TestResources_HandACallerCopiesItCanMutate(t *testing.T) {
	t.Run("Resources", func(t *testing.T) {
		snapshot := []contract.ResourceEntry{}
		for _, r := range registry(t) {
			r.Aliases = append([]string{}, r.Aliases...)
			snapshot = append(snapshot, r)
		}
		got := registry(t)

		for i := range got {
			for j := range got[i].Aliases {
				got[i].Aliases[j] = "mutated"
			}
			got[i].Name = "mutated"
		}

		if after := contract.Resources(); !reflect.DeepEqual(after, snapshot) {
			t.Error("mutating what Resources returned changed a later call's result")
		}
	})
	t.Run("LookupResource", func(t *testing.T) {
		first, ok := contract.LookupResource("cloud.region")
		if !ok || len(first.Aliases) == 0 {
			t.Fatalf("cloud.region needs an alias for this test to mutate, got %+v, %v", first, ok)
		}
		want := first
		want.Aliases = append([]string{}, first.Aliases...)

		first.Aliases[0] = "mutated"

		if again, _ := contract.LookupResource("cloud.region"); !reflect.DeepEqual(again, want) {
			t.Errorf("mutating a looked-up entry changed the registry: a later lookup returned %+v, want %+v", again, want)
		}
	})
}

// hostnme and runsOnP each tie between two names, and regon is one edit from the alias region.
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
			_, absent := contract.LookupResource(tc.name)
			_, wantOK := contract.LookupResource(tc.want)
			_, loserOK := contract.LookupResource(tc.loser)
			if absent || !wantOK || !loserOK {
				t.Fatalf("the fixture needs %q absent and %q and %q registered, so a drifted name reports the drift", tc.name, tc.want, tc.loser)
			}

			got := contract.NearestResource(tc.name)

			if got != tc.want {
				t.Errorf("NearestResource(%q) = %q, want %q (not %q)", tc.name, got, tc.want, tc.loser)
			}
		})
	}
}

// A caller quotes the suggestion to a block author as a name that validates.
func TestNearestResource_AlwaysReturnsANameTheRegistryResolves(t *testing.T) {
	for _, name := range []string{"", "x", "noSuchOutput", "Region", "HOSTNAME", "cloud.regoin"} {
		t.Run(name, func(t *testing.T) {
			got := contract.NearestResource(name)

			if _, ok := contract.LookupResource(got); !ok {
				t.Errorf("NearestResource(%q) = %q, which the registry does not resolve", name, got)
			}
		})
	}
}
