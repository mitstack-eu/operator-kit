// The contract package carries typed Go types — Request, Response,
// Observation, Desired, Identity — decoded from envelope.schema.json and
// response.schema.json, alongside DecodeRequest/DecodeResponse and the
// Request.Identity/Request.Desired/Observation.Response helpers.
package contract_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// --- DecodeRequest decodes every valid golden, kit-native ------------------

func TestDecodeRequest_DecodesEveryValidGoldenViaKittest(t *testing.T) {
	kittest.AssertDecodesEveryValidGolden(t, func(raw []byte) error {
		_, err := contract.DecodeRequest(raw)
		return err
	}, "operation", "nonce", "createdAt", "subscriptionId", "blockId", "orgId")
}

// --- DecodeRequest field-by-field against the raw golden --------------------

// compareRequestToGolden re-decodes raw as a bare map[string]any and checks
// every field DecodeRequest is supposed to carry against it. A struct field
// missing from this comparison is drift this test exists to catch: a golden
// field that silently fails to reach the typed Request looks identical to
// one that was never sent.
func compareRequestToGolden(t *testing.T, name string, raw []byte, got contract.Request) {
	t.Helper()

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("golden %s is not valid JSON: %v", name, err)
	}
	str := func(key string) string {
		s, _ := m[key].(string)
		return s
	}

	if got.Operation != str("operation") {
		t.Errorf("%s: Operation = %q, want %q", name, got.Operation, str("operation"))
	}
	if got.SubscriptionID != str("subscriptionId") {
		t.Errorf("%s: SubscriptionID = %q, want %q", name, got.SubscriptionID, str("subscriptionId"))
	}
	if got.IdempotencyKey != str("idempotencyKey") {
		t.Errorf("%s: IdempotencyKey = %q, want %q", name, got.IdempotencyKey, str("idempotencyKey"))
	}
	if got.BlockID != str("blockId") {
		t.Errorf("%s: BlockID = %q, want %q", name, got.BlockID, str("blockId"))
	}
	if got.BlockVersion != str("blockVersion") {
		t.Errorf("%s: BlockVersion = %q, want %q", name, got.BlockVersion, str("blockVersion"))
	}
	if got.OrgID != str("orgId") {
		t.Errorf("%s: OrgID = %q, want %q", name, got.OrgID, str("orgId"))
	}
	if got.AppID != str("appId") {
		t.Errorf("%s: AppID = %q, want %q", name, got.AppID, str("appId"))
	}

	if v, ok := m["params"]; ok {
		want, _ := v.(map[string]any)
		if len(want) != len(got.Params) {
			t.Errorf("%s: Params has %d entries, want %d", name, len(got.Params), len(want))
		}
		for k, ev := range want {
			es, _ := ev.(string)
			if got.Params[k] != es {
				t.Errorf("%s: Params[%q] = %q, want %q", name, k, got.Params[k], es)
			}
		}
	}

	if v, ok := m["instances"]; ok {
		want, _ := v.(map[string]any)
		if len(want) != len(got.Instances) {
			t.Errorf("%s: Instances has %d entries, want %d", name, len(got.Instances), len(want))
		}
		for k, ev := range want {
			obj, _ := ev.(map[string]any)
			gi, ok := got.Instances[k]
			if !ok {
				t.Errorf("%s: Instances missing key %q", name, k)
				continue
			}
			wantName, _ := obj["name"].(string)
			wantEndpoint, _ := obj["endpoint"].(string)
			if gi.Name != wantName {
				t.Errorf("%s: Instances[%q].Name = %q, want %q", name, k, gi.Name, wantName)
			}
			if gi.Endpoint != wantEndpoint {
				t.Errorf("%s: Instances[%q].Endpoint = %q, want %q", name, k, gi.Endpoint, wantEndpoint)
			}
			wantConfig, _ := obj["config"].(map[string]any)
			if len(wantConfig) != len(gi.Config) {
				t.Errorf("%s: Instances[%q].Config has %d entries, want %d", name, k, len(gi.Config), len(wantConfig))
			}
			for ck, cv := range wantConfig {
				cs, _ := cv.(string)
				if gi.Config[ck] != cs {
					t.Errorf("%s: Instances[%q].Config[%q] = %q, want %q", name, k, ck, gi.Config[ck], cs)
				}
			}
		}
	}

	if v, ok := m["secrets"]; ok {
		want, _ := v.(map[string]any)
		if len(want) != len(got.Secrets) {
			t.Errorf("%s: Secrets has %d entries, want %d", name, len(got.Secrets), len(want))
		}
		for k, ev := range want {
			obj, _ := ev.(map[string]any)
			gs, ok := got.Secrets[k]
			if !ok {
				t.Errorf("%s: Secrets missing key %q", name, k)
				continue
			}
			for sk, sv := range obj {
				ss, _ := sv.(string)
				if gs[sk] != ss {
					t.Errorf("%s: Secrets[%q][%q] = %q, want %q", name, k, sk, gs[sk], ss)
				}
			}
		}
	}

	if got.OBO != str("obo") {
		t.Errorf("%s: OBO = %q, want %q", name, got.OBO, str("obo"))
	}

	if v, ok := m["secretInputs"]; ok {
		want, _ := v.(map[string]any)
		if len(want) != len(got.SecretInputs) {
			t.Errorf("%s: SecretInputs has %d entries, want %d", name, len(got.SecretInputs), len(want))
		}
		for k, ev := range want {
			es, _ := ev.(string)
			if got.SecretInputs[k] != es {
				t.Errorf("%s: SecretInputs[%q] = %q, want %q", name, k, got.SecretInputs[k], es)
			}
		}
	}

	if v, ok := m["appSecrets"]; ok {
		want, _ := v.(map[string]any)
		if len(want) != len(got.AppSecrets) {
			t.Errorf("%s: AppSecrets has %d entries, want %d", name, len(got.AppSecrets), len(want))
		}
	}

	if v, ok := m["bindings"]; ok {
		want, _ := v.(map[string]any)
		if len(want) != len(got.Bindings) {
			t.Errorf("%s: Bindings has %d entries, want %d", name, len(got.Bindings), len(want))
		}
		for k, ev := range want {
			obj, _ := ev.(map[string]any)
			gb, ok := got.Bindings[k]
			if !ok {
				t.Errorf("%s: Bindings missing key %q", name, k)
				continue
			}
			for bk, bv := range obj {
				bs, _ := bv.(string)
				if gb[bk] != bs {
					t.Errorf("%s: Bindings[%q][%q] = %q, want %q", name, k, bk, gb[bk], bs)
				}
			}
		}
	}

	if v, ok := m["bindingsAll"]; ok {
		want, _ := v.(map[string]any)
		if len(want) != len(got.BindingsAll) {
			t.Errorf("%s: BindingsAll has %d entries, want %d", name, len(got.BindingsAll), len(want))
		}
		for k, ev := range want {
			arr, _ := ev.([]any)
			gba, ok := got.BindingsAll[k]
			if !ok {
				t.Errorf("%s: BindingsAll missing key %q", name, k)
				continue
			}
			if len(arr) != len(gba) {
				t.Errorf("%s: BindingsAll[%q] has %d entries, want %d", name, k, len(gba), len(arr))
			}
		}
	}

	if v, ok := m["cloudIdentity"]; ok && v != nil {
		obj, _ := v.(map[string]any)
		if got.CloudIdentity == nil {
			t.Errorf("%s: CloudIdentity = nil, want non-nil (golden carries one)", name)
		} else {
			wantProvider, _ := obj["provider"].(string)
			wantRoleARN, _ := obj["roleArn"].(string)
			wantToken, _ := obj["webIdentityToken"].(string)
			wantAccountRef, _ := obj["accountRef"].(string)
			if got.CloudIdentity.Provider != wantProvider {
				t.Errorf("%s: CloudIdentity.Provider = %q, want %q", name, got.CloudIdentity.Provider, wantProvider)
			}
			if got.CloudIdentity.RoleARN != wantRoleARN {
				t.Errorf("%s: CloudIdentity.RoleARN = %q, want %q", name, got.CloudIdentity.RoleARN, wantRoleARN)
			}
			if got.CloudIdentity.WebIdentityToken != wantToken {
				t.Errorf("%s: CloudIdentity.WebIdentityToken = %q, want %q", name, got.CloudIdentity.WebIdentityToken, wantToken)
			}
			if got.CloudIdentity.AccountRef != wantAccountRef {
				t.Errorf("%s: CloudIdentity.AccountRef = %q, want %q", name, got.CloudIdentity.AccountRef, wantAccountRef)
			}
		}
	}

	wantCreatedAt := str("createdAt")
	wantTime, err := time.Parse(time.RFC3339, wantCreatedAt)
	if err != nil {
		t.Fatalf("%s: golden createdAt %q is not RFC3339: %v", name, wantCreatedAt, err)
	}
	if !got.CreatedAt.Equal(wantTime) {
		t.Errorf("%s: CreatedAt = %v, want %v", name, got.CreatedAt, wantTime)
	}

	if got.Nonce != str("nonce") {
		t.Errorf("%s: Nonce = %q, want %q", name, got.Nonce, str("nonce"))
	}

	if v, ok := m["priorOutputs"]; ok {
		want, _ := v.(map[string]any)
		if len(want) != len(got.PriorOutputs) {
			t.Errorf("%s: PriorOutputs has %d entries, want %d", name, len(got.PriorOutputs), len(want))
		}
		for k, ev := range want {
			es, _ := ev.(string)
			if got.PriorOutputs[k] != es {
				t.Errorf("%s: PriorOutputs[%q] = %q, want %q", name, k, got.PriorOutputs[k], es)
			}
		}
	}

	if v, ok := m["buildRequest"]; ok && v != nil {
		obj, _ := v.(map[string]any)
		if got.BuildRequest == nil {
			t.Errorf("%s: BuildRequest = nil, want non-nil (golden carries one)", name)
		} else {
			wantID, _ := obj["id"].(string)
			wantBranch, _ := obj["sourceBranch"].(string)
			wantSha, _ := obj["sourceSha"].(string)
			if got.BuildRequest.ID != wantID {
				t.Errorf("%s: BuildRequest.ID = %q, want %q", name, got.BuildRequest.ID, wantID)
			}
			if got.BuildRequest.SourceBranch != wantBranch {
				t.Errorf("%s: BuildRequest.SourceBranch = %q, want %q", name, got.BuildRequest.SourceBranch, wantBranch)
			}
			if got.BuildRequest.SourceSha != wantSha {
				t.Errorf("%s: BuildRequest.SourceSha = %q, want %q", name, got.BuildRequest.SourceSha, wantSha)
			}
		}
	}
}

