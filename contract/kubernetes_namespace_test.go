package contract_test

// kubernetes_namespace_test.go pins the two registry entries a namespace a
// cluster block places in an app's namespace publishes.

import (
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

func TestLookupResource_NamespaceAndServiceAccountAreNonSecretCanonicalEntriesWithNoAliases(t *testing.T) {
	for _, c := range []struct{ name, kind string }{
		{"namespace", "Host & network"},
		{"serviceAccount", "Access"},
	} {
		r, ok := contract.LookupResource(c.name)
		if !ok {
			t.Errorf("LookupResource(%q) found nothing", c.name)
			continue
		}
		if r.Name != c.name {
			t.Errorf("LookupResource(%q).Name = %q, want its own canonical entry", c.name, r.Name)
		}
		if r.Kind != c.kind {
			t.Errorf("%q: Kind = %q, want %q", c.name, r.Kind, c.kind)
		}
		if r.Secret {
			t.Errorf("%q is secret, want it not secret", c.name)
		}
		if len(r.Aliases) != 0 {
			t.Errorf("%q: aliases = %q, want none", c.name, r.Aliases)
		}
	}
}
