package contract_test

// response_resources_test.go pins the per-resource half of the response
// contract: a response may report, under each declared resource's name, the
// outcome of the ensure, observe and delete calls a verb ran for it. The
// field is optional, so every existing response golden decodes to exactly
// the value it decoded to before the field existed.

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// resourcesResponse reports three resources: one ensured and observed
// present, one whose ensure failed, and one deleted and observed absent.
const resourcesResponse = `{
  "outputs": {"vpcId": "vpc-0abc", "publicIp": "203.0.113.7"},
  "secretOutputs": {"rolePassword": "not-a-real-secret"},
  "resources": {
    "network": {
      "ensure": {},
      "observe": {
        "state": "present",
        "outputs": {"vpcId": "vpc-0abc"},
        "statusDetail": "available"
      }
    },
    "instance": {
      "ensure": {"error": "refused:quota: no capacity in eu-west-1", "statusDetail": "refused:quota: no capacity in eu-west-1"}
    },
    "old-volume": {
      "delete": {},
      "observe": {"state": "absent"}
    }
  }
}`

func TestDecodeResponse_ReadsEachResourceOutcomeByName(t *testing.T) {
	got, err := contract.DecodeResponse([]byte(resourcesResponse))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	if len(got.Resources) != 3 {
		t.Fatalf("Resources has %d entries %v, want 3 (network, instance, old-volume)", len(got.Resources), got.Resources)
	}

	network, ok := got.Resources["network"]
	if !ok {
		t.Fatal(`Resources has no "network" entry`)
	}
	if network.Ensure == nil {
		t.Error(`network.Ensure = nil, want the ensure outcome ({} means ensure ran and succeeded)`)
	} else if !reflect.DeepEqual(*network.Ensure, contract.CallOutcome{}) {
		t.Errorf("network.Ensure = %+v, want the zero CallOutcome", *network.Ensure)
	}
	if network.Delete != nil {
		t.Errorf("network.Delete = %+v, want nil: no delete ran for it", *network.Delete)
	}
	wantObserve := contract.CallOutcome{
		State:        contract.Present,
		Outputs:      map[string]string{"vpcId": "vpc-0abc"},
		StatusDetail: "available",
	}
	if network.Observe == nil {
		t.Error("network.Observe = nil, want the observe outcome")
	} else if !reflect.DeepEqual(*network.Observe, wantObserve) {
		t.Errorf("network.Observe = %+v, want %+v", *network.Observe, wantObserve)
	}

	instance, ok := got.Resources["instance"]
	if !ok {
		t.Fatal(`Resources has no "instance" entry`)
	}
	wantEnsure := contract.CallOutcome{
		Error:        "refused:quota: no capacity in eu-west-1",
		StatusDetail: "refused:quota: no capacity in eu-west-1",
	}
	if instance.Ensure == nil {
		t.Error("instance.Ensure = nil, want the failed ensure outcome")
	} else if !reflect.DeepEqual(*instance.Ensure, wantEnsure) {
		t.Errorf("instance.Ensure = %+v, want %+v", *instance.Ensure, wantEnsure)
	}
	if instance.Observe != nil || instance.Delete != nil {
		t.Errorf("instance carries observe %v and delete %v, want neither: only ensure ran", instance.Observe, instance.Delete)
	}

	old, ok := got.Resources["old-volume"]
	if !ok {
		t.Fatal(`Resources has no "old-volume" entry`)
	}
	if old.Delete == nil {
		t.Error("old-volume.Delete = nil, want the delete outcome")
	}
	if old.Ensure != nil {
		t.Errorf("old-volume.Ensure = %+v, want nil", *old.Ensure)
	}
	if old.Observe == nil || old.Observe.State != contract.Absent {
		t.Errorf("old-volume.Observe = %+v, want state absent", old.Observe)
	}

	if got.Outputs["vpcId"] != "vpc-0abc" || got.Outputs["publicIp"] != "203.0.113.7" {
		t.Errorf("block-level Outputs = %v, want both resources' outputs", got.Outputs)
	}
	if got.SecretOutputs["rolePassword"] != "not-a-real-secret" {
		t.Errorf("block-level SecretOutputs = %v, want rolePassword", got.SecretOutputs)
	}
}

