package contract_test

// resource_type_request_test.go pins the request half of serving a resource
// the manifest no longer declares: a request naming one resource may carry
// its recorded type under "resourceType", a string, and Desired carries it
// across to the block. A type with no resource to bind it to is refused by
// DecodeRequest and by the envelope schema, which raises its version one
// minor for the new field.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/contract"
)

// namedResourceType is the recorded type of the resource a request names.
const namedResourceType = "aws.security-group"

// envelopeSchemaDoc decodes contract.EnvelopeSchema.
func envelopeSchemaDoc(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(contract.EnvelopeSchema, &doc); err != nil {
		t.Fatalf("contract.EnvelopeSchema is not valid JSON: %v", err)
	}
	return doc
}

// envelopeSchemaRefusals checks raw against the envelope schema's top-level
// required, dependentRequired and declared property types, the keywords a
// request field's presence and shape turn on, and returns one refusal for
// each it breaks.
func envelopeSchemaRefusals(t *testing.T, raw []byte) []string {
	t.Helper()
	doc := envelopeSchemaDoc(t)
	var instance map[string]any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("request is not a JSON object: %v", err)
	}

	var refusals []string
	required, _ := doc["required"].([]any)
	for _, r := range required {
		if _, ok := instance[r.(string)]; !ok {
			refusals = append(refusals, fmt.Sprintf("required %q is missing", r))
		}
	}
	dependent, _ := doc["dependentRequired"].(map[string]any)
	for field, needs := range dependent {
		if _, present := instance[field]; !present {
			continue
		}
		for _, n := range needs.([]any) {
			if _, ok := instance[n.(string)]; !ok {
				refusals = append(refusals, fmt.Sprintf("%q needs %q", field, n))
			}
		}
	}
	props := schemaObject(t, doc, "", "properties")
	for field, value := range instance {
		node, declared := props[field].(map[string]any)
		if !declared || len(schemaTypes(node)) == 0 {
			continue
		}
		if !hasType(node, jsonType(value)) {
			refusals = append(refusals, fmt.Sprintf("%q is a %s, want %v", field, jsonType(value), schemaTypes(node)))
		}
	}
	return refusals
}

// jsonType is the JSON Schema type name of a value encoding/json decoded.
func jsonType(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case nil:
		return "null"
	}
	return "unknown"
}

// namingResourceWithType mutates the subscribe golden into an unsubscribe
// naming firewall with its recorded type and stored identity.
func namingResourceWithType(m map[string]any) {
	m["operation"] = "unsubscribe"
	m["resource"] = "firewall"
	m["resourceType"] = namedResourceType
	m["resourceIdentity"] = namedResourceIdentity
}

func TestDecodeRequest_ReadsTheNamedResourcesRecordedTypeBackOnDesired(t *testing.T) {
	req, err := contract.DecodeRequest(mutateSubscribeGolden(t, namingResourceWithType))
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	d := req.Desired()
	if d.ResourceType != namedResourceType {
		t.Errorf("Desired().ResourceType = %q, want %q", d.ResourceType, namedResourceType)
	}
	if d.Resource != "firewall" || !reflect.DeepEqual(d.ResourceIdentity, namedResourceIdentity) {
		t.Errorf("Desired() = Resource %q, ResourceIdentity %v, want firewall and %v", d.Resource, d.ResourceIdentity, namedResourceIdentity)
	}
}

func TestDecodeRequest_RefusesAResourceTypeWithNoResourceNamingBothFields(t *testing.T) {
	cases := map[string]func(m map[string]any){
		"no resource":    func(m map[string]any) { m["resourceType"] = namedResourceType },
		"empty resource": func(m map[string]any) { m["resource"] = ""; m["resourceType"] = namedResourceType },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := contract.DecodeRequest(mutateSubscribeGolden(t, mutate))
			if err == nil {
				t.Fatal("DecodeRequest accepted a resource type with no resource to bind it to, want it refused")
			}
			for _, field := range []string{`"resourceType"`, `"resource"`} {
				if !strings.Contains(err.Error(), field) {
					t.Errorf("DecodeRequest error = %q, want it to name the field %s", err.Error(), field)
				}
			}
		})
	}
}

func TestEnvelopeSchema_DeclaresResourceTypeAsAnOptionalStringThatNeedsAResource(t *testing.T) {
	doc := envelopeSchemaDoc(t)
	if req, ok := doc["required"].([]any); ok {
		for _, r := range req {
			if r == "resourceType" {
				t.Error(`envelope.schema.json requires "resourceType", but a request naming no resource must stay valid`)
			}
		}
	}

	props := schemaObject(t, doc, "", "properties")
	typ, ok := props["resourceType"].(map[string]any)
	if !ok {
		t.Fatal(`envelope.schema.json declares no "resourceType" property`)
	}
	if !hasType(typ, "string") {
		t.Errorf("properties.resourceType type = %v, want string", schemaTypes(typ))
	}

	dependent := schemaObject(t, doc, "", "dependentRequired")
	needs, ok := dependent["resourceType"].([]any)
	if !ok || !reflect.DeepEqual(needs, []any{"resource"}) {
		t.Errorf(`dependentRequired.resourceType = %v, want ["resource"]`, dependent["resourceType"])
	}
}

func TestEnvelopeSchema_AcceptsARequestNamingAResourceWithItsRecordedType(t *testing.T) {
	if refusals := envelopeSchemaRefusals(t, mutateSubscribeGolden(t, namingResourceWithType)); len(refusals) != 0 {
		t.Errorf("envelope.schema.json refuses a request naming firewall with its recorded type: %q", refusals)
	}
	if _, ok := schemaObject(t, envelopeSchemaDoc(t), "", "properties")["resourceType"]; !ok {
		t.Error(`envelope.schema.json accepts "resourceType" only as an undeclared extra, want it declared`)
	}
}

func TestEnvelopeSchema_RefusesAResourceTypeWithNoResource(t *testing.T) {
	raw := mutateSubscribeGolden(t, func(m map[string]any) { m["resourceType"] = namedResourceType })
	refusals := envelopeSchemaRefusals(t, raw)
	want := `"resourceType" needs "resource"`
	for _, r := range refusals {
		if r == want {
			return
		}
	}
	t.Errorf("envelope.schema.json refusals = %q, want %s", refusals, want)
}

func TestEnvelopeSchema_VersionIsOneMinorAboveTheResourceIdentityRelease(t *testing.T) {
	if v, _ := envelopeSchemaDoc(t)["version"].(string); v != "1.3.0" {
		t.Errorf("envelope.schema.json version = %q, want %q: resourceType is a new optional field", v, "1.3.0")
	}
}
