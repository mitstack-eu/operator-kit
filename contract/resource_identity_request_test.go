package contract_test

// resource_identity_request_test.go pins the request half of handing a named
// resource's observe what its ensure stored: a request naming one resource
// may carry the identity its ensure stored under "resourceIdentity", an
// object of strings, and
// Desired carries it across to the resource's handler. An identity with no
// resource to bind it to is refused, naming both fields, and the envelope
// schema declares the field optional and dependent on "resource". A request
// naming no resource decodes to no identity, so every request golden keeps
// the outcome request_goldens_pin_test.go pins for it.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// namedResourceIdentity is the identity the named resource's ensure stored.
var namedResourceIdentity = map[string]string{"groupId": "sg-0abc", "vpcId": "vpc-0abc"}

func TestDecodeRequest_ReadsTheNamedResourcesStoredIdentityBackOnDesired(t *testing.T) {
	raw := mutateSubscribeGolden(t, func(m map[string]any) {
		m["operation"] = "validate"
		m["resource"] = "firewall"
		m["resourceIdentity"] = namedResourceIdentity
	})

	req, err := contract.DecodeRequest(raw)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if !reflect.DeepEqual(req.ResourceIdentity, namedResourceIdentity) {
		t.Errorf("ResourceIdentity = %v, want %v", req.ResourceIdentity, namedResourceIdentity)
	}

	d := req.Desired()
	if d.Resource != "firewall" {
		t.Errorf("Desired().Resource = %q, want %q", d.Resource, "firewall")
	}
	if !reflect.DeepEqual(d.ResourceIdentity, namedResourceIdentity) {
		t.Errorf("Desired().ResourceIdentity = %v, want %v", d.ResourceIdentity, namedResourceIdentity)
	}
	if d.SubscriptionID != req.SubscriptionID || !reflect.DeepEqual(d.Params, req.Params) {
		t.Errorf("Desired() lost the subscription or params: %+v", d)
	}
}

func TestDecodeRequest_RefusesAResourceIdentityWithNoResourceNamingBothFields(t *testing.T) {
	cases := map[string]func(m map[string]any){
		"no resource":    func(m map[string]any) { m["resourceIdentity"] = namedResourceIdentity },
		"empty resource": func(m map[string]any) { m["resource"] = ""; m["resourceIdentity"] = namedResourceIdentity },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := contract.DecodeRequest(mutateSubscribeGolden(t, mutate))
			if err == nil {
				t.Fatal("DecodeRequest accepted a resource identity with no resource to bind it to, want it refused")
			}
			for _, field := range []string{`"resourceIdentity"`, `"resource"`} {
				if !strings.Contains(err.Error(), field) {
					t.Errorf("DecodeRequest error = %q, want it to name the field %s", err.Error(), field)
				}
			}
		})
	}
}

func TestDecodeRequest_EveryValidGoldenNamesNoResourceAndDecodesToNoIdentity(t *testing.T) {
	for _, name := range []string{"subscribe", "upgrade", "unsubscribe", "validate"} {
		t.Run(name, func(t *testing.T) {
			req := decodeGolden(t, name)
			if req.ResourceIdentity != nil {
				t.Errorf("%s names no resource, but decodes to ResourceIdentity %v", name, req.ResourceIdentity)
			}
			if d := req.Desired(); d.ResourceIdentity != nil {
				t.Errorf("%s names no resource, but Desired() carries ResourceIdentity %v", name, d.ResourceIdentity)
			}
		})
	}
}

