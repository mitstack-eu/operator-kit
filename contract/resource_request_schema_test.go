package contract_test

// The envelope declares "resource" optional, and the response declares an ensure's identity as an object of strings.

import (
	"encoding/json"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

func TestEnvelopeSchema_DeclaresResourceAsAnOptionalString(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contract.EnvelopeSchema, &doc); err != nil {
		t.Fatalf("contract.EnvelopeSchema is not valid JSON: %v", err)
	}
	if req, ok := doc["required"].([]any); ok {
		for _, r := range req {
			if r == "resource" {
				t.Errorf("envelope.schema.json requires %q, but a request naming no resource must stay valid", r)
			}
		}
	}

	props := schemaObject(t, doc, "", "properties")
	resource, ok := props["resource"].(map[string]any)
	if !ok {
		t.Fatal(`envelope.schema.json declares no "resource" property`)
	}
	if !hasType(resource, "string") {
		t.Errorf("properties.resource type = %v, want string", schemaTypes(resource))
	}
}

func TestResponseSchema_DeclaresIdentityOnTheEnsureOutcomeAsAnObjectOfStrings(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contract.ResponseSchema, &doc); err != nil {
		t.Fatalf("contract.ResponseSchema is not valid JSON: %v", err)
	}
	props := schemaObject(t, doc, "", "properties")
	resources := schemaObject(t, props, "properties", "resources")
	outcome := schemaObject(t, resources, "properties.resources", "additionalProperties")
	calls := schemaObject(t, outcome, "properties.resources.additionalProperties", "properties")
	ensure := schemaObject(t, calls, "properties.resources.additionalProperties.properties", "ensure")
	fields := schemaObject(t, ensure, "the ensure outcome", "properties")

	identity, ok := fields["identity"].(map[string]any)
	if !ok {
		t.Fatal(`the ensure outcome in response.schema.json declares no "identity" field`)
	}
	if !hasType(identity, "object") {
		t.Errorf("ensure identity type = %v, want object", schemaTypes(identity))
	}
	values := schemaObject(t, identity, "the ensure identity", "additionalProperties")
	if !hasType(values, "string") {
		t.Errorf("ensure identity values type = %v, want string", schemaTypes(values))
	}
}