func TestDecodeRequest_FieldsMatchGoldenJSON(t *testing.T) {
	valid, err := contract.ValidGoldens()
	if err != nil {
		t.Fatalf("contract.ValidGoldens(): %v", err)
	}
	for name, raw := range valid {
		t.Run(name, func(t *testing.T) {
			got, err := contract.DecodeRequest(raw)
			if err != nil {
				t.Fatalf("DecodeRequest(%s): %v", name, err)
			}
			compareRequestToGolden(t, name, raw, got)
		})
	}
}

// --- DecodeRequest rejects every invalid golden, naming the field ----------

// wantInvalidRequestSubstr maps every invalid-* request golden name to a
// substring DecodeRequest's error must contain. A golden in
// contract.InvalidGoldens() with no row here is a table gap this test
// itself catches, so a new invalid golden cannot ship silently unchecked.
var wantInvalidRequestSubstr = map[string]string{
	"invalid-missing-nonce": "nonce",
	"invalid-unknown-verb":  "operation",
}

func TestDecodeRequest_RejectsInvalidGoldensNamingField(t *testing.T) {
	invalid, err := contract.InvalidGoldens()
	if err != nil {
		t.Fatalf("contract.InvalidGoldens(): %v", err)
	}

	for name := range invalid {
		if _, ok := wantInvalidRequestSubstr[name]; !ok {
			t.Errorf("invalid golden %q has no row in wantInvalidRequestSubstr — "+
				"a new invalid golden must be added to this table", name)
		}
	}

	for name, raw := range invalid {
		substr, ok := wantInvalidRequestSubstr[name]
		if !ok {
			continue
		}
		t.Run(name, func(t *testing.T) {
			_, err := contract.DecodeRequest(raw)
			if err == nil {
				t.Fatalf("DecodeRequest(%s) succeeded, want an error naming %q", name, substr)
			}
			if !strings.Contains(err.Error(), substr) {
				t.Errorf("DecodeRequest(%s) error = %q, want it to contain %q", name, err.Error(), substr)
			}
		})
	}
}

