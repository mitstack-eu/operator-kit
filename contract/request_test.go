package contract_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/kittest"
)

func TestDecodeRequest_DecodesEveryValidGoldenViaKittest(t *testing.T) {
	kittest.AssertDecodesEveryValidGolden(t, func(raw []byte) error {
		_, err := contract.DecodeRequest(raw)
		return err
	}, "operation", "nonce", "createdAt", "subscriptionId", "blockId", "orgId")
}

func strOf(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func strMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, e := range m {
		out[k], _ = e.(string)
	}
	return out
}

func strMaps(v any) map[string]map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]map[string]string, len(m))
	for k, e := range m {
		out[k] = strMap(e)
	}
	return out
}

// requestFromGolden builds a Request from the golden's raw keys by hand, so a field that fails to reach the typed Request shows as a difference.
func requestFromGolden(m map[string]any) contract.Request {
	r := contract.Request{
		Operation: strOf(m, "operation"), SubscriptionID: strOf(m, "subscriptionId"), IdempotencyKey: strOf(m, "idempotencyKey"),
		BlockID: strOf(m, "blockId"), BlockVersion: strOf(m, "blockVersion"), OrgID: strOf(m, "orgId"), AppID: strOf(m, "appId"),
		OBO: strOf(m, "obo"), Nonce: strOf(m, "nonce"),
		Params: strMap(m["params"]), SecretInputs: strMap(m["secretInputs"]), AppSecrets: strMap(m["appSecrets"]),
		PriorOutputs: strMap(m["priorOutputs"]), Secrets: strMaps(m["secrets"]), Bindings: strMaps(m["bindings"]),
	}
	if instances, ok := m["instances"].(map[string]any); ok {
		r.Instances = map[string]contract.InstanceConfig{}
		for k, v := range instances {
			o, _ := v.(map[string]any)
			r.Instances[k] = contract.InstanceConfig{Name: strOf(o, "name"), Endpoint: strOf(o, "endpoint"), Config: strMap(o["config"])}
		}
	}
	if all, ok := m["bindingsAll"].(map[string]any); ok {
		r.BindingsAll = map[string][]map[string]string{}
		for k, v := range all {
			list, _ := v.([]any)
			r.BindingsAll[k] = []map[string]string{}
			for _, e := range list {
				r.BindingsAll[k] = append(r.BindingsAll[k], strMap(e))
			}
		}
	}
	if ci, ok := m["cloudIdentity"].(map[string]any); ok {
		r.CloudIdentity = &contract.CloudIdentity{Provider: strOf(ci, "provider"), RoleARN: strOf(ci, "roleArn"),
			WebIdentityToken: strOf(ci, "webIdentityToken"), AccountRef: strOf(ci, "accountRef")}
	}
	if br, ok := m["buildRequest"].(map[string]any); ok {
		r.BuildRequest = &contract.BuildRequest{ID: strOf(br, "id"), SourceBranch: strOf(br, "sourceBranch"), SourceSha: strOf(br, "sourceSha")}
	}
	return r
}

func TestDecodeRequest_CarriesEveryGoldenFieldOntoTheRequest(t *testing.T) {
	valid, err := contract.ValidGoldens()
	if err != nil {
		t.Fatalf("contract.ValidGoldens(): %v", err)
	}
	for name, raw := range valid {
		t.Run(name, func(t *testing.T) {
			m := jsonObject(t, raw)
			wantCreated, err := time.Parse(time.RFC3339, strOf(m, "createdAt"))
			if err != nil {
				t.Fatalf("golden createdAt %q is not RFC 3339: %v", m["createdAt"], err)
			}

			got, err := contract.DecodeRequest(raw)

			if err != nil {
				t.Fatalf("DecodeRequest(%s): %v", name, err)
			}
			if !got.CreatedAt.Equal(wantCreated) {
				t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, wantCreated)
			}
			got.CreatedAt = time.Time{}
			if want := requestFromGolden(m); !reflect.DeepEqual(got, want) {
				t.Errorf("DecodeRequest(%s) =\n%+v\nwant the golden's fields\n%+v", name, got, want)
			}
		})
	}
}

