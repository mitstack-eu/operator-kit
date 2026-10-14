package main

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/webhook"
)

var desired = contract.Desired{Identity: contract.Identity{SubscriptionID: "sub-cluster", BlockID: "hcloud-k3s-cluster", OrgID: "org-fixture"}}

func TestDeclaredResources(t *testing.T) {
	raw, err := os.ReadFile("type-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := contract.ParseTypeSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	op, observers, err := newOperator(nil)
	if err != nil {
		t.Fatal(err)
	}

	kittest.AssertBlock(t, kittest.Block{Manifest: manifest, Types: snap, Handler: op, Observers: observers, Provider: op.cloud, Desired: desired, Upgrade: &desired})
}

func TestUnsubscribeDeletesEveryObjectSubscribeCreated(t *testing.T) {
	op, _, err := newOperator(nil)
	if err != nil {
		t.Fatal(err)
	}

	_, ensureErr := op.EnsureResources(context.Background(), desired)
	_, deleteErr := op.Delete(context.Background(), desired)

	if left, _ := op.cloud.ListTagged(context.Background(), desired.SubscriptionID); ensureErr != nil || deleteErr != nil || len(left) != 0 {
		t.Errorf("ensure %v, delete %v, objects left %v; want no error and none left", ensureErr, deleteErr, left)
	}
}

// The issue fails because the capability implements no issue handler.
func TestNoSecretLeak(t *testing.T) {
	const secret = "hcloud-token-fixture-7f3a"
	subscribe := map[string]any{"operation": "subscribe", "subscriptionId": "sub-cluster", "blockId": "hcloud-k3s-cluster",
		"orgId": "org-fixture", "secretInputs": map[string]any{"hcloudToken": secret}}
	issue := map[string]any{"operation": "issue", "issueAppId": "app-shop"}
	for k, v := range subscribe {
		if k != "operation" {
			issue[k] = v
		}
	}

	kittest.AssertNoSecretLeak(t, kittest.SecretLeak{
		Secrets: []string{secret},
		Serve: func(logs *webhook.LogBuffer) (http.Handler, *kittest.Signer) {
			s := kittest.NewSigner(t, "kid-cluster", 7)
			h, err := newHandler(s.JWKSServer(t), logs)
			if err != nil {
				t.Fatal(err)
			}
			return h, s
		},
		Requests: []kittest.LeakRequest{{Name: "subscribe", Body: subscribe}, {Name: "issue", Body: issue, Fails: true}},
	})
}
