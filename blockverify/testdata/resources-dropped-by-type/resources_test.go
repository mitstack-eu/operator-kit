package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/mitstack-eu/operator-kit/contract"
	"github.com/mitstack-eu/operator-kit/handler"
	"github.com/mitstack-eu/operator-kit/kittest"
)

// securityGroup reaches a firewall the manifest no longer declares only
// through the recorded type and stored identity the request carries.
type securityGroup struct{ *kittest.FakeResource }

func handedRecordedType(d contract.Desired) error {
	if d.ResourceType != "aws.security-group" {
		return errors.New("handed no recorded type")
	}
	if d.ResourceIdentity["groupId"] != "sg-0abc" {
		return errors.New("handed no stored identity")
	}
	return nil
}

func (r *securityGroup) Ensure(ctx context.Context, d contract.Desired) error {
	return errors.New("ensured, but a resource the manifest no longer declares is never ensured")
}

func (r *securityGroup) Observe(ctx context.Context, id contract.Identity) (contract.Observation, error) {
	return contract.Observation{}, errors.New("handed the identity alone")
}

func (r *securityGroup) Delete(ctx context.Context, id contract.Identity) error {
	return errors.New("handed the identity alone")
}

func (r *securityGroup) ObserveWithRequest(ctx context.Context, d contract.Desired) (contract.Observation, error) {
	if err := handedRecordedType(d); err != nil {
		return contract.Observation{}, err
	}
	return r.FakeResource.Observe(ctx, d.Identity)
}

func (r *securityGroup) DeleteWithRequest(ctx context.Context, d contract.Desired) error {
	if err := handedRecordedType(d); err != nil {
		return err
	}
	return r.FakeResource.Delete(ctx, d.Identity)
}

// firewall is still standing from before the manifest dropped it.
var firewall = &securityGroup{&kittest.FakeResource{Count: 1, Outputs: map[string]string{"groupId": "sg-0abc"}}}

func TestDeclaredResources(t *testing.T) {
	manifest, err := os.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	network := &kittest.FakeResource{Outputs: map[string]string{"vpcId": "vpc-0abc"}}
	kittest.AssertDeclaredResources(t, kittest.DeclaredResources{
		Manifest:  manifest,
		Resources: map[string]handler.DeclaredResource{"network": network},
		Types:     map[string]handler.DeclaredResource{"aws.security-group": firewall},
		Desired: contract.Desired{
			Identity: contract.Identity{SubscriptionID: "sub-fixture", BlockID: "blockverify-dropped-fixture", OrgID: "org-fixture"},
		},
		Resource:         "firewall",
		ResourceType:     "aws.security-group",
		ResourceIdentity: map[string]string{"groupId": "sg-0abc"},
	})
	if firewall.Count != 0 {
		t.Errorf("resource %q: still standing after the check, want its type handler to have deleted it", "firewall")
	}
}