// --- DecodeResponse field-by-field against the raw golden -------------------

func compareResponseToGolden(t *testing.T, name string, raw []byte, got contract.Response) {
	t.Helper()

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("golden %s is not valid JSON: %v", name, err)
	}
	str := func(key string) string {
		s, _ := m[key].(string)
		return s
	}

	if v, ok := m["outputs"]; ok && v != nil {
		want, _ := v.(map[string]any)
		if len(want) != len(got.Outputs) {
			t.Errorf("%s: Outputs has %d entries, want %d", name, len(got.Outputs), len(want))
		}
		for k, ev := range want {
			es, _ := ev.(string)
			if got.Outputs[k] != es {
				t.Errorf("%s: Outputs[%q] = %q, want %q", name, k, got.Outputs[k], es)
			}
		}
	} else if got.Outputs != nil {
		t.Errorf("%s: Outputs = %v, want nil (golden omits or nulls it)", name, got.Outputs)
	}

	if v, ok := m["secretOutputs"]; ok && v != nil {
		want, _ := v.(map[string]any)
		if len(want) != len(got.SecretOutputs) {
			t.Errorf("%s: SecretOutputs has %d entries, want %d", name, len(got.SecretOutputs), len(want))
		}
		for k, ev := range want {
			es, _ := ev.(string)
			if got.SecretOutputs[k] != es {
				t.Errorf("%s: SecretOutputs[%q] = %q, want %q", name, k, got.SecretOutputs[k], es)
			}
		}
	} else if got.SecretOutputs != nil {
		t.Errorf("%s: SecretOutputs = %v, want nil (golden omits or nulls it)", name, got.SecretOutputs)
	}

	if got.Error != str("error") {
		t.Errorf("%s: Error = %q, want %q", name, got.Error, str("error"))
	}

	wantPending, _ := m["pending"].(bool)
	if got.Pending != wantPending {
		t.Errorf("%s: Pending = %v, want %v", name, got.Pending, wantPending)
	}

	if got.StatusDetail != str("statusDetail") {
		t.Errorf("%s: StatusDetail = %q, want %q", name, got.StatusDetail, str("statusDetail"))
	}

	if got.LastError != str("lastError") {
		t.Errorf("%s: LastError = %q, want %q", name, got.LastError, str("lastError"))
	}

	if v, present := m["absent"]; !present || v == nil {
		if got.Absent != nil {
			t.Errorf("%s: Absent = %v, want nil (golden omits or nulls it)", name, *got.Absent)
		}
	} else {
		wantAbsent, _ := v.(bool)
		if got.Absent == nil {
			t.Errorf("%s: Absent = nil, want pointer to %v", name, wantAbsent)
		} else if *got.Absent != wantAbsent {
			t.Errorf("%s: *Absent = %v, want %v", name, *got.Absent, wantAbsent)
		}
	}
}

