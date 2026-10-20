package kittest_test

// A block declaring spec.lifecycle.absenceProbe: true must answer validate for a torn-down resource with an explicit absent marker.

import (
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

const declaringManifest = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: declaring-block
  name: Declaring Block
spec:
  layer: infra
  lifecycle:
    absenceProbe: true
`

const undeclaredManifest = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: quiet-block
  name: Quiet Block
spec:
  layer: infra
`

func TestCheckAbsenceProbe(t *testing.T) {
	golden := kittest.ValidResponseGoldens(t)["absent"]
	if len(golden) == 0 {
		t.Fatal("the absent response golden must exist and be non-empty for this test to prove anything")
	}
	cases := []struct {
		name, manifest string
		reply          []byte
		wantProblems   bool
	}{
		{"a declaring manifest answering with the absent golden has no problem", declaringManifest, golden, false},
		{"a declaring manifest's reply with no absent key is flagged", declaringManifest, []byte(`{}`), true},
		{"a declaring manifest's reply with a non-boolean absent is flagged", declaringManifest, []byte(`{"absent":"yes"}`), true},
		{"a declaring manifest's reply that is no JSON object is flagged", declaringManifest, []byte(`[1,2,3]`), true},
		{"an undeclared manifest owes nothing for an empty object", undeclaredManifest, []byte(`{}`), false},
		{"an undeclared manifest owes nothing for the absent golden", undeclaredManifest, golden, false},
		{"an undeclared manifest owes nothing for a non-object", undeclaredManifest, []byte(`[1,2,3]`), false},
		{"an undeclared manifest owes nothing for a non-boolean absent", undeclaredManifest, []byte(`{"absent":"yes"}`), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := kittest.CheckAbsenceProbe([]byte(tc.manifest), tc.reply)

			if tc.wantProblems != (len(problems) > 0) || (tc.manifest == undeclaredManifest && problems != nil) {
				t.Errorf("CheckAbsenceProbe(reply %s) = %#v, want problems %v (nil for an undeclared manifest)", tc.reply, problems, tc.wantProblems)
			}
		})
	}
}

func TestAssertAbsenceProbe_PassesForADeclaringManifestWithTheGoldenReply(t *testing.T) {
	golden := kittest.ValidResponseGoldens(t)["absent"]
	kittest.AssertAbsenceProbe(t, []byte(declaringManifest), golden)
}

func TestAssertAbsenceProbe_PassesForAnUndeclaredManifestWithAnyReply(t *testing.T) {
	kittest.AssertAbsenceProbe(t, []byte(undeclaredManifest), []byte(`{}`))
}
