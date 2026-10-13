package contract_test

// resources_test.go pins how the resource registry the kit owns behaves: the
// output names a block may declare in spec.outputs and read under
// spec.requires[].reads. It checks properties every entry must hold, never the
// registry's content, so adding an entry never means editing this file.

import (
	"reflect"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// nineKinds is the closed kind vocabulary, as the labels the console shows.
var nineKinds = map[string]bool{
	"App": true, "Host & network": true, "Data": true, "Build & deploy": true, "Access": true,
	"Model": true, "Coding agent": true, "Work": true, "Source": true,
}

// registry returns contract.Resources(), failing the test when it is empty so
// a check over every entry cannot pass by having none to check.
func registry(t *testing.T) []contract.ResourceEntry {
	t.Helper()
	rs := contract.Resources()
	if len(rs) == 0 {
		t.Fatal("contract.Resources() is empty")
	}
	return rs
}

func TestResources_EveryEntryIsComplete(t *testing.T) {
	for _, r := range registry(t) {
		if !nineKinds[r.Kind] {
			t.Errorf("%q: kind %q is not one of the nine labels", r.Name, r.Kind)
		}
		if r.Meaning == "" {
			t.Errorf("%q has no meaning", r.Name)
		}
		if r.Shape == "" {
			t.Errorf("%q has no shape", r.Name)
		}
		if !r.Secret && r.Example == "" {
			t.Errorf("%q is not secret and has no example", r.Name)
		}
	}
}

func TestResources_AliasesAreNeverNil(t *testing.T) {
	for _, r := range registry(t) {
		if r.Aliases == nil {
			t.Errorf("%q: Aliases is nil, want an empty list when the entry has none", r.Name)
		}
	}
}

func TestResources_AreSortedByNameWithEachNameOnce(t *testing.T) {
	rs := registry(t)
	for i := 1; i < len(rs); i++ {
		if rs[i-1].Name >= rs[i].Name {
			t.Errorf("entry %d (%q) must sort strictly before entry %d (%q)", i-1, rs[i-1].Name, i, rs[i].Name)
		}
	}
}

func TestResources_NoAliasEqualsAnEntryName(t *testing.T) {
	names := map[string]bool{}
	for _, r := range registry(t) {
		names[r.Name] = true
	}
	for _, r := range registry(t) {
		for _, a := range r.Aliases {
			if names[a] {
				t.Errorf("%q: alias %q is also an entry's name", r.Name, a)
			}
		}
	}
}

func TestResources_SecretEntriesCarryNoExample(t *testing.T) {
	for _, r := range registry(t) {
		if r.Secret && r.Example != "" {
			t.Errorf("secret entry %q carries example %q, want none, since the example would show the secret", r.Name, r.Example)
		}
	}
}

func TestLookupResource_RegionResolvesToTheCloudRegionEntry(t *testing.T) {
	r, ok := contract.LookupResource("region")
	if !ok {
		t.Fatal(`LookupResource("region") found nothing`)
	}
	if r.Name != "cloud.region" {
		t.Errorf(`LookupResource("region").Name = %q, want "cloud.region"`, r.Name)
	}
}

func TestLookupResource_InstanceTypeResolvesToTheHostTypeEntry(t *testing.T) {
	r, ok := contract.LookupResource("instanceType")
	if !ok {
		t.Fatal(`LookupResource("instanceType") found nothing`)
	}
	if r.Name != "host.type" {
		t.Errorf(`LookupResource("instanceType").Name = %q, want "host.type"`, r.Name)
	}
}

func TestLookupResource_FindsEveryEntryByItsNameAndEachAlias(t *testing.T) {
	rs := contract.Resources()
	if len(rs) == 0 {
		t.Fatal("contract.Resources() is empty")
	}
	for _, r := range rs {
		byName, ok := contract.LookupResource(r.Name)
		if !ok || !reflect.DeepEqual(byName, r) {
			t.Errorf("LookupResource(%q) = %+v, %v; want the entry %+v", r.Name, byName, ok, r)
		}
		for _, a := range r.Aliases {
			byAlias, ok := contract.LookupResource(a)
			if !ok || !reflect.DeepEqual(byAlias, r) {
				t.Errorf("LookupResource(%q) = %+v, %v; want the entry %+v", a, byAlias, ok, r)
			}
		}
	}
}

func TestLookupResource_UnknownOrMiscasedNameIsNotFound(t *testing.T) {
	for _, name := range []string{"", "noSuchOutput", "Region", "cloud.Region", "Host"} {
		if r, ok := contract.LookupResource(name); ok {
			t.Errorf("LookupResource(%q) found %q, want not found (the match is case-sensitive)", name, r.Name)
		}
	}
}

func TestResources_ReturnsCopiesACallerCanMutate(t *testing.T) {
	before := contract.Resources()
	snapshot := make([]contract.ResourceEntry, len(before))
	for i, r := range before {
		r.Aliases = append([]string{}, r.Aliases...)
		snapshot[i] = r
	}

	got := contract.Resources()
	if len(got) == 0 {
		t.Fatal("contract.Resources() is empty")
	}
	for i := range got {
		for j := range got[i].Aliases {
			got[i].Aliases[j] = "mutated"
		}
		got[i].Name = "mutated"
	}

	if after := contract.Resources(); !reflect.DeepEqual(after, snapshot) {
		t.Error("mutating what Resources returned changed a later call's result")
	}
}

func TestLookupResource_ReturnsACopyACallerCanMutate(t *testing.T) {
	first, ok := contract.LookupResource("cloud.region")
	if !ok {
		t.Fatal(`LookupResource("cloud.region") found nothing`)
	}
	if len(first.Aliases) == 0 {
		t.Fatalf("cloud.region needs an alias for this test to mutate, got %+v", first)
	}
	want := first
	want.Aliases = append([]string{}, first.Aliases...)

	first.Aliases[0] = "mutated"

	if again, _ := contract.LookupResource("cloud.region"); !reflect.DeepEqual(again, want) {
		t.Errorf("mutating a looked-up entry changed the registry: a later lookup returned %+v, want %+v", again, want)
	}
}