func TestDecodeResponse_FieldsMatchGoldenJSON(t *testing.T) {
	valid, err := contract.ValidResponseGoldens()
	if err != nil {
		t.Fatalf("contract.ValidResponseGoldens(): %v", err)
	}
	for name, raw := range valid {
		t.Run(name, func(t *testing.T) {
			got, err := contract.DecodeResponse(raw)
			if err != nil {
				t.Fatalf("DecodeResponse(%s): %v", name, err)
			}
			compareResponseToGolden(t, name, raw, got)
		})
	}
}

// TestDecodeResponse_ReencodedResponsesConformViaKittest decodes every valid
// response golden into contract.Response, re-encodes it with json.Marshal,
// and hands every re-encoded body — not the original golden bytes — to
// kittest.AssertEmitsConformingResponses. That proves the typed Response
// round-trips into a body the kit's own conformance harness still accepts.
func TestDecodeResponse_ReencodedResponsesConformViaKittest(t *testing.T) {
	valid, err := contract.ValidResponseGoldens()
	if err != nil {
		t.Fatalf("contract.ValidResponseGoldens(): %v", err)
	}

	bodies := make(map[string][]byte, len(valid))
	for name, raw := range valid {
		got, err := contract.DecodeResponse(raw)
		if err != nil {
			t.Fatalf("DecodeResponse(%s): %v", name, err)
		}
		reencoded, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("json.Marshal(DecodeResponse(%s)): %v", name, err)
		}
		bodies[name] = reencoded
	}

	kittest.AssertEmitsConformingResponses(t, bodies)
}

// --- DecodeResponse rejects every invalid golden, naming the field ---------

// wantInvalidResponseSubstr maps every invalid-* response golden name to a
// substring DecodeResponse's error must contain. As with
// wantInvalidRequestSubstr, a golden with no row here fails this test, so a
// new invalid response golden cannot ship unchecked.
var wantInvalidResponseSubstr = map[string]string{
	"invalid-absent-not-bool":            "absent",
	"invalid-outputs-not-object":         "outputs",
	"invalid-outputs-not-strings":        "outputs",
	"invalid-pending-not-bool":           "pending",
	"invalid-secret-outputs-not-strings": "secretOutputs",

	"invalid-resource-secret-outputs-not-object":       "resourceSecretOutputs",
	"invalid-resource-secret-outputs-entry-not-object": "resourceSecretOutputs",
	"invalid-resource-secret-outputs-value-not-string": "resourceSecretOutputs",
}

func TestDecodeResponse_RejectsInvalidGoldensNamingField(t *testing.T) {
	invalid, err := contract.InvalidResponseGoldens()
	if err != nil {
		t.Fatalf("contract.InvalidResponseGoldens(): %v", err)
	}

	for name := range invalid {
		if _, ok := wantInvalidResponseSubstr[name]; !ok {
			t.Errorf("invalid response golden %q has no row in wantInvalidResponseSubstr — "+
				"a new invalid response golden must be added to this table", name)
		}
	}

	for name, raw := range invalid {
		substr, ok := wantInvalidResponseSubstr[name]
		if !ok {
			continue
		}
		t.Run(name, func(t *testing.T) {
			_, err := contract.DecodeResponse(raw)
			if err == nil {
				t.Fatalf("DecodeResponse(%s) succeeded, want an error naming %q", name, substr)
			}
			if !strings.Contains(err.Error(), substr) {
				t.Errorf("DecodeResponse(%s) error = %q, want it to contain %q", name, err.Error(), substr)
			}
		})
	}
}

// --- Request.Identity() / Request.Desired() ---------------------------------