// The encodings live in a golden file because a field added later is optional, so it must leave every golden's encoding as pinned.
func TestDecodeRequest_EveryRequestGoldenDecodesOrIsRefusedExactlyAsPinned(t *testing.T) {
	refusals := map[string]string{
		"invalid-missing-nonce": `contract: envelope field "nonce" is required`,
		"invalid-unknown-verb":  `contract: envelope field "operation" must be one of subscribe, unsubscribe, upgrade, validate, issue, got "reticulate"`,
	}
	pinned, err := os.ReadFile("testdata/request-golden-encodings.json")
	if err != nil {
		t.Fatal(err)
	}
	var encodings map[string]json.RawMessage
	if err := json.Unmarshal(pinned, &encodings); err != nil {
		t.Fatalf("testdata/request-golden-encodings.json: %v", err)
	}
	valid, verr := contract.ValidGoldens()
	invalid, ierr := contract.InvalidGoldens()
	if verr != nil || ierr != nil {
		t.Fatalf("contract.ValidGoldens(), InvalidGoldens(): %v, %v", verr, ierr)
	}
	all := map[string][]byte{}
	for _, set := range []map[string][]byte{valid, invalid} {
		for name, raw := range set {
			all[name] = raw
			_, encoded := encodings[name]
			if _, refused := refusals[name]; !encoded && !refused {
				t.Errorf("request golden %q has no pinned outcome", name)
			}
		}
	}
	for _, name := range append(sortedKeys(encodings), sortedKeys(refusals)...) {
		t.Run(name, func(t *testing.T) {
			raw, ok := all[name]
			if !ok {
				t.Fatalf("request golden %q is gone, so its pinned outcome can no longer be compared", name)
			}

			req, err := contract.DecodeRequest(raw)

			if want, refused := refusals[name]; refused {
				if err == nil || err.Error() != want {
					t.Errorf("DecodeRequest(%s) error = %v, want %q", name, err, want)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeRequest(%s) refused with %q, want it decoded", name, err)
			}
			got, err := json.Marshal(req)
			var want bytes.Buffer
			if err == nil {
				err = json.Compact(&want, encodings[name])
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want.Bytes()) {
				t.Errorf("%s decodes to a Request encoding as\n  %s\nwant\n  %s", name, got, want.Bytes())
			}
		})
	}
}

func TestDecodeRequest_RefusesAMalformedFieldNamingIt(t *testing.T) {
	identity := map[string]string{"groupId": "sg-0abc", "vpcId": "vpc-0abc"}
	without := func(key string) func(m map[string]any) {
		return func(m map[string]any) { delete(m["cloudIdentity"].(map[string]any), key) }
	}
	buildWithout := func(key string) func(m map[string]any) {
		return func(m map[string]any) {
			br := map[string]any{"id": "b1", "sourceBranch": "main", "sourceSha": "abc123"}
			delete(br, key)
			m["buildRequest"] = br
		}
	}
	cases := []struct {
		label  string
		mutate func(m map[string]any)
		wants  []string
	}{
		{"createdAt a number", func(m map[string]any) { m["createdAt"] = 12345 }, []string{"createdAt"}},
		{"createdAt a bool", func(m map[string]any) { m["createdAt"] = true }, []string{"createdAt"}},
		{"createdAt an object", func(m map[string]any) { m["createdAt"] = map[string]any{"x": 1} }, []string{"createdAt"}},
		{"createdAt not RFC 3339", func(m map[string]any) { m["createdAt"] = "not-a-timestamp" }, []string{"createdAt"}},
		{"settlesAt not a time", func(m map[string]any) { m["settlesAt"] = "yesterday" }, []string{`"settlesAt"`}},
		{"settlesAt a number", func(m map[string]any) { m["settlesAt"] = 1752613200 }, []string{`"settlesAt"`}},
		{"neverActive a string", func(m map[string]any) { m["neverActive"] = "true" }, []string{`"neverActive"`}},
		{"cloudIdentity without provider", without("provider"), []string{"cloudIdentity.provider"}},
		{"cloudIdentity without roleArn", without("roleArn"), []string{"cloudIdentity.roleArn"}},
		{"cloudIdentity without webIdentityToken", without("webIdentityToken"), []string{"cloudIdentity.webIdentityToken"}},
		{"buildRequest without id", buildWithout("id"), []string{"buildRequest.id"}},
		{"buildRequest without sourceBranch", buildWithout("sourceBranch"), []string{"buildRequest.sourceBranch"}},
		{"buildRequest without sourceSha", buildWithout("sourceSha"), []string{"buildRequest.sourceSha"}},
		{"a resource identity with no resource", func(m map[string]any) { m["resourceIdentity"] = identity },
			[]string{`"resourceIdentity"`, `"resource"`}},
		{"a resource identity with an empty resource", func(m map[string]any) { m["resource"] = ""; m["resourceIdentity"] = identity },
			[]string{`"resourceIdentity"`, `"resource"`}},
		{"a resource type with no resource", func(m map[string]any) { m["resourceType"] = "aws.ec2-instance" },
			[]string{`"resourceType"`, `"resource"`}},
		{"a resource type with an empty resource", func(m map[string]any) { m["resource"] = ""; m["resourceType"] = "aws.ec2-instance" },
			[]string{`"resourceType"`, `"resource"`}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			raw := mutateSubscribeGolden(t, tc.mutate)

			_, err := contract.DecodeRequest(raw)

			requireErrorNaming(t, "DecodeRequest", err, tc.wants...)
		})
	}
}

