package contract_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/kittest"
)

func validResponseGoldens(t *testing.T) map[string][]byte {
	t.Helper()
	valid, err := contract.ValidResponseGoldens()
	if err != nil {
		t.Fatalf("contract.ValidResponseGoldens(): %v", err)
	}
	return valid
}

func decodeResponse(t *testing.T, raw []byte) contract.Response {
	t.Helper()
	resp, err := contract.DecodeResponse(raw)
	if err != nil {
		t.Fatalf("DecodeResponse(%s): %v", raw, err)
	}
	return resp
}

func TestDecodeResponse_CarriesEveryGoldenFieldOntoTheResponse(t *testing.T) {
	for name, raw := range validResponseGoldens(t) {
		t.Run(name, func(t *testing.T) {
			m := jsonObject(t, raw)
			want := contract.Response{Outputs: strMap(m["outputs"]), SecretOutputs: strMap(m["secretOutputs"]), Error: strOf(m, "error"),
				StatusDetail: strOf(m, "statusDetail"), LastError: strOf(m, "lastError")}
			want.Pending, _ = m["pending"].(bool)
			want.Redrive, _ = m["redrive"].(bool)
			if absent, ok := m["absent"].(bool); ok {
				want.Absent = &absent
			}

			got := decodeResponse(t, raw)

			wantField(t, name+" decoded", got, want)
		})
	}
}

func TestDecodeResponse_EveryResponseGoldenDecodesAsBefore(t *testing.T) {
	f, tr := false, true
	want := map[string]contract.Response{
		"absent":          {Absent: &tr},
		"error":           {Error: "subscription quota exceeded for this org"},
		"last-error":      {LastError: "timed out waiting for the load balancer to become healthy"},
		"null-outputs":    {},
		"outputs":         {Outputs: map[string]string{"instanceId": "i-0123456789abcdef0", "endpoint": "https://example.apps.mitstack.dev"}},
		"pending":         {Pending: true, StatusDetail: "waiting for the VPC to finish provisioning"},
		"pending-redrive": {Pending: true, Redrive: true, StatusDetail: "the builder powered off; the snapshot comes next"},
		"secret-outputs":  {SecretOutputs: map[string]string{"apiKey": "sk-example-not-a-real-secret"}},
		"still-present":   {Absent: &f},
	}
	valid := validResponseGoldens(t)
	for name := range valid {
		if _, ok := want[name]; !ok {
			t.Errorf("response golden %q has no pinned decoded value", name)
		}
	}
	for _, name := range sortedKeys(want) {
		t.Run(name, func(t *testing.T) {
			raw, ok := valid[name]
			if !ok {
				t.Fatalf("response golden %q is gone, so its decoded value can no longer be compared", name)
			}

			got := decodeResponse(t, raw)

			wantField(t, name+" decoded", got, want[name])
		})
	}
}

func TestDecodeResponse_ReencodedResponsesConformViaKittest(t *testing.T) {
	bodies := map[string][]byte{}
	for name, raw := range validResponseGoldens(t) {
		reencoded, err := json.Marshal(decodeResponse(t, raw))
		if err != nil {
			t.Fatalf("json.Marshal(DecodeResponse(%s)): %v", name, err)
		}
		bodies[name] = reencoded
	}

	kittest.AssertEmitsConformingResponses(t, bodies)
}

func TestDecodeResponse_RejectsInvalidGoldensNamingField(t *testing.T) {
	wantNamed := map[string]string{
		"invalid-absent-not-bool":                          "absent",
		"invalid-outputs-not-object":                       "outputs",
		"invalid-outputs-not-strings":                      "outputs",
		"invalid-pending-not-bool":                         "pending",
		"invalid-pending-redrive-not-bool":                 "redrive",
		"invalid-secret-outputs-not-strings":               "secretOutputs",
		"invalid-resource-secret-outputs-not-object":       "resourceSecretOutputs",
		"invalid-resource-secret-outputs-entry-not-object": "resourceSecretOutputs",
		"invalid-resource-secret-outputs-value-not-string": "resourceSecretOutputs",
	}
	invalid, err := contract.InvalidResponseGoldens()
	if err != nil {
		t.Fatalf("contract.InvalidResponseGoldens(): %v", err)
	}
	for name, raw := range invalid {
		t.Run(name, func(t *testing.T) {
			field, ok := wantNamed[name]
			if !ok {
				t.Fatalf("invalid response golden %q has no named field, so it ships unchecked", name)
			}

			_, err := contract.DecodeResponse(raw)

			requireErrorNaming(t, "DecodeResponse("+name+")", err, field)
		})
	}
}