func TestRequestMarshal_EncodesTheResourceIdentityUnderItsWireName(t *testing.T) {
	raw, err := json.Marshal(contract.Request{Resource: "firewall", ResourceIdentity: map[string]string{"groupId": "sg-0abc"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"resourceIdentity":{"groupId":"sg-0abc"}`) {
		t.Errorf("json.Marshal = %s, want it to carry %s", raw, `"resourceIdentity":{"groupId":"sg-0abc"}`)
	}
}

func TestEnvelopeSchema_DeclaresResourceIdentityAsAnOptionalObjectOfStringsThatNeedsAResource(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contract.EnvelopeSchema, &doc); err != nil {
		t.Fatalf("contract.EnvelopeSchema is not valid JSON: %v", err)
	}
	if req, ok := doc["required"].([]any); ok {
		for _, r := range req {
			if r == "resourceIdentity" {
				t.Error(`envelope.schema.json requires "resourceIdentity", but a request naming no resource must stay valid`)
			}
		}
	}

	props := schemaObject(t, doc, "", "properties")
	identity, ok := props["resourceIdentity"].(map[string]any)
	if !ok {
		t.Fatal(`envelope.schema.json declares no "resourceIdentity" property`)
	}
	if !hasType(identity, "object") {
		t.Errorf("properties.resourceIdentity type = %v, want object", schemaTypes(identity))
	}
	values := schemaObject(t, identity, "properties.resourceIdentity", "additionalProperties")
	if !hasType(values, "string") {
		t.Errorf("properties.resourceIdentity values type = %v, want string", schemaTypes(values))
	}

	dependent := schemaObject(t, doc, "", "dependentRequired")
	needs, ok := dependent["resourceIdentity"].([]any)
	if !ok || !reflect.DeepEqual(needs, []any{"resource"}) {
		t.Errorf(`dependentRequired.resourceIdentity = %v, want ["resource"]`, dependent["resourceIdentity"])
	}
}

// --- what a block-level handler is handed ---------------------------------------
//
// A request naming one resource for observe carries the type that resource
// was created as under "resourceType". A request naming no resource carries
// what the engine recorded for each declared resource under
// "recordedResources", and the exact type that met each requirement under
// "requirementTypes". Desired carries all three, and VerbAt, the request's
// createdAt, which an observe reading a report compares against.

var recordedResources = map[string]any{
	"aws.ec2-instance": map[string]any{"type": "aws.ec2-instance", "identity": map[string]any{"instanceId": "i-0abc"}},
	"app-container":    map[string]any{"type": "hetzner.server-container", "identity": map[string]any{"serverId": "4711", "container": "app"}},
}

func TestDecodeRequest_ReadsTheNamedResourcesTypeBackOnDesired(t *testing.T) {
	raw := mutateSubscribeGolden(t, func(m map[string]any) {
		m["operation"] = "validate"
		m["resource"] = "app-container"
		m["resourceType"] = "hetzner.server-container"
		m["resourceIdentity"] = map[string]any{"serverId": "4711"}
	})
	req, err := contract.DecodeRequest(raw)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if req.ResourceType != "hetzner.server-container" {
		t.Errorf("ResourceType = %q, want %q", req.ResourceType, "hetzner.server-container")
	}
	if d := req.Desired(); d.ResourceType != "hetzner.server-container" || d.Resource != "app-container" {
		t.Errorf("Desired() resource, type = %q, %q; want %q, %q", d.Resource, d.ResourceType, "app-container", "hetzner.server-container")
	}
}

func TestDecodeRequest_RefusesAResourceTypeWithNoResourceNamingBothFields(t *testing.T) {
	cases := map[string]func(m map[string]any){
		"no resource":    func(m map[string]any) { m["resourceType"] = "aws.ec2-instance" },
		"empty resource": func(m map[string]any) { m["resource"] = ""; m["resourceType"] = "aws.ec2-instance" },
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

func TestDecodeRequest_ReadsRecordedResourcesRequirementTypesAndTheVerbsTimeBackOnDesired(t *testing.T) {
	raw := mutateSubscribeGolden(t, func(m map[string]any) {
		m["operation"] = "unsubscribe"
		m["recordedResources"] = recordedResources
		m["requirementTypes"] = map[string]any{"host": "hetzner.server", "github.repository": "github.repository"}
	})
	req, err := contract.DecodeRequest(raw)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	want := map[string]contract.RecordedResource{
		"aws.ec2-instance": {Type: "aws.ec2-instance", Identity: map[string]string{"instanceId": "i-0abc"}},
		"app-container":    {Type: "hetzner.server-container", Identity: map[string]string{"serverId": "4711", "container": "app"}},
	}
	if !reflect.DeepEqual(req.RecordedResources, want) {
		t.Errorf("RecordedResources = %+v, want %+v", req.RecordedResources, want)
	}
	wantTypes := map[string]string{"host": "hetzner.server", "github.repository": "github.repository"}
	if !reflect.DeepEqual(req.RequirementTypes, wantTypes) {
		t.Errorf("RequirementTypes = %v, want %v", req.RequirementTypes, wantTypes)
	}

	d := req.Desired()
	if !reflect.DeepEqual(d.RecordedResources, want) {
		t.Errorf("Desired().RecordedResources = %+v, want %+v", d.RecordedResources, want)
	}
	if !reflect.DeepEqual(d.RequirementTypes, wantTypes) {
		t.Errorf("Desired().RequirementTypes = %v, want %v", d.RequirementTypes, wantTypes)
	}
	if d.VerbAt.IsZero() || !d.VerbAt.Equal(req.CreatedAt) {
		t.Errorf("Desired().VerbAt = %v, want the request's createdAt %v", d.VerbAt, req.CreatedAt)
	}
}

func TestDecodeRequest_EveryValidGoldenDecodesToNoRecordedResourcesOrRequirementTypes(t *testing.T) {
	for _, name := range []string{"subscribe", "upgrade", "unsubscribe", "validate"} {
		t.Run(name, func(t *testing.T) {
			req := decodeGolden(t, name)
			if req.ResourceType != "" || req.RecordedResources != nil || req.RequirementTypes != nil {
				t.Errorf("%s decodes to resourceType %q, recordedResources %v, requirementTypes %v; want none",
					name, req.ResourceType, req.RecordedResources, req.RequirementTypes)
			}
		})
	}
}

func TestRequestMarshal_EncodesTheBlockLevelFieldsUnderTheirWireNames(t *testing.T) {
	raw, err := json.Marshal(contract.Request{
		Resource:          "aws.ec2-instance",
		ResourceType:      "aws.ec2-instance",
		RecordedResources: map[string]contract.RecordedResource{"aws.vpc": {Type: "aws.vpc", Identity: map[string]string{"vpcId": "vpc-0abc"}}},
		RequirementTypes:  map[string]string{"host": "aws.ec2-instance"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"resourceType":"aws.ec2-instance"`,
		`"recordedResources":{"aws.vpc":{"type":"aws.vpc","identity":{"vpcId":"vpc-0abc"}}}`,
		`"requirementTypes":{"host":"aws.ec2-instance"}`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("json.Marshal = %s, want it to carry %s", raw, want)
		}
	}
	bare, err := json.Marshal(contract.Request{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"resourceType", "recordedResources", "requirementTypes"} {
		if strings.Contains(string(bare), `"`+key+`"`) {
			t.Errorf("json.Marshal of a request without %s = %s, want the key left out", key, bare)
		}
	}
}

func TestEnvelopeSchema_DeclaresTheBlockLevelFieldsAndRaisesItsMinorVersion(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contract.EnvelopeSchema, &doc); err != nil {
		t.Fatalf("contract.EnvelopeSchema is not valid JSON: %v", err)
	}
	if doc["version"] != "1.4.0" {
		t.Errorf("envelope.schema.json version = %v, want 1.4.0: it drops resourceInputs, which the engine never sends, a minor change from 1.3.0", doc["version"])
	}
	if req, ok := doc["required"].([]any); ok {
		for _, r := range req {
			if r == "resourceType" || r == "recordedResources" || r == "requirementTypes" {
				t.Errorf("envelope.schema.json requires %q, but a request without it must stay valid", r)
			}
		}
	}

	props := schemaObject(t, doc, "", "properties")
	resourceType, ok := props["resourceType"].(map[string]any)
	if !ok || !hasType(resourceType, "string") {
		t.Errorf("properties.resourceType = %v, want a string", props["resourceType"])
	}
	dependent := schemaObject(t, doc, "", "dependentRequired")
	if needs, _ := dependent["resourceType"].([]any); !reflect.DeepEqual(needs, []any{"resource"}) {
		t.Errorf(`dependentRequired.resourceType = %v, want ["resource"]`, dependent["resourceType"])
	}

	recorded, ok := props["recordedResources"].(map[string]any)
	if !ok || !hasType(recorded, "object") {
		t.Fatalf("properties.recordedResources = %v, want an object", props["recordedResources"])
	}
	entry := schemaObject(t, recorded, "properties.recordedResources", "additionalProperties")
	fields := schemaObject(t, entry, "properties.recordedResources entries", "properties")
	if ty, _ := fields["type"].(map[string]any); !hasType(ty, "string") {
		t.Errorf("a recorded resource's type = %v, want a string", fields["type"])
	}
	identity, _ := fields["identity"].(map[string]any)
	if !hasType(identity, "object") {
		t.Fatalf("a recorded resource's identity = %v, want an object", fields["identity"])
	}
	if values := schemaObject(t, identity, "a recorded resource's identity", "additionalProperties"); !hasType(values, "string") {
		t.Errorf("a recorded resource's identity values = %v, want strings", values)
	}

	types, ok := props["requirementTypes"].(map[string]any)
	if !ok || !hasType(types, "object") {
		t.Fatalf("properties.requirementTypes = %v, want an object", props["requirementTypes"])
	}
	if values := schemaObject(t, types, "properties.requirementTypes", "additionalProperties"); !hasType(values, "string") {
		t.Errorf("properties.requirementTypes values = %v, want strings", values)
	}
}

// --- which verb an observation settles -----------------------------------------
//
// A validate settles the subscription's last state-changing verb, not
// itself, so the engine sends that verb's time under "settlesAt", and
// Desired.VerbAt is that time. A request without it settles itself, at its
// createdAt.

func TestDecodeRequest_VerbAtIsSettlesAtWhenTheRequestCarriesIt(t *testing.T) {
	const settles = "2026-07-15T20:00:00Z"
	raw := mutateSubscribeGolden(t, func(m map[string]any) {
		m["operation"] = "validate"
		m["settlesAt"] = settles
	})
	req, err := contract.DecodeRequest(raw)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	want, _ := time.Parse(time.RFC3339, settles)
	if d := req.Desired(); !d.VerbAt.Equal(want) {
		t.Errorf("Desired().VerbAt = %v, want settlesAt %v rather than createdAt %v", d.VerbAt, want, req.CreatedAt)
	}
}

func TestDecodeRequest_VerbAtIsCreatedAtWithoutSettlesAt(t *testing.T) {
	req := decodeGolden(t, "validate")
	if d := req.Desired(); d.VerbAt.IsZero() || !d.VerbAt.Equal(req.CreatedAt) {
		t.Errorf("Desired().VerbAt = %v, want createdAt %v", d.VerbAt, req.CreatedAt)
	}
}

func TestDecodeRequest_RefusesASettlesAtThatIsNotRFC3339NamingIt(t *testing.T) {
	for label, value := range map[string]any{"not a time": "yesterday", "a number": 1752613200} {
		t.Run(label, func(t *testing.T) {
			_, err := contract.DecodeRequest(mutateSubscribeGolden(t, func(m map[string]any) { m["settlesAt"] = value }))
			if err == nil {
				t.Fatalf("DecodeRequest accepted settlesAt %v", value)
			}
			if !strings.Contains(err.Error(), `"settlesAt"`) {
				t.Errorf("DecodeRequest error = %q, want it to name %q", err, "settlesAt")
			}
		})
	}
}

func TestEnvelopeSchema_DeclaresSettlesAtAsAnOptionalTime(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contract.EnvelopeSchema, &doc); err != nil {
		t.Fatalf("contract.EnvelopeSchema is not valid JSON: %v", err)
	}
	if req, ok := doc["required"].([]any); ok {
		for _, r := range req {
			if r == "settlesAt" {
				t.Error(`envelope.schema.json requires "settlesAt", but a request settling itself carries none`)
			}
		}
	}
	props := schemaObject(t, doc, "", "properties")
	settles, ok := props["settlesAt"].(map[string]any)
	if !ok || !hasType(settles, "string") || settles["format"] != "date-time" {
		t.Errorf("properties.settlesAt = %v, want a string of format date-time", props["settlesAt"])
	}
}