func TestRequestIdentity_SubscribeGolden(t *testing.T) {
	valid, err := contract.ValidGoldens()
	if err != nil {
		t.Fatalf("contract.ValidGoldens(): %v", err)
	}
	req, err := contract.DecodeRequest(valid["subscribe"])
	if err != nil {
		t.Fatalf("DecodeRequest(subscribe): %v", err)
	}

	id := req.Identity()
	if id.SubscriptionID != req.SubscriptionID {
		t.Errorf("Identity().SubscriptionID = %q, want %q", id.SubscriptionID, req.SubscriptionID)
	}
	if id.BlockID != req.BlockID {
		t.Errorf("Identity().BlockID = %q, want %q", id.BlockID, req.BlockID)
	}
	if id.BlockVersion != req.BlockVersion {
		t.Errorf("Identity().BlockVersion = %q, want %q", id.BlockVersion, req.BlockVersion)
	}
	if id.OrgID != req.OrgID {
		t.Errorf("Identity().OrgID = %q, want %q", id.OrgID, req.OrgID)
	}
	if id.AppID != req.AppID {
		t.Errorf("Identity().AppID = %q, want %q", id.AppID, req.AppID)
	}
	if len(id.PriorOutputs) != 0 {
		t.Errorf("Identity().PriorOutputs = %v, want empty (subscribe golden carries none)", id.PriorOutputs)
	}
	if id.CloudIdentity == nil {
		t.Fatal("Identity().CloudIdentity = nil, want non-nil (subscribe golden carries one)")
	}
	if id.CloudIdentity.Provider != req.CloudIdentity.Provider {
		t.Errorf("Identity().CloudIdentity.Provider = %q, want %q", id.CloudIdentity.Provider, req.CloudIdentity.Provider)
	}
}

func TestRequestIdentity_UnsubscribeGolden_CarriesPriorOutputs(t *testing.T) {
	valid, err := contract.ValidGoldens()
	if err != nil {
		t.Fatalf("contract.ValidGoldens(): %v", err)
	}
	req, err := contract.DecodeRequest(valid["unsubscribe"])
	if err != nil {
		t.Fatalf("DecodeRequest(unsubscribe): %v", err)
	}

	id := req.Identity()
	if id.SubscriptionID != req.SubscriptionID {
		t.Errorf("Identity().SubscriptionID = %q, want %q", id.SubscriptionID, req.SubscriptionID)
	}
	if len(id.PriorOutputs) == 0 {
		t.Fatal("Identity().PriorOutputs is empty — the unsubscribe golden's priorOutputs must reach Identity")
	}
	if got, want := id.PriorOutputs["instanceId"], "i-0a1b2c3d4e5f67890"; got != want {
		t.Errorf("Identity().PriorOutputs[instanceId] = %q, want %q", got, want)
	}
}

func TestRequestDesired_SubscribeGolden(t *testing.T) {
	valid, err := contract.ValidGoldens()
	if err != nil {
		t.Fatalf("contract.ValidGoldens(): %v", err)
	}
	req, err := contract.DecodeRequest(valid["subscribe"])
	if err != nil {
		t.Fatalf("DecodeRequest(subscribe): %v", err)
	}

	d := req.Desired()
	if d.SubscriptionID != req.SubscriptionID {
		t.Errorf("Desired().SubscriptionID = %q, want %q", d.SubscriptionID, req.SubscriptionID)
	}
	if d.OrgID != req.OrgID {
		t.Errorf("Desired().OrgID = %q, want %q", d.OrgID, req.OrgID)
	}
	if len(d.Params) != len(req.Params) {
		t.Errorf("Desired().Params has %d entries, want %d", len(d.Params), len(req.Params))
	}
	for k, v := range req.Params {
		if d.Params[k] != v {
			t.Errorf("Desired().Params[%q] = %q, want %q", k, d.Params[k], v)
		}
	}
	if len(d.SecretInputs) != len(req.SecretInputs) {
		t.Errorf("Desired().SecretInputs has %d entries, want %d", len(d.SecretInputs), len(req.SecretInputs))
	}
	for k, v := range req.SecretInputs {
		if d.SecretInputs[k] != v {
			t.Errorf("Desired().SecretInputs[%q] = %q, want %q", k, d.SecretInputs[k], v)
		}
	}
	if len(d.Instances) != len(req.Instances) {
		t.Errorf("Desired().Instances has %d entries, want %d", len(d.Instances), len(req.Instances))
	}
	if d.BuildRequest != nil || req.BuildRequest != nil {
		if (d.BuildRequest == nil) != (req.BuildRequest == nil) {
			t.Errorf("Desired().BuildRequest nilness = %v, want %v", d.BuildRequest == nil, req.BuildRequest == nil)
		}
	}
}