func TestDecodeResponse_ResourcesOfTheWrongTypeNameTheField(t *testing.T) {
	cases := map[string]string{
		"resources not an object":       `{"resources": ["network"]}`,
		"outcome not an object":         `{"resources": {"network": "ensured"}}`,
		"call outcome not an object":    `{"resources": {"network": {"ensure": true}}}`,
		"observe outputs not strings":   `{"resources": {"network": {"observe": {"outputs": {"port": 8080}}}}}`,
		"call outcome error not string": `{"resources": {"network": {"delete": {"error": 7}}}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := contract.DecodeResponse([]byte(raw))
			if err == nil {
				t.Fatalf("DecodeResponse(%s) succeeded, want an error naming the resources field", raw)
			}
			if !strings.Contains(err.Error(), "resources") {
				t.Errorf("DecodeResponse(%s) error = %q, want it to name %q", raw, err.Error(), "resources")
			}
		})
	}
}

// wantGoldenDecodes is the value every valid response golden decoded to
// before a response could report its resources. A golden added later needs
// a row here, so no golden's decoded value goes unpinned.
func wantGoldenDecodes() map[string]contract.Response {
	f, tr := false, true
	return map[string]contract.Response{
		"absent":         {Absent: &tr},
		"error":          {Error: "subscription quota exceeded for this org"},
		"last-error":     {LastError: "timed out waiting for the load balancer to become healthy"},
		"null-outputs":   {},
		"outputs":        {Outputs: map[string]string{"instanceId": "i-0123456789abcdef0", "endpoint": "https://example.apps.mitstack.dev"}},
		"pending":        {Pending: true, StatusDetail: "waiting for the VPC to finish provisioning"},
		"secret-outputs": {SecretOutputs: map[string]string{"apiKey": "sk-example-not-a-real-secret"}},
		"still-present":  {Absent: &f},
	}
}

func TestDecodeResponse_EveryResponseGoldenDecodesAsBefore(t *testing.T) {
	valid, err := contract.ValidResponseGoldens()
	if err != nil {
		t.Fatalf("contract.ValidResponseGoldens(): %v", err)
	}
	want := wantGoldenDecodes()
	for name := range valid {
		if _, ok := want[name]; !ok {
			t.Errorf("response golden %q has no row in wantGoldenDecodes, so its decoded value is unpinned", name)
		}
	}
	for name, w := range want {
		raw, ok := valid[name]
		if !ok {
			t.Errorf("response golden %q is gone, so its decoded value can no longer be compared", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			got, err := contract.DecodeResponse(raw)
			if err != nil {
				t.Fatalf("DecodeResponse(%s): %v", name, err)
			}
			if got.Resources != nil {
				t.Errorf("%s: Resources = %v, want nil: the golden reports no resource", name, got.Resources)
			}
			if !reflect.DeepEqual(got, w) {
				t.Errorf("%s decodes to %+v, want %+v as before", name, got, w)
			}
		})
	}
}

func TestResponseMarshal_OmitsResourcesWhenNoneAreReported(t *testing.T) {
	raw, err := json.Marshal(contract.Response{Pending: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), `{"outputs":null,"pending":true}`; got != want {
		t.Errorf("json.Marshal = %s, want %s: a response with no resources must encode as before", got, want)
	}
}

func TestResponseMarshal_ResourceSecretOutputsRoundTripUnderTheirTagAndOmitWhenEmpty(t *testing.T) {
	in := contract.Response{
		ResourceSecretOutputs: map[string]map[string]string{"deploy-role": {"roleArn": "not-a-real-secret"}},
	}

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal fields: %v", err)
	}
	got, err := contract.DecodeResponse(raw)
	if err != nil {
		t.Fatalf("DecodeResponse(%s): %v", raw, err)
	}
	empty, _ := json.Marshal(contract.Response{})

	if _, ok := fields["resourceSecretOutputs"]; !ok {
		t.Errorf("encoded response %s has no resourceSecretOutputs key", raw)
	}
	if !reflect.DeepEqual(got.ResourceSecretOutputs, in.ResourceSecretOutputs) {
		t.Errorf("decoded ResourceSecretOutputs = %v, want %v", got.ResourceSecretOutputs, in.ResourceSecretOutputs)
	}
	if strings.Contains(string(empty), "resourceSecretOutputs") {
		t.Errorf("empty response encodes as %s, want resourceSecretOutputs omitted", empty)
	}
}

func TestResponseMarshal_ResourcesRoundTripByName(t *testing.T) {
	in := contract.Response{
		Outputs: map[string]string{"vpcId": "vpc-0abc"},
		Resources: map[string]contract.ResourceOutcome{
			"network": {
				Ensure:  &contract.CallOutcome{},
				Observe: &contract.CallOutcome{State: contract.Present, Outputs: map[string]string{"vpcId": "vpc-0abc"}},
			},
			"instance": {
				Delete:  &contract.CallOutcome{Error: "instance still terminating"},
				Observe: &contract.CallOutcome{State: contract.Unknown, StatusDetail: "shutting-down"},
			},
		},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	resources, ok := probe["resources"].(map[string]any)
	if !ok {
		t.Fatalf(`encoded response %s has no "resources" object`, raw)
	}
	network, _ := resources["network"].(map[string]any)
	if _, ok := network["ensure"]; !ok {
		t.Errorf(`network has no "ensure" key in %s: an ensure that ran must be reported even when it carries nothing`, raw)
	}
	if _, ok := network["delete"]; ok {
		t.Errorf(`network carries a "delete" key in %s, but no delete ran`, raw)
	}

	out, err := contract.DecodeResponse(raw)
	if err != nil {
		t.Fatalf("DecodeResponse(%s): %v", raw, err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("round trip gave %+v, want %+v", out, in)
	}
}

// A validate names the declared resources its upgrade would replace under
// "replaces", and an observe of a block-level resource reports the type it
// observed it as beside its identity.
func TestDecodeResponse_ReadsReplacesAndTheTypeAnObserveReports(t *testing.T) {
	raw := `{"outputs": null, "replaces": ["aws.ebs-volume", "deploy-role"], "resources": {` +
		`"aws.ebs-volume": {"observe": {"state": "present", "type": "aws.ebs-volume", "identity": {"volumeId": "vol-0abc"}}}}}`
	got, err := contract.DecodeResponse([]byte(raw))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if want := []string{"aws.ebs-volume", "deploy-role"}; !reflect.DeepEqual(got.Replaces, want) {
		t.Errorf("Replaces = %q, want %q", got.Replaces, want)
	}
	want := contract.CallOutcome{State: contract.Present, Type: "aws.ebs-volume", Identity: map[string]string{"volumeId": "vol-0abc"}}
	if o := got.Resources["aws.ebs-volume"].Observe; o == nil || !reflect.DeepEqual(*o, want) {
		t.Errorf("aws.ebs-volume observe = %+v, want %+v", o, want)
	}

	out, err := json.Marshal(contract.Response{Pending: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "replaces") {
		t.Errorf("json.Marshal = %s, want no replaces key on a response naming no replacement", out)
	}
}

func TestResponseSchema_DeclaresReplacesAsAListOfResourceNames(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contract.ResponseSchema, &doc); err != nil {
		t.Fatalf("contract.ResponseSchema is not valid JSON: %v", err)
	}
	props := schemaObject(t, doc, "", "properties")
	replaces, ok := props["replaces"].(map[string]any)
	if !ok || !hasType(replaces, "array") {
		t.Fatalf("properties.replaces = %v, want an array", props["replaces"])
	}
	if items := schemaObject(t, replaces, "properties.replaces", "items"); !hasType(items, "string") {
		t.Errorf("properties.replaces items = %v, want strings", items)
	}
}

// schemaObject reads key from a decoded JSON Schema node as an object.
func schemaObject(t *testing.T, node map[string]any, path, key string) map[string]any {
	t.Helper()
	v, ok := node[key].(map[string]any)
	if !ok {
		t.Fatalf("response.schema.json %s has no %q object", path, key)
	}
	return v
}

// schemaTypes reads a node's "type" as a sorted list, whether the schema
// gives one type or several.
func schemaTypes(node map[string]any) []string {
	switch v := node["type"].(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		sort.Strings(out)
		return out
	}
	return nil
}

func hasType(node map[string]any, want string) bool {
	for _, ty := range schemaTypes(node) {
		if ty == want {
			return true
		}
	}
	return false
}

func TestResponseSchema_DeclaresResourcesAsOutcomesByName(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contract.ResponseSchema, &doc); err != nil {
		t.Fatalf("contract.ResponseSchema is not valid JSON: %v", err)
	}
	if req, ok := doc["required"].([]any); ok {
		for _, r := range req {
			if r == "resources" {
				t.Error(`response.schema.json requires "resources", but a response that reports none must stay valid`)
			}
		}
	}

	props := schemaObject(t, doc, "", "properties")
	resources := schemaObject(t, props, "properties", "resources")
	if !hasType(resources, "object") {
		t.Errorf("properties.resources type = %v, want object", schemaTypes(resources))
	}
	outcome := schemaObject(t, resources, "properties.resources", "additionalProperties")
	if !hasType(outcome, "object") {
		t.Errorf("properties.resources.additionalProperties type = %v, want object", schemaTypes(outcome))
	}
	calls := schemaObject(t, outcome, "properties.resources.additionalProperties", "properties")
	for _, verb := range []string{"ensure", "observe", "delete"} {
		call, ok := calls[verb].(map[string]any)
		if !ok {
			t.Errorf("a resource outcome in response.schema.json declares no %q call", verb)
			continue
		}
		if !hasType(call, "object") {
			t.Errorf("resource outcome %q type = %v, want object", verb, schemaTypes(call))
		}
		fields, ok := call["properties"].(map[string]any)
		if !ok {
			t.Errorf("resource outcome %q declares no properties", verb)
			continue
		}
		wantTypes := map[string]string{"error": "string", "state": "string", "outputs": "object", "statusDetail": "string"}
		for field, ty := range wantTypes {
			f, ok := fields[field].(map[string]any)
			if !ok {
				t.Errorf("resource outcome %q declares no %q field", verb, field)
				continue
			}
			if !hasType(f, ty) {
				t.Errorf("resource outcome %q field %q type = %v, want %s", verb, field, schemaTypes(f), ty)
			}
		}
	}
}

// jsonNames returns the JSON property names typ's fields encode under.
func jsonNames(typ reflect.Type) []string {
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		out = append(out, name)
	}
	return out
}

func TestResponseSchema_NamesEveryFieldTheTypesEncode(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(contract.ResponseSchema, &doc); err != nil {
		t.Fatalf("contract.ResponseSchema is not valid JSON: %v", err)
	}
	props := schemaObject(t, doc, "", "properties")
	for _, name := range jsonNames(reflect.TypeOf(contract.Response{})) {
		if _, ok := props[name]; !ok {
			t.Errorf("contract.Response encodes %q, which response.schema.json does not declare", name)
		}
	}

	resources := schemaObject(t, props, "properties", "resources")
	outcome := schemaObject(t, resources, "properties.resources", "additionalProperties")
	calls := schemaObject(t, outcome, "properties.resources.additionalProperties", "properties")
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
}
