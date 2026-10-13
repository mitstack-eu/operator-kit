package kittest_test

// absence_test.go — ADR 0067 decision 3's kit-side conformance check: a
// block that declares spec.lifecycle.absenceProbe: true must answer
// validate for a torn-down resource with an explicit "absent" boolean
// marker, in the shape of response-goldens/absent.json. CheckAbsenceProbe is
// the pure judge; AssertAbsenceProbe is the *testing.T wrapper an operator's
// own contract test calls, mirroring CheckResponse/AssertEmitsConformingResponses's
// split in response.go.
//
// The kit module has no third-party dependencies, so the manifest fixtures
// below are read as plain text: the declaration is a line whose trimmed
// text is exactly "absenceProbe: true", anywhere in the document — no YAML
// library involved.

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

// TestAssertAbsenceProbe_PassesForADeclaringManifestWithTheGoldenReply proves
// the *testing.T wrapper exists with the brief's exact signature and
// delegates cleanly for the conforming case — a failing call is exercised
// indirectly through CheckAbsenceProbe's own problem-space tests above,
// mirroring response_test.go's split between CheckResponse (thoroughly
// tested) and AssertEmitsConformingResponses (only proven to pass clean).
func TestAssertAbsenceProbe_PassesForADeclaringManifestWithTheGoldenReply(t *testing.T) {
	golden := kittest.ValidResponseGoldens(t)["absent"]
	kittest.AssertAbsenceProbe(t, []byte(declaringManifest), golden)
}

// TestAssertAbsenceProbe_PassesForAnUndeclaredManifestWithAnyReply: an
// undeclared manifest owes nothing, so AssertAbsenceProbe must never fail t
// for it, whatever the reply looks like.
func TestAssertAbsenceProbe_PassesForAnUndeclaredManifestWithAnyReply(t *testing.T) {
	kittest.AssertAbsenceProbe(t, []byte(undeclaredManifest), []byte(`{}`))
}
