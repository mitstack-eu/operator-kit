package contract_test

// resource_request_test.go pins the request half of observing one declared
// resource: a validate may name the resource it concerns under "resource",
// and Desired carries it across to the resource's observer. A response's
// ensure outcome may report the identity the ensure stored.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

func TestDecodeRequest_ReadsTheNamedResource(t *testing.T) {
	raw := mutateSubscribeGolden(t, func(m map[string]any) {
		m["operation"] = "validate"
		m["resource"] = "firewall"
	})

	req, err := contract.DecodeRequest(raw)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if req.Resource != "firewall" {
		t.Errorf("Resource = %q, want %q", req.Resource, "firewall")
	}
}

func TestRequestDesired_CarriesTheNamedResource(t *testing.T) {
	raw := mutateSubscribeGolden(t, func(m map[string]any) {
		m["operation"] = "validate"
		m["resource"] = "firewall"
	})
	req, err := contract.DecodeRequest(raw)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	d := req.Desired()
	if d.Resource != "firewall" {
		t.Errorf("Desired().Resource = %q, want %q", d.Resource, "firewall")
	}
	if d.SubscriptionID != req.SubscriptionID || !reflect.DeepEqual(d.Params, req.Params) {
		t.Errorf("Desired() lost the subscription or params: %+v", d)
	}
}

func TestDecodeRequest_NamingNoResourceLeavesTheResourceFieldsEmpty(t *testing.T) {
	for _, name := range []string{"subscribe", "upgrade", "unsubscribe", "validate"} {
		t.Run(name, func(t *testing.T) {
			req := decodeGolden(t, name)
			if req.Resource != "" {
				t.Errorf("%s names no resource, but decodes to Resource %q", name, req.Resource)
			}
			d := req.Desired()
			if d.Resource != "" {
				t.Errorf("%s names no resource, but Desired() carries Resource %q", name, d.Resource)
			}
		})
	}
}

func TestRequestMarshal_OmitsTheResourceFieldsWhenNoResourceIsNamed(t *testing.T) {
	raw, err := json.Marshal(contract.Request{Operation: "validate"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"resource`) {
		t.Errorf("json.Marshal = %s, want no resource key for a request naming no resource", raw)
	}
}

func TestCallOutcome_EncodesIdentityOnlyWhenReported(t *testing.T) {
	raw, err := json.Marshal(contract.CallOutcome{Identity: map[string]string{"groupId": "sg-0abc"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), `{"identity":{"groupId":"sg-0abc"}}`; got != want {
		t.Errorf("json.Marshal = %s, want %s", got, want)
	}

	raw, err = json.Marshal(contract.CallOutcome{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), `{}`; got != want {
		t.Errorf("json.Marshal(zero CallOutcome) = %s, want %s: an ensure that reports no identity encodes as before", got, want)
	}
}

func TestDecodeResponse_ReadsTheIdentityAnEnsureReported(t *testing.T) {
	resp, err := contract.DecodeResponse([]byte(`{"resources":{"firewall":{"ensure":{"identity":{"groupId":"sg-0abc"},"outputs":{"groupArn":"arn:sg"}}}}}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	ensure := resp.Resources["firewall"].Ensure
	if ensure == nil {
		t.Fatal("firewall has no ensure outcome")
	}
	if want := map[string]string{"groupId": "sg-0abc"}; !reflect.DeepEqual(ensure.Identity, want) {
		t.Errorf("ensure Identity = %v, want %v", ensure.Identity, want)
	}
	if want := map[string]string{"groupArn": "arn:sg"}; !reflect.DeepEqual(ensure.Outputs, want) {
		t.Errorf("ensure Outputs = %v, want %v", ensure.Outputs, want)
	}
}
