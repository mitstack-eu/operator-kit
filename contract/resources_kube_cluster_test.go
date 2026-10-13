package contract_test

// The Hetzner k3s cluster block registers six outputs and reuses cloud.region and host.type as they stand.

import (
	"reflect"
	"regexp"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// kubeClusterOutputs are the six outputs the kube-cluster block registers.
var kubeClusterOutputs = []string{
	"privateEndpoint",
	"issuerUrl",
	"issuerBucket",
	"clusterRepoUrl",
	"serverId",
	"enrolmentTokenSha256",
}

func TestLookupResource_KubeClusterOutputsAreNonSecretEntriesWithNoAliases(t *testing.T) {
	for _, name := range kubeClusterOutputs {
		r, ok := contract.LookupResource(name)
		if !ok {
			t.Errorf("LookupResource(%q) found nothing", name)
			continue
		}
		if r.Name != name {
			t.Errorf("LookupResource(%q).Name = %q, want the entry of that name", name, r.Name)
		}
		if r.Secret {
			t.Errorf("%q is secret, want it not secret", name)
		}
		if len(r.Aliases) != 0 {
			t.Errorf("%q: aliases = %q, want none", name, r.Aliases)
		}
	}
}

// Each pattern is the form the block's README gives the published value.
func TestResources_KubeClusterExamplesTakeThePublishedShape(t *testing.T) {
	shapes := map[string]*regexp.Regexp{
		"issuerBucket":         regexp.MustCompile(`^mitstack-oidc-[a-z2-7]{26}$`),
		"enrolmentTokenSha256": regexp.MustCompile(`^[a-z2-7]{52}$`),
		"serverId":             regexp.MustCompile(`^[0-9]+$`),
		"privateEndpoint":      regexp.MustCompile(`^https://`),
		"issuerUrl":            regexp.MustCompile(`^https://`),
		"clusterRepoUrl":       regexp.MustCompile(`^https://`),
	}
	for name, shape := range shapes {
		r, ok := contract.LookupResource(name)
		if !ok {
			t.Errorf("LookupResource(%q) found nothing", name)
			continue
		}
		if !shape.MatchString(r.Example) {
			t.Errorf("%q: example %q does not match %s", name, r.Example, shape)
		}
	}
}

func TestLookupResource_LocationAndServerTypeResolveToNothing(t *testing.T) {
	for _, name := range []string{"location", "serverType"} {
		if r, ok := contract.LookupResource(name); ok {
			t.Errorf("LookupResource(%q) found %q, want not found: the block publishes these as cloud.region and host.type", name, r.Name)
		}
	}
}

// The two entries the kube-cluster block reuses, which must not change for it.
func TestResources_CloudRegionAndHostTypeAreUnchanged(t *testing.T) {
	want := []contract.ResourceEntry{
		{
			Name:    "cloud.region",
			Kind:    "Host & network",
			Meaning: "The cloud region the app runs in.",
			Shape:   "cloud region code",
			Example: "eu-central-1",
			Secret:  false,
			Aliases: []string{"region"},
		},
		{
			Name:    "host.type",
			Kind:    "Host & network",
			Meaning: "The machine type the app runs on.",
			Shape:   "cloud instance type",
			Example: "t3.small",
			Secret:  false,
			Aliases: []string{"instanceType"},
		},
	}
	for _, w := range want {
		got, ok := contract.LookupResource(w.Name)
		if !ok {
			t.Errorf("LookupResource(%q) found nothing", w.Name)
			continue
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("LookupResource(%q) = %+v, want %+v", w.Name, got, w)
		}
	}
}
