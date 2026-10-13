package observe

import (
	"context"
	"strings"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

type EC2InstanceDescription struct {
	InstanceID string
	State      string
	PublicIP   string
	Region     string
	Tags       map[string]string
}

// EC2API describes one instance; found is false when EC2 holds none of that id.
type EC2API interface {
	DescribeInstance(ctx context.Context, d contract.Desired, instanceID string) (desc EC2InstanceDescription, found bool, err error)
}

// EC2Instance observes an aws.ec2-instance by its instanceId: running is
// present, terminated or missing absent, any other state unknown.
type EC2Instance struct {
	API EC2API
}

func (o EC2Instance) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	return observeInstance(ctx, o.API, r, func(desc EC2InstanceDescription) map[string]string {
		outputs := map[string]string{"hostId": desc.InstanceID, "execKind": "ssm", "execTarget": desc.InstanceID}
		if desc.PublicIP != "" {
			outputs["publicIp"] = desc.PublicIP
		}
		if desc.Region != "" {
			outputs["cloud.region"] = desc.Region
		}
		return outputs
	})
}

// EgressProxy observes an aws.egress-proxy instance as EC2Instance does,
// with no outputs.
type EgressProxy struct {
	API EC2API
}

func (o EgressProxy) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	return observeInstance(ctx, o.API, r, func(EC2InstanceDescription) map[string]string { return nil })
}

func observeInstance(ctx context.Context, api EC2API, r contract.ResourceRef, outputs func(EC2InstanceDescription) map[string]string) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "instanceId")
	if !ok {
		return obs, nil
	}
	desc, found, err := api.DescribeInstance(ctx, r.Desired, ids[0])
	switch {
	case err != nil:
		return contract.Observation{}, err
	case !found || desc.State == "terminated":
		return absent(), nil
	case desc.State == "running":
		return present(outputs(desc)), nil
	}
	return unknown("resource %q: instance %s is %s", r.Name, ids[0], desc.State), nil
}

type S3BucketAPI interface {
	BucketExists(ctx context.Context, d contract.Desired, bucket string) (bool, error)
}

// S3Bucket observes an aws.s3-bucket by its bucket.
type S3Bucket struct {
	API S3BucketAPI
}

func (o S3Bucket) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "bucket")
	if !ok {
		return obs, nil
	}
	exists, err := o.API.BucketExists(ctx, r.Desired, ids[0])
	return existence(exists, err, nil)
}

type VPCAPI interface {
	VPCExists(ctx context.Context, d contract.Desired, vpcID string) (bool, error)
}

// VPC observes an aws.vpc by its vpcId.
type VPC struct {
	API VPCAPI
}

func (o VPC) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "vpcId")
	if !ok {
		return obs, nil
	}
	exists, err := o.API.VPCExists(ctx, r.Desired, ids[0])
	return existence(exists, err, nil)
}

type SecurityGroupAPI interface {
	SecurityGroupExists(ctx context.Context, d contract.Desired, groupID string) (bool, error)
}

// SecurityGroup observes an aws.security-group by its groupId.
type SecurityGroup struct {
	API SecurityGroupAPI
}

func (o SecurityGroup) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "groupId")
	if !ok {
		return obs, nil
	}
	exists, err := o.API.SecurityGroupExists(ctx, r.Desired, ids[0])
	return existence(exists, err, nil)
}

type SSMAssociationAPI interface {
	AssociationExists(ctx context.Context, d contract.Desired, associationID string) (bool, error)
}

// SSMAssociation observes an aws.ssm-association by its associationId.
type SSMAssociation struct {
	API SSMAssociationAPI
}

func (o SSMAssociation) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "associationId")
	if !ok {
		return obs, nil
	}
	exists, err := o.API.AssociationExists(ctx, r.Desired, ids[0])
	return existence(exists, err, nil)
}

type EBSVolumeDescription struct {
	VolumeID string
	State    string
}

type EBSVolumeAPI interface {
	DescribeVolume(ctx context.Context, d contract.Desired, volumeID string) (desc EBSVolumeDescription, found bool, err error)
}

// EBSVolume observes an aws.ebs-volume by its volumeId: available or in-use
// is present, deleted or missing absent, any other state unknown.
type EBSVolume struct {
	API EBSVolumeAPI
}

func (o EBSVolume) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "volumeId")
	if !ok {
		return obs, nil
	}
	desc, found, err := o.API.DescribeVolume(ctx, r.Desired, ids[0])
	switch {
	case err != nil:
		return contract.Observation{}, err
	case !found || desc.State == "deleted":
		return absent(), nil
	case desc.State == "available" || desc.State == "in-use":
		return present(nil), nil
	}
	return unknown("resource %q: volume %s is %s", r.Name, ids[0], desc.State), nil
}

type ECRRepositoryDescription struct {
	Name string
	URI  string
}

type ECRRepositoryAPI interface {
	DescribeRepository(ctx context.Context, d contract.Desired, name string) (desc ECRRepositoryDescription, found bool, err error)
}

// ECRRepository observes an aws.ecr-repository by its repositoryName,
// reporting its URI's host as registryHost.
type ECRRepository struct {
	API ECRRepositoryAPI
}

func (o ECRRepository) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "repositoryName")
	if !ok {
		return obs, nil
	}
	desc, found, err := o.API.DescribeRepository(ctx, r.Desired, ids[0])
	if err != nil || !found {
		return existence(found, err, nil)
	}
	host, _, _ := strings.Cut(desc.URI, "/")
	return present(map[string]string{"registryHost": host}), nil
}

type IAMRoleDescription struct {
	Name string
	ARN  string
}

type IAMRoleAPI interface {
	DescribeRole(ctx context.Context, d contract.Desired, name string) (desc IAMRoleDescription, found bool, err error)
}

// IAMRole observes an aws.iam-role by its roleName. The type marks roleArn
// secret, so it is a secret output.
type IAMRole struct {
	API IAMRoleAPI
}

func (o IAMRole) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "roleName")
	if !ok {
		return obs, nil
	}
	desc, found, err := o.API.DescribeRole(ctx, r.Desired, ids[0])
	if err != nil || !found {
		return existence(found, err, nil)
	}
	return contract.Observation{State: contract.Present, SecretOutputs: map[string]string{"roleArn": desc.ARN}}, nil
}

type Route53RecordDescription struct {
	Name   string
	Type   string
	Values []string
}

type Route53API interface {
	DescribeRecord(ctx context.Context, d contract.Desired, zoneID, name string) (desc Route53RecordDescription, found bool, err error)
}

// Route53Record observes an aws.route53-record by its hostedZoneId and
// recordName, reporting appHostname and its https appUrl.
type Route53Record struct {
	API Route53API
}

func (o Route53Record) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "hostedZoneId", "recordName")
	if !ok {
		return obs, nil
	}
	desc, found, err := o.API.DescribeRecord(ctx, r.Desired, ids[0], ids[1])
	if err != nil || !found {
		return existence(found, err, nil)
	}
	host := strings.TrimSuffix(desc.Name, ".")
	return present(map[string]string{"appHostname": host, "appUrl": "https://" + host}), nil
}