func TestDecodeRequest_CarriesWhatTheRequestNamesOntoDesired(t *testing.T) {
	const settles = "2026-07-15T20:00:00Z"
	settlesAt, _ := time.Parse(time.RFC3339, settles)
	identity := map[string]string{"groupId": "sg-0abc", "vpcId": "vpc-0abc"}
	recorded := map[string]contract.RecordedResource{
		"aws.ec2-instance": {Type: "aws.ec2-instance", Identity: map[string]string{"instanceId": "i-0abc"}},
		"app-container":    {Type: "hetzner.server-container", Identity: map[string]string{"serverId": "4711", "container": "app"}},
		"database":         {Type: "aws.ec2-postgres-database", Identity: map[string]string{"instanceId": "i-0abc"}, State: contract.Absent},
	}
	requirementTypes := map[string]string{"host": "hetzner.server", "github.repository": "github.repository"}
	cases := []struct {
		label  string
		mutate func(m map[string]any)
		check  func(t *testing.T, req contract.Request, d contract.Desired)
	}{
		{"an unmodified golden decodes", func(map[string]any) {}, func(t *testing.T, req contract.Request, d contract.Desired) {}},
		{"a named resource is decoded", func(m map[string]any) { m["operation"] = "validate"; m["resource"] = "firewall" },
			func(t *testing.T, req contract.Request, d contract.Desired) {
				wantField(t, "Resource", req.Resource, "firewall")
			}},
		{"a named resource reaches Desired", func(m map[string]any) { m["operation"] = "validate"; m["resource"] = "firewall" },
			func(t *testing.T, req contract.Request, d contract.Desired) {
				wantField(t, "Desired().Resource", d.Resource, "firewall")
			}},
		{"a named resource's stored identity reaches Desired", func(m map[string]any) {
			m["operation"], m["resource"], m["resourceIdentity"] = "validate", "firewall", identity
		}, func(t *testing.T, req contract.Request, d contract.Desired) {
			wantField(t, "ResourceIdentity", req.ResourceIdentity, identity)
			wantField(t, "Desired().Resource", d.Resource, "firewall")
			wantField(t, "Desired().ResourceIdentity", d.ResourceIdentity, identity)
		}},
		{"a named resource's type reaches Desired", func(m map[string]any) {
			m["operation"], m["resource"], m["resourceType"] = "validate", "app-container", "hetzner.server-container"
			m["resourceIdentity"] = map[string]any{"serverId": "4711"}
		}, func(t *testing.T, req contract.Request, d contract.Desired) {
			wantField(t, "ResourceType", req.ResourceType, "hetzner.server-container")
			wantField(t, "Desired() resource, type", []string{d.Resource, d.ResourceType}, []string{"app-container", "hetzner.server-container"})
		}},
		{"recorded resources and requirement types reach Desired, at the request's time", func(m map[string]any) {
			m["operation"], m["recordedResources"], m["requirementTypes"] = "unsubscribe", recorded, requirementTypes
		}, func(t *testing.T, req contract.Request, d contract.Desired) {
			wantField(t, "RecordedResources", req.RecordedResources, recorded)
			wantField(t, "RequirementTypes", req.RequirementTypes, requirementTypes)
			wantField(t, "Desired().RecordedResources", d.RecordedResources, recorded)
			wantField(t, "Desired().RequirementTypes", d.RequirementTypes, requirementTypes)
			if d.VerbAt.IsZero() || !d.VerbAt.Equal(req.CreatedAt) {
				t.Errorf("Desired().VerbAt = %v, want the request's createdAt %v", d.VerbAt, req.CreatedAt)
			}
		}},
		{"a validate without settlesAt settles at its createdAt", func(m map[string]any) { m["operation"] = "validate" },
			func(t *testing.T, req contract.Request, d contract.Desired) {
				if d.VerbAt.IsZero() || !d.VerbAt.Equal(req.CreatedAt) {
					t.Errorf("Desired().VerbAt = %v, want createdAt %v", d.VerbAt, req.CreatedAt)
				}
			}},
		{"a subscription the engine proved never active reaches Desired as never active", func(m map[string]any) { m["neverActive"] = true },
			func(t *testing.T, req contract.Request, d contract.Desired) {
				wantField(t, "NeverActive, Desired().NeverActive", []bool{req.NeverActive, d.NeverActive}, []bool{true, true})
			}},
		{"a validate settles at its settlesAt", func(m map[string]any) { m["operation"], m["settlesAt"] = "validate", settles },
			func(t *testing.T, req contract.Request, d contract.Desired) {
				if !d.VerbAt.Equal(settlesAt) {
					t.Errorf("Desired().VerbAt = %v, want settlesAt %v rather than createdAt %v", d.VerbAt, settlesAt, req.CreatedAt)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			raw := mutateSubscribeGolden(t, tc.mutate)

			req, err := contract.DecodeRequest(raw)

			if err != nil {
				t.Fatalf("DecodeRequest: %v", err)
			}
			d := req.Desired()
			if d.SubscriptionID != req.SubscriptionID || !reflect.DeepEqual(d.Params, req.Params) {
				t.Errorf("Desired() lost the subscription or params: %+v", d)
			}
			tc.check(t, req, d)
		})
	}
}

// requestGoldenBehaviours each hold for every valid request golden, a golden naming no resource.
var requestGoldenBehaviours = []struct {
	behaviour string
	check     func(t *testing.T, req contract.Request)
}{
	{"names no resource on the request or Desired", func(t *testing.T, req contract.Request) {
		wantField(t, "Resource, Desired().Resource", []string{req.Resource, req.Desired().Resource}, []string{"", ""})
	}},
	{"decodes no resource identity", func(t *testing.T, req contract.Request) {
		if req.ResourceIdentity != nil || req.Desired().ResourceIdentity != nil {
			t.Errorf("ResourceIdentity, Desired().ResourceIdentity = %v, %v; want nil", req.ResourceIdentity, req.Desired().ResourceIdentity)
		}
	}},
	{"decodes no resource type, recorded resources or requirement types", func(t *testing.T, req contract.Request) {
		if req.ResourceType != "" || req.RecordedResources != nil || req.RequirementTypes != nil {
			t.Errorf("resourceType %q, recordedResources %v, requirementTypes %v; want none", req.ResourceType, req.RecordedResources, req.RequirementTypes)
		}
	}},
	{"reads as a subscription that may have been active, on the request and Desired", func(t *testing.T, req contract.Request) {
		wantField(t, "NeverActive, Desired().NeverActive", []bool{req.NeverActive, req.Desired().NeverActive}, []bool{false, false})
	}},
	{"hands Identity the request's identity fields", func(t *testing.T, req contract.Request) {
		wantField(t, "Identity()", req.Identity(), identityOf(req))
	}},
	{"embeds that Identity in Desired, promoting its instances", func(t *testing.T, req contract.Request) {
		d := req.Desired()
		wantField(t, "Desired().Identity", d.Identity, identityOf(req))
		wantField(t, "Desired().Instances", d.Instances, req.Instances)
	}},
	{"hands Desired the request's params, secret inputs and build request", func(t *testing.T, req contract.Request) {
		d := req.Desired()
		wantField(t, "Desired() params, secret inputs, build request", []any{d.Params, d.SecretInputs, d.BuildRequest},
			[]any{req.Params, req.SecretInputs, req.BuildRequest})
	}},
}

func identityOf(req contract.Request) contract.Identity {
	return contract.Identity{SubscriptionID: req.SubscriptionID, BlockID: req.BlockID, BlockVersion: req.BlockVersion,
		OrgID: req.OrgID, AppID: req.AppID, Instances: req.Instances, PriorOutputs: req.PriorOutputs, CloudIdentity: req.CloudIdentity}
}

func TestRequest_EveryValidGoldenDecodesToItsIdentityAndDesired(t *testing.T) {
	for _, name := range []string{"subscribe", "unsubscribe", "upgrade", "validate"} {
		for _, tc := range requestGoldenBehaviours {
			t.Run(name+"/"+tc.behaviour, func(t *testing.T) {
				req := decodeGolden(t, name)
				if len(req.Instances) == 0 {
					t.Fatalf("the %s golden carries no instances, so this proves nothing about what Identity carries", name)
				}

				tc.check(t, req)
			})
		}
	}
}

func TestRequest_IdentityCarriesWhatEachGoldenHoldsForItsResource(t *testing.T) {
	cases := []struct {
		label, golden string
		got           func(r contract.Request) any
		want          any
	}{
		{"subscribe carries no prior outputs", "subscribe", func(r contract.Request) any { return len(r.Identity().PriorOutputs) }, 0},
		{"subscribe carries its cloud identity", "subscribe", func(r contract.Request) any { return r.Identity().CloudIdentity != nil }, true},
		{"unsubscribe carries its prior outputs", "unsubscribe",
			func(r contract.Request) any { return r.Identity().PriorOutputs["instanceId"] }, "i-0a1b2c3d4e5f67890"},
		{"Desired carries unsubscribe's prior outputs", "unsubscribe",
			func(r contract.Request) any { return r.Desired().PriorOutputs["instanceId"] }, "i-0a1b2c3d4e5f67890"},
		{"unsubscribe carries its instance region", "unsubscribe",
			func(r contract.Request) any { return r.Identity().Instances["aws"].Config["region"] }, "eu-west-1"},
		{"Desired carries subscribe's instance region", "subscribe",
			func(r contract.Request) any { return r.Desired().Instances["aws"].Config["region"] }, "eu-west-1"},
		{"Desired carries unsubscribe's instance region", "unsubscribe",
			func(r contract.Request) any { return r.Desired().Instances["aws"].Config["region"] }, "eu-west-1"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			req := decodeGolden(t, tc.golden)

			got := tc.got(req)

			wantField(t, tc.label, got, tc.want)
		})
	}
}

