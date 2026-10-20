package contract_test

import (
	"io/fs"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

func TestManifestGoldenAccessors_ReturnTheEmbeddedGoldens(t *testing.T) {
	cases := []struct {
		file string
		got  func() []byte
	}{
		{"lifecycle-hooks.yaml", contract.HooksManifestGolden},
		{"lifecycle-hooks-type-block.yaml", contract.TypeBlockHooksManifestGolden},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := fs.ReadFile(contract.ManifestGoldens, contract.ManifestGoldensDir+"/"+tc.file)

			if err != nil {
				t.Fatalf("reading %s from contract.ManifestGoldens: %v", tc.file, err)
			}
			if len(raw) == 0 || string(raw) != string(tc.got()) {
				t.Errorf("the accessor for %s returns %q, want the embedded file %q", tc.file, tc.got(), raw)
			}
		})
	}
}

func TestHookNameLists_AreFixedAndReturnACopy(t *testing.T) {
	cases := []struct {
		label string
		list  func() []string
		want  []string
	}{
		{"LifecycleHookPoints, the four in order", contract.LifecycleHookPoints, []string{"beforeEnsure", "afterEnsure", "beforeDelete", "afterDelete"}},
		{"HooksManifestGoldenDeclares", contract.HooksManifestGoldenDeclares, []string{"beforeEnsure", "afterDelete"}},
	}
	for _, tc := range cases {
		t.Run(tc.label+"/holds its names", func(t *testing.T) {
			got := tc.list()

			wantField(t, tc.label, got, tc.want)
		})
		t.Run(tc.label+"/returns a copy", func(t *testing.T) {
			first := tc.list()
			if len(first) == 0 {
				t.Fatal("the list is empty")
			}

			first[0] = "mutated"

			if got := tc.list(); got[0] != tc.want[0] {
				t.Errorf("mutating the returned slice changed the next call: got[0] = %q, want %q", got[0], tc.want[0])
			}
		})
	}
}

func hooksOfGolden(t *testing.T, raw []byte) *contract.V1AlphaManifest {
	t.Helper()
	a := parseAlpha(t, string(raw))
	if a.Spec.Lifecycle == nil {
		t.Fatalf("the golden declares no spec.lifecycle:\n%s", raw)
	}
	return a
}

func TestHooksManifestGoldens_DeclareTheAccessorsHooks(t *testing.T) {
	t.Run("every name the accessor declares is a hook point", func(t *testing.T) {
		known := map[string]bool{}
		for _, p := range contract.LifecycleHookPoints() {
			known[p] = true
		}

		for _, name := range contract.HooksManifestGoldenDeclares() {
			if !known[name] {
				t.Errorf("HooksManifestGoldenDeclares() names %q, which is not in contract.LifecycleHookPoints() %v", name, contract.LifecycleHookPoints())
			}
		}
	})
	for file, raw := range map[string][]byte{"the hooks golden": contract.HooksManifestGolden(), "its type: block twin": contract.TypeBlockHooksManifestGolden()} {
		t.Run(file, func(t *testing.T) {
			got := hooksOfGolden(t, raw).Spec.Lifecycle.Hooks

			wantField(t, file+"'s spec.lifecycle.hooks", got, contract.HooksManifestGoldenDeclares())
		})
	}
}

// The twin carries everything the kind-named golden does, but its kind: and layer:, and adds the type: block, format: and store: spellings.
func TestTypeBlockHooksManifestGolden_IsTheHooksGoldenInTheNewNames(t *testing.T) {
	hooks, twin := hooksOfGolden(t, contract.HooksManifestGolden()), hooksOfGolden(t, contract.TypeBlockHooksManifestGolden())

	hooks.Spec.Layer = ""
	twin.Spec.Provides, twin.Spec.ProvidedContracts, twin.Spec.Continuity = nil, nil, nil

	wantField(t, "the twin without its additions", *twin, *hooks)
}
