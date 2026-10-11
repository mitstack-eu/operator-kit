package observe

import (
	"context"
	"path"
	"strings"

	"github.com/mitstack-eu/operator-kit/contract"
)

// AWSSubscriptionTag is the tag key the platform's operator trust boundary requires on every object a subscription creates.
const AWSSubscriptionTag = "mitstack:subscription"

const HetznerSubscriptionLabel = "mitstack.dev/subscription"

// Tagged has an empty Type for an object the lister cannot type, and its ID then names the object in full.
type Tagged struct {
	Type string
	ID   string
}

// TaggedLister lists everything carrying a subscription's id.
type TaggedLister interface {
	ListTagged(ctx context.Context, d contract.Desired, subscriptionID string) ([]Tagged, error)
}

type AWSTaggingAPI interface {
	ResourceARNs(ctx context.Context, d contract.Desired, tagKey, tagValue string) ([]string, error)
}

// AWSTagged lists the AWS resources carrying a subscription's id under AWSSubscriptionTag.
type AWSTagged struct {
	API AWSTaggingAPI
}

// awsResourceTypes maps an ARN's service and resource type to the catalogue type id the platform serves for it.
var awsResourceTypes = map[string]string{
	"ec2/instance":            "aws.ec2-instance",
	"ec2/vpc":                 "aws.vpc",
	"ec2/security-group":      "aws.security-group",
	"ec2/volume":              "aws.ebs-volume",
	"iam/role":                "aws.iam-role",
	"ecr/repository":          "aws.ecr-repository",
	"iam/instance-profile":    "aws.iam-instance-profile",
	"ec2/elastic-ip":          "aws.elastic-ip",
	"ec2/security-group-rule": "aws.security-group-rule",
	"dlm/policy":              "aws.dlm-lifecycle-policy",
	"ssm/document":            "aws.ssm-document",
}

// awsPathedTypes are the IAM types whose ARN carries a path before the name.
var awsPathedTypes = map[string]bool{"aws.iam-role": true, "aws.iam-instance-profile": true}

func (l AWSTagged) ListTagged(ctx context.Context, d contract.Desired, subscriptionID string) ([]Tagged, error) {
	arns, err := l.API.ResourceARNs(ctx, d, AWSSubscriptionTag, subscriptionID)
	if err != nil {
		return nil, err
	}
	out := make([]Tagged, 0, len(arns))
	for _, arn := range arns {
		out = append(out, taggedARN(arn))
	}
	return out, nil
}

// taggedARN types one ARN, arn:<partition>:<service>:<region>:<account>:<resource>.
func taggedARN(arn string) Tagged {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) != 6 || parts[0] != "arn" {
		return Tagged{ID: arn}
	}
	service, resource := parts[2], parts[5]
	if service == "s3" && !strings.Contains(resource, "/") {
		return Tagged{Type: "aws.s3-bucket", ID: resource}
	}
	kind, id, ok := strings.Cut(resource, "/")
	typ := awsResourceTypes[service+"/"+kind]
	if !ok || typ == "" {
		return Tagged{ID: arn}
	}
	if awsPathedTypes[typ] {
		id = path.Base(id)
	}
	return Tagged{Type: typ, ID: id}
}

// HetznerObject carries its Kind as the Hetzner Cloud API names it.
type HetznerObject struct {
	Kind string
	ID   string
}

type HetznerLabelAPI interface {
	ListByLabel(ctx context.Context, d contract.Desired, selector string) ([]HetznerObject, error)
}

type HetznerLabelled struct {
	API HetznerLabelAPI
}

var hetznerTypes = map[string]string{
	"server":     "hetzner.server",
	"primary_ip": "hetzner.primary-ip",
	"firewall":   "hetzner.firewall",
	"volume":     "hetzner.volume",
	"ssh_key":    "hetzner.ssh-key",
	"network":    "hetzner.network",
}

func (l HetznerLabelled) ListTagged(ctx context.Context, d contract.Desired, subscriptionID string) ([]Tagged, error) {
	objects, err := l.API.ListByLabel(ctx, d, HetznerSubscriptionLabel+"="+subscriptionID)
	if err != nil {
		return nil, err
	}
	out := make([]Tagged, 0, len(objects))
	for _, o := range objects {
		if typ, ok := hetznerTypes[o.Kind]; ok {
			out = append(out, Tagged{Type: typ, ID: o.ID})
		} else {
			out = append(out, Tagged{ID: o.Kind + "/" + o.ID})
		}
	}
	return out, nil
}