// A block scoped to a region reads it from Identity, so a request with no instances must read as an empty region rather than panic.
func TestRequest_NoInstancesLeavesTheMapNil(t *testing.T) {
	req := contract.Request{Operation: "unsubscribe", SubscriptionID: "sub-1", BlockID: "block-1", OrgID: "org-1"}

	id, d := req.Identity(), req.Desired()

	if id.Instances != nil || d.Instances != nil {
		t.Errorf("Identity().Instances, Desired().Instances = %v, %v; want nil when the request carries none", id.Instances, d.Instances)
	}
	if got := id.Instances["aws"].Config["region"]; got != "" {
		t.Errorf("Identity().Instances[aws].Config[region] = %q, want an empty string", got)
	}
}

func TestRequestMarshal_EncodesEachResourceFieldUnderItsWireNameOnlyWhenSet(t *testing.T) {
	blockLevel := contract.Request{
		Resource:          "aws.ec2-instance",
		ResourceType:      "aws.ec2-instance",
		RecordedResources: map[string]contract.RecordedResource{"aws.vpc": {Type: "aws.vpc", Identity: map[string]string{"vpcId": "vpc-0abc"}}},
		RequirementTypes:  map[string]string{"host": "aws.ec2-instance"},
	}
	cases := []struct {
		label string
		req   contract.Request
		key   string
		want  any
	}{
		{"a resource identity", contract.Request{Resource: "firewall", ResourceIdentity: map[string]string{"groupId": "sg-0abc"}},
			"resourceIdentity", map[string]any{"groupId": "sg-0abc"}},
		{"a resource type", blockLevel, "resourceType", "aws.ec2-instance"},
		{"recorded resources", blockLevel, "recordedResources",
			map[string]any{"aws.vpc": map[string]any{"type": "aws.vpc", "identity": map[string]any{"vpcId": "vpc-0abc"}}}},
		{"requirement types", blockLevel, "requirementTypes", map[string]any{"host": "aws.ec2-instance"}},
		{"a recorded resource's state, when the engine recorded it absent", contract.Request{RecordedResources: map[string]contract.RecordedResource{
			"database": {Type: "aws.ec2-postgres-database", State: contract.Absent}}}, "recordedResources",
			map[string]any{"database": map[string]any{"type": "aws.ec2-postgres-database", "state": "absent"}}},
		{"never active, when the engine proved it", contract.Request{NeverActive: true}, "neverActive", true},
		{"no resource type when unset", contract.Request{}, "resourceType", nil},
		{"no never-active key when it may have been active", contract.Request{}, "neverActive", nil},
		{"no recorded resources when unset", contract.Request{}, "recordedResources", nil},
		{"no requirement types when unset", contract.Request{}, "requirementTypes", nil},
		{"no resource key at all for a validate naming no resource", contract.Request{Operation: "validate"}, "resource", nil},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got := marshalObject(t, tc.req)

			if tc.want == nil {
				for key := range got {
					if strings.HasPrefix(key, tc.key) {
						t.Errorf("json.Marshal(%+v) carries key %q, want no key starting %q", tc.req, key, tc.key)
					}
				}
				return
			}
			wantField(t, "encoded "+tc.key, got[tc.key], tc.want)
		})
	}
}