func TestRequestDesired_UnsubscribeGolden(t *testing.T) {
	valid, err := contract.ValidGoldens()
	if err != nil {
		t.Fatalf("contract.ValidGoldens(): %v", err)
	}
	req, err := contract.DecodeRequest(valid["unsubscribe"])
	if err != nil {
		t.Fatalf("DecodeRequest(unsubscribe): %v", err)
	}

	d := req.Desired()
	if d.SubscriptionID != req.SubscriptionID {
		t.Errorf("Desired().SubscriptionID = %q, want %q", d.SubscriptionID, req.SubscriptionID)
	}
	if len(d.PriorOutputs) == 0 {
		t.Fatal("Desired().PriorOutputs is empty — Desired embeds Identity, which must carry the unsubscribe golden's priorOutputs")
	}
	if got, want := d.PriorOutputs["instanceId"], "i-0a1b2c3d4e5f67890"; got != want {
		t.Errorf("Desired().PriorOutputs[instanceId] = %q, want %q", got, want)
	}
}

// --- Identity carries the resolved tool instances ---------------------------

// decodeGolden decodes the named valid request golden, failing the test when
// the golden set has no such entry or it does not decode.
func decodeGolden(t *testing.T, name string) contract.Request {
	t.Helper()
	valid, err := contract.ValidGoldens()
	if err != nil {
		t.Fatalf("contract.ValidGoldens(): %v", err)
	}
	raw, ok := valid[name]
	if !ok {
		t.Fatalf("contract.ValidGoldens() has no %q entry", name)
	}
	req, err := contract.DecodeRequest(raw)
	if err != nil {
		t.Fatalf("DecodeRequest(%s): %v", name, err)
	}
	return req
}

// assertSameInstances compares two instance maps entry for entry, naming
// what diverged.
func assertSameInstances(t *testing.T, what string, got, want map[string]contract.InstanceConfig) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s has %d entries, want %d", what, len(got), len(want))
	}
	for key, wi := range want {
		gi, ok := got[key]
		if !ok {
			t.Errorf("%s is missing key %q", what, key)
			continue
		}
		if gi.Name != wi.Name {
			t.Errorf("%s[%q].Name = %q, want %q", what, key, gi.Name, wi.Name)
		}
		if gi.Endpoint != wi.Endpoint {
			t.Errorf("%s[%q].Endpoint = %q, want %q", what, key, gi.Endpoint, wi.Endpoint)
		}
		if len(gi.Config) != len(wi.Config) {
			t.Errorf("%s[%q].Config has %d entries, want %d", what, key, len(gi.Config), len(wi.Config))
		}
		for ck, cv := range wi.Config {
			if gi.Config[ck] != cv {
				t.Errorf("%s[%q].Config[%q] = %q, want %q", what, key, ck, gi.Config[ck], cv)
			}
		}
	}
}

// TestRequestIdentity_GoldensCarryInstances pins that the resolved tool
// instances reach Identity. Observe and Delete are handed an Identity and
// nothing else, so a block scoped to a region reads that region here or
// not at all.
func TestRequestIdentity_GoldensCarryInstances(t *testing.T) {
	for _, name := range []string{"subscribe", "unsubscribe", "upgrade", "validate"} {
		t.Run(name, func(t *testing.T) {
			req := decodeGolden(t, name)
			if len(req.Instances) == 0 {
				t.Fatalf("the %s golden carries no instances — every valid golden must, "+
					"or this test proves nothing about what Identity carries", name)
			}

			assertSameInstances(t, "Identity().Instances", req.Identity().Instances, req.Instances)
		})
	}
}

// TestRequestIdentity_UnsubscribeGolden_CarriesInstanceRegion pins the one
// value the unsubscribe path turns on: the region a region-scoped block's
// Delete addresses its resource by.
func TestRequestIdentity_UnsubscribeGolden_CarriesInstanceRegion(t *testing.T) {
	req := decodeGolden(t, "unsubscribe")

	id := req.Identity()
	if len(id.Instances) == 0 {
		t.Fatal("Identity().Instances is empty — the unsubscribe golden's instances must reach Identity")
	}
	if got, want := id.Instances["aws"].Config["region"], "eu-west-1"; got != want {
		t.Errorf("Identity().Instances[aws].Config[region] = %q, want %q", got, want)
	}
}

// TestRequestDesired_InstancesPromotedThroughIdentity pins the promotion
// round-trip: with Instances living on Identity, d.Instances inside a
// block's Ensure still resolves, and resolves to the same value the
// embedded Identity carries.
func TestRequestDesired_InstancesPromotedThroughIdentity(t *testing.T) {
	for _, name := range []string{"subscribe", "unsubscribe"} {
		t.Run(name, func(t *testing.T) {
			req := decodeGolden(t, name)
			d := req.Desired()

			assertSameInstances(t, "Desired().Instances", d.Instances, req.Instances)
			assertSameInstances(t, "Desired().Identity.Instances", d.Identity.Instances, req.Instances)

			if got, want := d.Instances["aws"].Config["region"], "eu-west-1"; got != want {
				t.Errorf("Desired().Instances[aws].Config[region] = %q, want %q", got, want)
			}
			if got, want := d.Identity.Instances["aws"].Config["region"], "eu-west-1"; got != want {
				t.Errorf("Desired().Identity.Instances[aws].Config[region] = %q, want %q", got, want)
			}
		})
	}
}