func TestDecodeResponse_RefusesAFieldOfTheWrongTypeNamingIt(t *testing.T) {
	cases := []struct{ label, raw, field string }{
		{"resources not an object", `{"resources": ["network"]}`, "resources"},
		{"outcome not an object", `{"resources": {"network": "ensured"}}`, "resources"},
		{"call outcome not an object", `{"resources": {"network": {"ensure": true}}}`, "resources"},
		{"observe outputs not strings", `{"resources": {"network": {"observe": {"outputs": {"port": 8080}}}}}`, "resources"},
		{"call outcome error not string", `{"resources": {"network": {"delete": {"error": 7}}}}`, "resources"},
		{"statusDetail not a string", `{"statusDetail": 5}`, "statusDetail"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			_, err := contract.DecodeResponse([]byte(tc.raw))

			requireErrorNaming(t, "DecodeResponse("+tc.raw+")", err, tc.field)
		})
	}
}

const resourcesResponse = `{
  "outputs": {"vpcId": "vpc-0abc", "publicIp": "203.0.113.7"},
  "secretOutputs": {"rolePassword": "not-a-real-secret"},
  "resources": {
    "network": {"ensure": {}, "observe": {"state": "present", "outputs": {"vpcId": "vpc-0abc"}, "statusDetail": "available"}},
    "instance": {"ensure": {"error": "refused:quota: no capacity in eu-west-1", "statusDetail": "refused:quota: no capacity in eu-west-1"}},
    "old-volume": {"delete": {}, "observe": {"state": "absent"}}
  }
}`

func TestDecodeResponse_ReadsEachResourceOutcomeByName(t *testing.T) {
	refused := "refused:quota: no capacity in eu-west-1"
	cases := []struct {
		label, raw string
		want       contract.Response
	}{
		{"an ensured, a failed and a deleted resource", resourcesResponse, contract.Response{
			Outputs:       map[string]string{"vpcId": "vpc-0abc", "publicIp": "203.0.113.7"},
			SecretOutputs: map[string]string{"rolePassword": "not-a-real-secret"},
			Resources: map[string]contract.ResourceOutcome{
				"network": {Ensure: &contract.CallOutcome{},
					Observe: &contract.CallOutcome{State: contract.Present, Outputs: map[string]string{"vpcId": "vpc-0abc"}, StatusDetail: "available"}},
				"instance":   {Ensure: &contract.CallOutcome{Error: refused, StatusDetail: refused}},
				"old-volume": {Delete: &contract.CallOutcome{}, Observe: &contract.CallOutcome{State: contract.Absent}},
			},
		}},
		{"the replacements a validate names and the type an observe reports",
			`{"outputs": null, "replaces": ["aws.ebs-volume", "deploy-role"], "resources": {` +
				`"aws.ebs-volume": {"observe": {"state": "present", "type": "aws.ebs-volume", "identity": {"volumeId": "vol-0abc"}}}}}`,
			contract.Response{Replaces: []string{"aws.ebs-volume", "deploy-role"}, Resources: map[string]contract.ResourceOutcome{
				"aws.ebs-volume": {Observe: &contract.CallOutcome{State: contract.Present, Type: "aws.ebs-volume", Identity: map[string]string{"volumeId": "vol-0abc"}}},
			}}},
		{"the identity an ensure reported", `{"resources":{"firewall":{"ensure":{"identity":{"groupId":"sg-0abc"},"outputs":{"groupArn":"arn:sg"}}}}}`,
			contract.Response{Resources: map[string]contract.ResourceOutcome{
				"firewall": {Ensure: &contract.CallOutcome{Identity: map[string]string{"groupId": "sg-0abc"}, Outputs: map[string]string{"groupArn": "arn:sg"}}},
			}}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got := decodeResponse(t, []byte(tc.raw))

			wantField(t, "DecodeResponse", got, tc.want)
		})
	}
}