func dependsOnResource(t *testing.T, doc map[string]any, field string) {
	t.Helper()
	dependent := schemaAt(t, doc, doc, "dependentRequired")
	if needs, _ := dependent[field].([]any); !reflect.DeepEqual(needs, []any{"resource"}) {
		t.Errorf(`dependentRequired.%s = %v, want ["resource"]`, field, dependent[field])
	}
}

func objectOfStrings(t *testing.T, doc, node map[string]any, what string) {
	t.Helper()
	if !hasType(node, "object") {
		t.Fatalf("%s type = %v, want object", what, schemaTypes(node))
	}
	if values := schemaAt(t, doc, node, "additionalProperties"); !hasType(values, "string") {
		t.Errorf("%s values type = %v, want string", what, schemaTypes(values))
	}
}

func TestEnvelopeSchema_DeclaresEachOptionalRequestFieldByItsShape(t *testing.T) {
	doc := jsonObject(t, contract.EnvelopeSchema)
	cases := []struct {
		field string
		check func(t *testing.T, prop map[string]any)
	}{
		{"resource", func(t *testing.T, prop map[string]any) {
			if !hasType(prop, "string") {
				t.Errorf("properties.resource type = %v, want string", schemaTypes(prop))
			}
		}},
		{"resourceIdentity", func(t *testing.T, prop map[string]any) {
			objectOfStrings(t, doc, prop, "properties.resourceIdentity")
			dependsOnResource(t, doc, "resourceIdentity")
		}},
		{"resourceType", func(t *testing.T, prop map[string]any) {
			if !hasType(prop, "string") {
				t.Errorf("properties.resourceType = %v, want a string", prop)
			}
			dependsOnResource(t, doc, "resourceType")
		}},
		{"recordedResources", func(t *testing.T, prop map[string]any) {
			if !hasType(prop, "object") {
				t.Fatalf("properties.recordedResources = %v, want an object", prop)
			}
			entry := schemaAt(t, doc, prop, "additionalProperties", "properties")
			if ty, _ := entry["type"].(map[string]any); !hasType(ty, "string") {
				t.Errorf("a recorded resource's type = %v, want a string", entry["type"])
			}
			identity, _ := entry["identity"].(map[string]any)
			objectOfStrings(t, doc, identity, "a recorded resource's identity")
			state, _ := entry["state"].(map[string]any)
			if !hasType(state, "string") || !reflect.DeepEqual(state["enum"], []any{"absent"}) {
				t.Errorf("a recorded resource's state = %v, want a string whose one value is absent", entry["state"])
			}
		}},
		{"requirementTypes", func(t *testing.T, prop map[string]any) {
			objectOfStrings(t, doc, prop, "properties.requirementTypes")
		}},
		{"settlesAt", func(t *testing.T, prop map[string]any) {
			if !hasType(prop, "string") || prop["format"] != "date-time" {
				t.Errorf("properties.settlesAt = %v, want a string of format date-time", prop)
			}
		}},
		{"neverActive", func(t *testing.T, prop map[string]any) {
			if !hasType(prop, "boolean") {
				t.Errorf("properties.neverActive type = %v, want boolean", schemaTypes(prop))
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			prop := schemaAt(t, doc, doc, "properties", tc.field)

			if slices.Contains(sortedStrings(doc["required"]), tc.field) {
				t.Errorf("envelope.schema.json requires %q, but a request without it must stay valid", tc.field)
			}
			tc.check(t, prop)
		})
	}
}

func TestEnvelopeSchema_IsVersion1_7_0(t *testing.T) {
	doc := jsonObject(t, contract.EnvelopeSchema)

	if doc["version"] != "1.7.0" {
		t.Errorf("envelope.schema.json version = %v, want 1.7.0: it adds neverActive, a minor change from 1.6.0", doc["version"])
	}
}
