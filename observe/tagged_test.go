package observe_test

// tagged_test.go pins the tag listers blockverify's tagging guard reads:
// each lists everything carrying the subscription's id, as AWS tags it
// (mitstack:subscription, the key aws-app-infra writes) or Hetzner labels it
// (mitstack.dev/subscription), as the type it is. An object the lister
// cannot type is still listed, with no type, so the guard reports it rather
// than missing it.

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/mitstack-eu/operator-kit/contract"
	"github.com/mitstack-eu/operator-kit/observe"
)

var (
	_ observe.TaggedLister = observe.AWSTagged{}
	_ observe.TaggedLister = observe.HetznerLabelled{}
)

type fakeTagging struct {
	arns          []string
	key, value    string
	err           error
	subscriptions []string
}

func (f *fakeTagging) ResourceARNs(_ context.Context, d contract.Desired, tagKey, tagValue string) ([]string, error) {
	f.key, f.value = tagKey, tagValue
	f.subscriptions = append(f.subscriptions, d.SubscriptionID)
	return f.arns, f.err
}

func TestAWSTagged_ListsEachTaggedARNAsItsType(t *testing.T) {
	api := &fakeTagging{arns: []string{
		"arn:aws:ec2:eu-central-1:123456789012:instance/i-0abc",
		"arn:aws:ec2:eu-central-1:123456789012:vpc/vpc-0abc",
		"arn:aws:ec2:eu-central-1:123456789012:security-group/sg-0abc",
		"arn:aws:ec2:eu-central-1:123456789012:volume/vol-0abc",
		"arn:aws:s3:::app-state",
		"arn:aws:iam::123456789012:role/deploy",
		"arn:aws:ecr:eu-central-1:123456789012:repository/app",
		"arn:aws:sqs:eu-central-1:123456789012:stray-queue",
	}}
	d := contract.Desired{Identity: contract.Identity{SubscriptionID: "sub-1"}}
	got, err := observe.AWSTagged{API: api}.ListTagged(context.Background(), d, "sub-1")
	if err != nil {
		t.Fatalf("ListTagged: %v", err)
	}
	want := []observe.Tagged{
		{Type: "aws.ec2-instance", ID: "i-0abc"},
		{Type: "aws.vpc", ID: "vpc-0abc"},
		{Type: "aws.security-group", ID: "sg-0abc"},
		{Type: "aws.ebs-volume", ID: "vol-0abc"},
		{Type: "aws.s3-bucket", ID: "app-state"},
		{Type: "aws.iam-role", ID: "deploy"},
		{Type: "aws.ecr-repository", ID: "app"},
		{Type: "", ID: "arn:aws:sqs:eu-central-1:123456789012:stray-queue"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListTagged =\n%+v\nwant\n%+v", got, want)
	}
	if api.key != "mitstack:subscription" || api.value != "sub-1" {
		t.Errorf("asked for tag %q = %q, want mitstack:subscription = sub-1, the key aws-app-infra tags with", api.key, api.value)
	}
}

func TestAWSTagged_ListsEachPartARNAsItsCatalogueType(t *testing.T) {
	api := &fakeTagging{arns: []string{
		"arn:aws:iam::123456789012:instance-profile/mitstack/mitstack-app-myapp-profile",
		"arn:aws:ec2:eu-central-1:123456789012:elastic-ip/eipalloc-0abc",
		"arn:aws:ec2:eu-central-1:123456789012:security-group-rule/sgr-0abc",
		"arn:aws:dlm:eu-central-1:123456789012:policy/policy-0abc",
		"arn:aws:ssm:eu-central-1:123456789012:document/mitstack-app-myapp-edge-config",
	}}

	got, err := observe.AWSTagged{API: api}.ListTagged(context.Background(), contract.Desired{}, "sub-1")

	if err != nil {
		t.Fatalf("ListTagged: %v", err)
	}
	want := []observe.Tagged{
		{Type: "aws.iam-instance-profile", ID: "mitstack-app-myapp-profile"},
		{Type: "aws.elastic-ip", ID: "eipalloc-0abc"},
		{Type: "aws.security-group-rule", ID: "sgr-0abc"},
		{Type: "aws.dlm-lifecycle-policy", ID: "policy-0abc"},
		{Type: "aws.ssm-document", ID: "mitstack-app-myapp-edge-config"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListTagged(%q) =\n%+v\nwant\n%+v", api.arns, got, want)
	}
}

func TestAWSTagged_ReturnsTheAPIsError(t *testing.T) {
	boom := errors.New("tagging: throttled")
	_, err := observe.AWSTagged{API: &fakeTagging{err: boom}}.ListTagged(context.Background(), contract.Desired{}, "sub-1")
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want the API's error", err)
	}
}

type fakeLabels struct {
	objects  []observe.HetznerObject
	selector string
}

func (f *fakeLabels) ListByLabel(_ context.Context, _ contract.Desired, selector string) ([]observe.HetznerObject, error) {
	f.selector = selector
	return f.objects, nil
}

func TestHetznerLabelled_ListsEachLabelledObjectAsItsType(t *testing.T) {
	api := &fakeLabels{objects: []observe.HetznerObject{
		{Kind: "server", ID: "4711"},
		{Kind: "primary_ip", ID: "99"},
		{Kind: "firewall", ID: "12"},
		{Kind: "volume", ID: "55"},
		{Kind: "ssh_key", ID: "8"},
		{Kind: "network", ID: "77"},
		{Kind: "load_balancer", ID: "31"},
	}}
	got, err := observe.HetznerLabelled{API: api}.ListTagged(context.Background(), contract.Desired{}, "sub-1")
	if err != nil {
		t.Fatalf("ListTagged: %v", err)
	}
	want := []observe.Tagged{
		{Type: "hetzner.server", ID: "4711"},
		{Type: "hetzner.primary-ip", ID: "99"},
		{Type: "hetzner.firewall", ID: "12"},
		{Type: "hetzner.volume", ID: "55"},
		{Type: "hetzner.ssh-key", ID: "8"},
		{Type: "hetzner.network", ID: "77"},
		{Type: "", ID: "load_balancer/31"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListTagged =\n%+v\nwant\n%+v", got, want)
	}
	if want := "mitstack.dev/subscription=sub-1"; api.selector != want {
		t.Errorf("label selector = %q, want %q", api.selector, want)
	}
}