// Unknown leaves Absent nil, so the engine fails open as it does for an operator without the marker.
func TestObservationResponse_MarksAbsenceByState(t *testing.T) {
	f, tr := false, true
	outputs, secrets := map[string]string{"instanceId": "i-0123456789abcdef0"}, map[string]string{"apiKey": "sk-example-not-a-real-secret"}
	cases := []struct {
		obs  contract.Observation
		want contract.Response
	}{
		{contract.Observation{State: contract.Present, Outputs: outputs, SecretOutputs: secrets, StatusDetail: "converged"},
			contract.Response{Outputs: outputs, SecretOutputs: secrets, StatusDetail: "converged", Absent: &f}},
		{contract.Observation{State: contract.Absent, StatusDetail: "resource is gone"},
			contract.Response{StatusDetail: "resource is gone", Absent: &tr}},
		{contract.Observation{State: contract.Unknown, StatusDetail: "checking"},
			contract.Response{StatusDetail: "checking"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.obs.State), func(t *testing.T) {
			got := tc.obs.Response()

			wantField(t, "Observation{State: "+string(tc.obs.State)+"}.Response()", got, tc.want)
		})
	}
}

func TestWire_EncodesEachValueExactly(t *testing.T) {
	cases := []struct {
		label    string
		value    any
		want     string
		conforms bool
	}{
		{"a response with nil outputs encodes JSON null, which the engine tolerates", contract.Response{}, `{"outputs":null}`, true},
		{"a response reporting no resources encodes as before", contract.Response{Pending: true}, `{"outputs":null,"pending":true}`, false},
		{"an ensure reporting an identity", contract.CallOutcome{Identity: map[string]string{"groupId": "sg-0abc"}}, `{"identity":{"groupId":"sg-0abc"}}`, false},
		{"an ensure reporting no identity encodes as before", contract.CallOutcome{}, `{}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			raw, err := json.Marshal(tc.value)

			if err != nil {
				t.Fatalf("json.Marshal(%+v): %v", tc.value, err)
			}
			if string(raw) != tc.want {
				t.Errorf("json.Marshal(%+v) = %s, want %s", tc.value, raw, tc.want)
			}
			if problems := kittest.CheckResponse(raw); tc.conforms && len(problems) != 0 {
				t.Errorf("kittest.CheckResponse(%s) = %v, want no problems", raw, problems)
			}
		})
	}
}

func TestResponse_RoundTripsThroughJSONUnderItsWireNames(t *testing.T) {
	cases := []struct {
		label   string
		in      contract.Response
		encoded func(t *testing.T, m map[string]any)
	}{
		{"resource secret outputs", contract.Response{
			ResourceSecretOutputs: map[string]map[string]string{"deploy-role": {"roleArn": "not-a-real-secret"}},
		}, func(t *testing.T, m map[string]any) {
			if _, ok := m["resourceSecretOutputs"]; !ok {
				t.Errorf("encoded response %v has no resourceSecretOutputs key", m)
			}
		}},
		{"resources by name", contract.Response{
			Outputs: map[string]string{"vpcId": "vpc-0abc"},
			Resources: map[string]contract.ResourceOutcome{
				"network": {Ensure: &contract.CallOutcome{},
					Observe: &contract.CallOutcome{State: contract.Present, Outputs: map[string]string{"vpcId": "vpc-0abc"}}},
				"instance": {Delete: &contract.CallOutcome{Error: "instance still terminating"},
					Observe: &contract.CallOutcome{State: contract.Unknown, StatusDetail: "shutting-down"}},
			},
		}, func(t *testing.T, m map[string]any) {
			resources, _ := m["resources"].(map[string]any)
			network, _ := resources["network"].(map[string]any)
			if _, ok := network["ensure"]; !ok {
				t.Errorf(`network has no "ensure" key in %v: an ensure that ran is reported even when it carries nothing`, m)
			}
			if _, ok := network["delete"]; ok {
				t.Errorf(`network carries a "delete" key in %v, but no delete ran`, m)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			raw, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatal(err)
			}

			got := decodeResponse(t, raw)

			tc.encoded(t, jsonObject(t, raw))
			wantField(t, "round trip", got, tc.in)
		})
	}
}

func jsonNames(typ reflect.Type) []string {
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		if name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

func TestResponseSchema_DeclaresEachFieldTheResponseTypesEncode(t *testing.T) {
	doc := jsonObject(t, contract.ResponseSchema)
	props := schemaAt(t, doc, doc, "properties")
	calls := schemaAt(t, doc, props, "resources", "additionalProperties", "properties")
	cases := []struct {
		label string
		check func(t *testing.T)
	}{
		{"replaces is a list of resource names", func(t *testing.T) {
			replaces := schemaAt(t, doc, props, "replaces")
			if !hasType(replaces, "array") || !hasType(schemaAt(t, doc, replaces, "items"), "string") {
				t.Errorf("properties.replaces = %v, want an array of strings", replaces)
			}
		}},
		{"redrive is a boolean", func(t *testing.T) {
			if f := schemaAt(t, doc, props, "redrive"); !hasType(f, "boolean") {
				t.Errorf("properties.redrive type = %v, want boolean", schemaTypes(f))
			}
		}},
		{"resources is an optional object of outcomes by name", func(t *testing.T) {
			if slices.Contains(sortedStrings(doc["required"]), "resources") {
				t.Error(`response.schema.json requires "resources", but a response that reports none must stay valid`)
			}
			resources := schemaAt(t, doc, props, "resources")
			if !hasType(resources, "object") || !hasType(schemaAt(t, doc, resources, "additionalProperties"), "object") {
				t.Errorf("properties.resources = %v, want an object of objects", resources)
			}
		}},
		{"each call outcome types its fields", func(t *testing.T) {
			wantTypes := map[string]string{"error": "string", "state": "string", "outputs": "object", "statusDetail": "string"}
			for _, verb := range []string{"ensure", "observe", "delete"} {
				call := schemaAt(t, doc, calls, verb)
				if !hasType(call, "object") {
					t.Errorf("resource outcome %q type = %v, want object", verb, schemaTypes(call))
				}
				for field, ty := range wantTypes {
					if f := schemaAt(t, doc, call, "properties", field); !hasType(f, ty) {
						t.Errorf("resource outcome %q field %q type = %v, want %s", verb, field, schemaTypes(f), ty)
					}
				}
			}
		}},
		{"an ensure's identity is an object of strings", func(t *testing.T) {
			objectOfStrings(t, doc, schemaAt(t, doc, calls, "ensure", "properties", "identity"), "ensure identity")
		}},
		{"every field the types encode is declared", func(t *testing.T) {
			for _, name := range jsonNames(reflect.TypeOf(contract.Response{})) {
				if _, ok := props[name]; !ok {
					t.Errorf("contract.Response encodes %q, which response.schema.json does not declare", name)
				}
			}
			for _, name := range jsonNames(reflect.TypeOf(contract.ResourceOutcome{})) {
				if _, ok := calls[name]; !ok {
					t.Errorf("contract.ResourceOutcome encodes %q, which response.schema.json does not declare", name)
				}
			}
			for verb, raw := range calls {
				call, _ := raw.(map[string]any)
				fields, _ := call["properties"].(map[string]any)
				for _, name := range jsonNames(reflect.TypeOf(contract.CallOutcome{})) {
					if _, ok := fields[name]; !ok {
						t.Errorf("contract.CallOutcome encodes %q, which response.schema.json does not declare under %q", name, verb)
					}
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.label, tc.check)
	}
}