// TestRequestIdentity_NoInstancesLeavesTheMapNil pins the legitimate
// "provisioned nothing" case: a request with no instances gives a block a
// nil map, which reads as an empty region rather than panicking.
func TestRequestIdentity_NoInstancesLeavesTheMapNil(t *testing.T) {
	req := contract.Request{Operation: "unsubscribe", SubscriptionID: "sub-1", BlockID: "block-1", OrgID: "org-1"}

	id := req.Identity()
	if id.Instances != nil {
		t.Errorf("Identity().Instances = %v, want nil when the request carries none", id.Instances)
	}
	if got := id.Instances["aws"].Config["region"]; got != "" {
		t.Errorf("Identity().Instances[aws].Config[region] = %q, want an empty string", got)
	}
	if d := req.Desired(); d.Instances != nil {
		t.Errorf("Desired().Instances = %v, want nil when the request carries none", d.Instances)
	}
}

// --- Observation.Response() -------------------------------------------------

func TestObservationResponse_PresentStateSetsAbsentFalse(t *testing.T) {
	obs := contract.Observation{
		State:         contract.Present,
		Outputs:       map[string]string{"instanceId": "i-0123456789abcdef0"},
		SecretOutputs: map[string]string{"apiKey": "sk-example-not-a-real-secret"},
		StatusDetail:  "converged",
	}

	resp := obs.Response()
	if resp.Absent == nil {
		t.Fatal("Response().Absent = nil, want a pointer to false for State=Present")
	}
	if *resp.Absent != false {
		t.Errorf("*Response().Absent = %v, want false for State=Present", *resp.Absent)
	}
	if resp.Pending {
		t.Error("Response().Pending = true, want false")
	}
	if resp.Outputs["instanceId"] != obs.Outputs["instanceId"] {
		t.Errorf("Response().Outputs[instanceId] = %q, want %q", resp.Outputs["instanceId"], obs.Outputs["instanceId"])
	}
	if resp.SecretOutputs["apiKey"] != obs.SecretOutputs["apiKey"] {
		t.Errorf("Response().SecretOutputs[apiKey] = %q, want %q", resp.SecretOutputs["apiKey"], obs.SecretOutputs["apiKey"])
	}
	if resp.StatusDetail != obs.StatusDetail {
		t.Errorf("Response().StatusDetail = %q, want %q", resp.StatusDetail, obs.StatusDetail)
	}
}

func TestObservationResponse_AbsentStateSetsAbsentTrue(t *testing.T) {
	obs := contract.Observation{State: contract.Absent, StatusDetail: "resource is gone"}

	resp := obs.Response()
	if resp.Absent == nil {
		t.Fatal("Response().Absent = nil, want a pointer to true for State=Absent")
	}
	if *resp.Absent != true {
		t.Errorf("*Response().Absent = %v, want true for State=Absent", *resp.Absent)
	}
	if resp.Pending {
		t.Error("Response().Pending = true, want false")
	}
	if resp.StatusDetail != obs.StatusDetail {
		t.Errorf("Response().StatusDetail = %q, want %q", resp.StatusDetail, obs.StatusDetail)
	}
}

func TestObservationResponse_UnknownStateLeavesAbsentNil(t *testing.T) {
	obs := contract.Observation{State: contract.Unknown, StatusDetail: "checking"}

	resp := obs.Response()
	if resp.Absent != nil {
		t.Errorf("Response().Absent = %v, want nil for State=Unknown", *resp.Absent)
	}
	if resp.Pending {
		t.Error("Response().Pending = true, want false")
	}
	if resp.StatusDetail != obs.StatusDetail {
		t.Errorf("Response().StatusDetail = %q, want %q", resp.StatusDetail, obs.StatusDetail)
	}
}

// --- Response{} with nil Outputs marshals to JSON null, and the engine
// tolerates it -------------------------------------------------------------

func TestResponseMarshal_NilOutputsEncodesJSONNullAndConforms(t *testing.T) {
	resp := contract.Response{}

	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal(Response{}): %v", err)
	}
	if want := `{"outputs":null}`; string(raw) != want {
		t.Errorf("json.Marshal(Response{}) = %s, want %s", raw, want)
	}
	if problems := kittest.CheckResponse(raw); len(problems) != 0 {
		t.Errorf("kittest.CheckResponse(%s) = %v, want no problems — the engine tolerates null outputs", raw, problems)
	}
}

// --- DecodeRequest against a mutated subscribe golden -----------------------

