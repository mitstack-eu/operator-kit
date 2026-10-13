package contract_test

// manifest_warnings_test.go pins Manifest.Warnings: contract.ParseManifest
// reports each retired key a mitstack/v1alpha manifest still carries as one
// ManifestWarning naming its path, with the platform's own message, and
// reports nothing for a manifest of either version that carries none.

import (
	"reflect"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// dependsOnWarning is the warning ParseManifest gives for spec.dependsOn.
var dependsOnWarning = contract.ManifestWarning{
	Path:    "spec.dependsOn",
	Message: "spec.dependsOn is retired and was ignored; declare the dependency with spec.requires as a capability the target provides",
}

// optionalWarning is the warning ParseManifest gives for a requirement on
// capability that carries optional: true.
func optionalWarning(capability string) contract.ManifestWarning {
	return contract.ManifestWarning{
		Path: "spec.requires[" + capability + "].optional",
		Message: "the optional flag on spec.requires[" + capability + "] is retired and its value is still honoured; " +
			"a requires entry either names a capability the block needs or leaves it out, " +
			"so drop the entry rather than the flag when the block runs without a provider",
	}
}

// parseManifest parses raw and fails the test when it is refused.
func parseManifest(t *testing.T, raw string) contract.Manifest {
	t.Helper()
	m, err := contract.ParseManifest([]byte(raw))
	if err != nil {
		t.Fatalf("ParseManifest: %v\n%s", err, raw)
	}
	return m
}

// A v1alpha manifest carrying spec.dependsOn parses, and Warnings holds
// exactly one warning, naming spec.dependsOn.
func TestParseManifest_V1AlphaWarnsOnceThatDependsOnIsRetired(t *testing.T) {
	for label, lines := range map[string]string{
		"a block list": "  dependsOn:\n    - weather-app\n    - postgres-db\n",
		"a flow list":  "  dependsOn: [weather-app]\n",
	} {
		t.Run(label, func(t *testing.T) {
			m := parseManifest(t, alphaHead+lines+"  layer: backend\n")
			want := []contract.ManifestWarning{dependsOnWarning}
			if !reflect.DeepEqual(m.Warnings, want) {
				t.Errorf("Warnings = %#v, want %#v", m.Warnings, want)
			}
		})
	}
}

// A v1alpha requirement carrying optional: true gives one warning keyed by
// its capability, not its index, and keeps Optional true. A requirement
// whose optional is false or absent gives none.
func TestParseManifest_V1AlphaWarnsOnAnOptionalRequirementByItsCapability(t *testing.T) {
	m := parseManifest(t, alphaHead+`  requires:
    - capability: repo
      reads: [repo]
    - capability: app-host
      optional: true
    - capability: database
      optional: false
`)
	want := []contract.ManifestWarning{optionalWarning("app-host")}
	if !reflect.DeepEqual(m.Warnings, want) {
		t.Errorf("Warnings = %#v, want %#v", m.Warnings, want)
	}
	if m.V1Alpha == nil {
		t.Fatalf("ParseManifest gave no V1Alpha for a %s manifest: %+v", contract.ManifestAPIVersionV1Alpha, m)
	}
	requireLen(t, "spec.requires", m.V1Alpha.Spec.Requires, 3)
	wantField(t, "spec.requires[1].capability", m.V1Alpha.Spec.Requires[1].Capability, "app-host")
	wantField(t, "spec.requires[1].optional", m.V1Alpha.Spec.Requires[1].Optional, true)
	requireLen(t, "Requires", m.V1Alpha.Requires, 3)
	wantField(t, "Requires[1].Optional", m.V1Alpha.Requires[1].Optional, true)
}

// Each optional requirement gives its own warning, keyed by its own
// capability.
func TestParseManifest_V1AlphaWarnsOncePerOptionalRequirement(t *testing.T) {
	m := parseManifest(t, alphaHead+`  requires:
    - capability: app-host
      optional: true
    - capability: database
      optional: true
`)
	want := []contract.ManifestWarning{optionalWarning("app-host"), optionalWarning("database")}
	if len(m.Warnings) != len(want) {
		t.Fatalf("Warnings = %#v, want the %d warnings %#v in any order", m.Warnings, len(want), want)
	}
	for _, w := range want {
		found := false
		for _, got := range m.Warnings {
			if got == w {
				found = true
			}
		}
		if !found {
			t.Errorf("Warnings = %#v, missing %#v", m.Warnings, w)
		}
	}
}

// A manifest of either version that carries no retired key parses with no
// warning.
func TestParseManifest_WarnsOfNothingWithoutARetiredKey(t *testing.T) {
	for label, raw := range map[string]string{
		contract.ManifestAPIVersionV1Alpha: alphaHead + "  layer: backend\n  requires:\n    - capability: repo\n      reads: [repo]\n",
		contract.ManifestAPIVersion:        betaManifest,
	} {
		t.Run(label, func(t *testing.T) {
			m := parseManifest(t, raw)
			if m.APIVersion != label {
				t.Fatalf("APIVersion = %q, want %q", m.APIVersion, label)
			}
			if len(m.Warnings) != 0 {
				t.Errorf("Warnings = %#v, want none for a manifest carrying no retired key", m.Warnings)
			}
		})
	}
}

// The optional warning names the requirement's capability as the manifest
// writes it, untrimmed, in both its path and its message, as the platform's
// warning does.
func TestParseManifest_V1AlphaOptionalWarningKeepsTheRawCapability(t *testing.T) {
	m := parseManifest(t, alphaHead+`  requires:
    - capability: " app-host "
      optional: true
`)
	want := []contract.ManifestWarning{optionalWarning(" app-host ")}
	if !reflect.DeepEqual(m.Warnings, want) {
		t.Errorf("Warnings = %#v, want %#v", m.Warnings, want)
	}
}

// The dependsOn warning comes first, then one optional warning per
// requirement in spec.requires order, whatever order the spec keys are
// written in.
func TestParseManifest_V1AlphaWarningsComeDependsOnFirstThenRequiresInOrder(t *testing.T) {
	m := parseManifest(t, alphaHead+`  requires:
    - capability: b-cap
      optional: true
    - capability: a-cap
      optional: true
  dependsOn: [weather-app]
`)
	want := []contract.ManifestWarning{dependsOnWarning, optionalWarning("b-cap"), optionalWarning("a-cap")}
	if !reflect.DeepEqual(m.Warnings, want) {
		t.Errorf("Warnings = %#v, want %#v", m.Warnings, want)
	}
}
