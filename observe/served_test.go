package observe_test

// served_test.go drives the shared aws.ec2-instance observer the way an
// operator runs it: a fixture block served through handler.New with the
// observer under Config.Observers, whose block-level ensure reports the
// instance it created. The observer reads that reported identity against a
// fake EC2 API, so the answer's entry is what the API holds.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/observe"
	"github.com/mitstack-eu/operator-kit/v2/webhook"
)

const hostBlockManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: host-block
  name: Host Block
spec:
  version: 1.0.0
  resources: [aws.ec2-instance]
  provides: [aws.ec2-instance]
  lifecycle:
    endpoint: https://host-block.example/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`

// hostBlock creates its instance in the fake EC2 API and reports it.
type hostBlock struct{ api *fakeEC2 }

func (b hostBlock) Ensure(ctx context.Context, d contract.Desired) error {
	_, err := b.EnsureResources(ctx, d)
	return err
}

func (b hostBlock) EnsureResources(ctx context.Context, d contract.Desired) ([]contract.CreatedResource, error) {
	b.api.mu.Lock()
	b.api.instances = map[string]observe.EC2InstanceDescription{"i-0fixture": {
		InstanceID: "i-0fixture", State: "running", PublicIP: "198.51.100.4", Region: "eu-west-1",
		Tags: map[string]string{observe.AWSSubscriptionTag: d.SubscriptionID},
	}}
	b.api.mu.Unlock()
	return []contract.CreatedResource{{Name: "aws.ec2-instance", Type: "aws.ec2-instance", Identity: map[string]string{"instanceId": "i-0fixture"}}}, nil
}

func (b hostBlock) Observe(ctx context.Context, d contract.Desired) (contract.Observation, error) {
	return contract.Observation{State: contract.Unknown}, nil
}

func (b hostBlock) Delete(ctx context.Context, d contract.Desired) (map[string]string, error) {
	b.api.lose("i-0fixture")
	return nil, nil
}

func TestEC2Instance_ServedThroughTheHandlerObservesTheInstanceTheBlockReported(t *testing.T) {
	api := &fakeEC2{}
	s := kittest.NewSigner(t, "kid-observe", 31)
	h, err := handler.New(handler.Config{
		Resource:     hostBlock{api: api},
		Manifest:     []byte(hostBlockManifest),
		Observers:    map[string]handler.Observer{"aws.ec2-instance": observe.EC2Instance{API: api}},
		JWKSURL:      s.JWKSServer(t),
		VerbBudget:   3,
		PollInterval: time.Millisecond,
		Sleep:        func(context.Context, time.Duration) {},
	})
	if err != nil {
		t.Fatalf("handler.New: %v", err)
	}
	obo, err := webhook.MintOBO(s.Private, s.Kid, webhook.OBOClaims{OrgID: "org-1", SubscriptionID: "sub-1"}, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body := kittest.Envelope("nonce-observe-served", time.Now(), map[string]any{
		"operation": "subscribe", "subscriptionId": "sub-1", "blockId": "host-block", "orgId": "org-1", "obo": obo,
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, s.HookRequest(t, "/hook", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	resp, err := contract.DecodeResponse(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	o := resp.Resources["aws.ec2-instance"].Observe
	if o == nil || o.State != contract.Present || o.Identity["instanceId"] != "i-0fixture" || o.Type != "aws.ec2-instance" {
		t.Fatalf("aws.ec2-instance observe entry = %+v, want present as aws.ec2-instance with the reported instance id", o)
	}
	if o.Outputs["publicIp"] != "198.51.100.4" || resp.Outputs["hostId"] != "i-0fixture" {
		t.Errorf("observe outputs %v, answer outputs %v; want what the API holds for i-0fixture", o.Outputs, resp.Outputs)
	}
	if len(api.asked) == 0 || api.asked[0] != "i-0fixture" {
		t.Errorf("the EC2 API was asked about %q, want the reported i-0fixture", api.asked)
	}
}