// mutateSubscribeGolden decodes the subscribe golden fresh into a
// map[string]any, hands it to mutate for in-place editing, and re-marshals
// the result. Decoding fresh on every call means a mutate func that edits a
// nested map (such as cloudIdentity or buildRequest) never leaks into
// another test's copy.
func mutateSubscribeGolden(t *testing.T, mutate func(m map[string]any)) []byte {
	t.Helper()

	valid, err := contract.ValidGoldens()
	if err != nil {
		t.Fatalf("contract.ValidGoldens(): %v", err)
	}
	golden, ok := valid["subscribe"]
	if !ok {
		t.Fatal(`contract.ValidGoldens() has no "subscribe" entry`)
	}

	var m map[string]any
	if err := json.Unmarshal(golden, &m); err != nil {
		t.Fatalf("unmarshal subscribe golden: %v", err)
	}
	mutate(m)

	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("re-marshal mutated subscribe golden: %v", err)
	}
	return raw
}

func TestDecodeRequest_UnmodifiedSubscribeGoldenStillDecodes(t *testing.T) {
	raw := mutateSubscribeGolden(t, func(m map[string]any) {})

	if _, err := contract.DecodeRequest(raw); err != nil {
		t.Fatalf("DecodeRequest(unmodified subscribe golden): %v, want no error — "+
			"the mutation helper must round-trip a golden unchanged when mutate does nothing", err)
	}
}

func TestDecodeRequest_CreatedAtWrongType(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{"number", 12345},
		{"bool", true},
		{"object", map[string]any{"x": 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := mutateSubscribeGolden(t, func(m map[string]any) {
				m["createdAt"] = tt.value
			})

			_, err := contract.DecodeRequest(raw)
			if err == nil {
				t.Fatal("DecodeRequest() succeeded, want an error naming \"createdAt\"")
			}
			if !strings.Contains(err.Error(), "createdAt") {
				t.Errorf("DecodeRequest() error = %q, want it to contain %q", err.Error(), "createdAt")
			}
		})
	}
}

func TestDecodeRequest_CreatedAtNotRFC3339String(t *testing.T) {
	raw := mutateSubscribeGolden(t, func(m map[string]any) {
		m["createdAt"] = "not-a-timestamp"
	})

	_, err := contract.DecodeRequest(raw)
	if err == nil {
		t.Fatal("DecodeRequest() succeeded, want an error naming \"createdAt\"")
	}
	if !strings.Contains(err.Error(), "createdAt") {
		t.Errorf("DecodeRequest() error = %q, want it to contain %q", err.Error(), "createdAt")
	}
}

func TestDecodeRequest_CloudIdentityMissingRequiredField(t *testing.T) {
	tests := []struct {
		removeKey  string
		wantSubstr string
	}{
		{"provider", "cloudIdentity.provider"},
		{"roleArn", "cloudIdentity.roleArn"},
		{"webIdentityToken", "cloudIdentity.webIdentityToken"},
	}

	for _, tt := range tests {
		t.Run(tt.removeKey, func(t *testing.T) {
			raw := mutateSubscribeGolden(t, func(m map[string]any) {
				ci, ok := m["cloudIdentity"].(map[string]any)
				if !ok {
					t.Fatal(`subscribe golden has no "cloudIdentity" object`)
				}
				delete(ci, tt.removeKey)
			})

			_, err := contract.DecodeRequest(raw)
			if err == nil {
				t.Fatalf("DecodeRequest() succeeded, want an error naming %q", tt.wantSubstr)
			}
			if !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Errorf("DecodeRequest() error = %q, want it to contain %q", err.Error(), tt.wantSubstr)
			}
		})
	}
}

func TestDecodeRequest_BuildRequestMissingRequiredField(t *testing.T) {
	full := map[string]any{
		"id":           "b1",
		"sourceBranch": "main",
		"sourceSha":    "abc123",
	}

	tests := []struct {
		removeKey  string
		wantSubstr string
	}{
		{"id", "buildRequest.id"},
		{"sourceBranch", "buildRequest.sourceBranch"},
		{"sourceSha", "buildRequest.sourceSha"},
	}

	for _, tt := range tests {
		t.Run(tt.removeKey, func(t *testing.T) {
			raw := mutateSubscribeGolden(t, func(m map[string]any) {
				br := map[string]any{}
				for k, v := range full {
					if k != tt.removeKey {
						br[k] = v
					}
				}
				m["buildRequest"] = br
			})

			_, err := contract.DecodeRequest(raw)
			if err == nil {
				t.Fatalf("DecodeRequest() succeeded, want an error naming %q", tt.wantSubstr)
			}
			if !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Errorf("DecodeRequest() error = %q, want it to contain %q", err.Error(), tt.wantSubstr)
			}
		})
	}
}

// --- DecodeResponse type-mismatch naming covers string fields too ----------

func TestDecodeResponse_StatusDetailWrongType(t *testing.T) {
	raw := []byte(`{"statusDetail": 5}`)

	_, err := contract.DecodeResponse(raw)
	if err == nil {
		t.Fatal("DecodeResponse() succeeded, want an error naming \"statusDetail\"")
	}
	if !strings.Contains(err.Error(), "statusDetail") {
		t.Errorf("DecodeResponse() error = %q, want it to contain %q", err.Error(), "statusDetail")
	}
}
