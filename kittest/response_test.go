package kittest

import (
	"strings"
	"testing"
)

// The engine's decoder discards its unmarshal error, so a response with numeric outputs loses the whole payload in silence, and CheckResponse must not accept everything.

// invalidGoldenField is explicit rather than derived from the file name so that a new invalid golden forces a decision about what its problem text must name.
var invalidGoldenField = map[string]string{
	"invalid-outputs-not-strings":        "outputs",
	"invalid-outputs-not-object":         "outputs",
	"invalid-pending-not-bool":           "pending",
	"invalid-absent-not-bool":            "absent",
	"invalid-secret-outputs-not-strings": "secretOutputs",

	"invalid-resource-secret-outputs-not-object":       "resourceSecretOutputs",
	"invalid-resource-secret-outputs-entry-not-object": "resourceSecretOutputs",
	"invalid-resource-secret-outputs-value-not-string": "resourceSecretOutputs",
}

func TestCheckResponse_AcceptsEveryValidResponseGolden(t *testing.T) {
	for name, raw := range ValidResponseGoldens(t) {
		t.Run(name, func(t *testing.T) {
			if problems := CheckResponse(raw); len(problems) != 0 {
				t.Errorf("CheckResponse rejected the %s response golden with %v — "+
					"the goldens ARE the contract, so a valid one the checker refuses "+
					"means sixteen shipping operators are about to be failed by a rule "+
					"the contract never made.\n%s", name, problems, raw)
			}
		})
	}
}

func TestCheckResponse_RejectsEveryInvalidResponseGoldenNamingTheOffendingField(t *testing.T) {
	invalid := InvalidResponseGoldens(t)

	for name, raw := range invalid {
		field, known := invalidGoldenField[name]
		if !known {
			t.Fatalf("invalid response golden %q has no expected field in invalidGoldenField — "+
				"a new malformed fixture must say which property its problem text has to name, "+
				"or this test starts accepting any complaint at all", name)
		}

		t.Run(name, func(t *testing.T) {
			problems := CheckResponse(raw)
			if len(problems) == 0 {
				t.Fatalf("CheckResponse ACCEPTED %s — the contract marks this response "+
					"malformed, and accepting it is how a payload the engine cannot decode "+
					"reaches the engine.\n%s", name, raw)
			}
			joined := strings.Join(problems, "; ")
			if !strings.Contains(joined, field) {
				t.Errorf("CheckResponse rejected %s but its problem text never names %q: %v\n"+
					"An operator author reads this string and nothing else; a complaint that "+
					"does not name the field is a complaint they cannot act on.", name, field, problems)
			}
		})
	}
}

func TestResponseGoldens_BothHalvesArePresent(t *testing.T) {
	valid := ValidResponseGoldens(t)
	invalid := InvalidResponseGoldens(t)

	if len(valid) == 0 {
		t.Fatal("ValidResponseGoldens returned nothing — the acceptance half of this harness would pass vacuously")
	}
	if len(invalid) == 0 {
		t.Fatal("InvalidResponseGoldens returned nothing — the rejection half of this harness would pass vacuously")
	}

	for name := range valid {
		if strings.HasPrefix(name, "invalid-") {
			t.Errorf("ValidResponseGoldens returned %q — names prefixed invalid- are the malformed set", name)
		}
	}
	for name := range invalid {
		if !strings.HasPrefix(name, "invalid-") {
			t.Errorf("InvalidResponseGoldens returned %q — the malformed set is the invalid- prefixed names only", name)
		}
	}

	// null-outputs keeps the contract additive: shipping operators emit a nil map as JSON null.
	if _, ok := valid["null-outputs"]; !ok {
		t.Error("the valid set is missing null-outputs, the fixture that pins a null outputs map as legitimate")
	}
}

func TestCheckResponse_AcceptsAValidResponseCarryingAnUnknownProperty(t *testing.T) {
	// An unknown top-level property is accepted so the contract can be extended.
	const withUnknown = `{"outputs":{"url":"https://app.example.com"},"somethingTheEngineWillAddLater":{"nested":1}}`

	if problems := CheckResponse([]byte(withUnknown)); len(problems) != 0 {
		t.Errorf("CheckResponse rejected a conforming response carrying an unknown property: %v\n%s",
			problems, withUnknown)
	}
}
