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

func TestCheckAbsenceProbe_NoProblemsForADeclaringManifestWithTheGoldenReply(t *testing.T) {
	golden := kittest.ValidResponseGoldens(t)["absent"]
	if len(golden) == 0 {
		t.Fatal("the absent response golden must exist and be non-empty for this test to prove anything")
	}
	if problems := kittest.CheckAbsenceProbe([]byte(declaringManifest), golden); len(problems) != 0 {
		t.Errorf("a declaring manifest answering with the absent.json golden must have no problems, got %v", problems)
	}
}

func TestCheckAbsenceProbe_ProblemForADeclaringManifestWithAnEmptyObjectReply(t *testing.T) {
	problems := kittest.CheckAbsenceProbe([]byte(declaringManifest), []byte(`{}`))
	if len(problems) == 0 {
		t.Fatal("a declaring manifest's validate reply with no absent key must be flagged")
	}
}

func TestCheckAbsenceProbe_ProblemForADeclaringManifestWithAbsentNotABoolean(t *testing.T) {
	problems := kittest.CheckAbsenceProbe([]byte(declaringManifest), []byte(`{"absent":"yes"}`))
	if len(problems) == 0 {
		t.Fatal("a declaring manifest's validate reply with a non-boolean absent value must be flagged")
	}
}

func TestCheckAbsenceProbe_ProblemForADeclaringManifestWithANonObjectReply(t *testing.T) {
	problems := kittest.CheckAbsenceProbe([]byte(declaringManifest), []byte(`[1,2,3]`))
	if len(problems) == 0 {
		t.Fatal("a declaring manifest's validate reply that is not a JSON object must be flagged")
	}
}

func TestCheckAbsenceProbe_NilForAnUndeclaredManifestWhateverTheReply(t *testing.T) {
	cases := map[string][]byte{
		"empty object":    []byte(`{}`),
		"absent golden":   kittest.ValidResponseGoldens(t)["absent"],
		"not an object":   []byte(`[1,2,3]`),
		"absent not bool": []byte(`{"absent":"yes"}`),
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			if problems := kittest.CheckAbsenceProbe([]byte(undeclaredManifest), reply); problems != nil {
				t.Errorf("an undeclared manifest owes nothing, got %v for reply %s", problems, reply)
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
