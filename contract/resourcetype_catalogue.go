package contract

import (
	_ "embed"
	"encoding/json"
	"time"
)

// providerInputsJSON is the output of cmd/catalogue-inputs, regenerated with
//
//	go run ./cmd/catalogue-inputs -schema cmd/catalogue-inputs/testdata/provider-schema-extract.json > contract/provider-inputs.json
//
//go:embed provider-inputs.json
var providerInputsJSON []byte

// withProviderInputs gives a mapped type the file does not cover an empty list, which the catalogue tests refuse.
func withProviderInputs(ts []ResourceType) []ResourceType {
	var generated map[string][]Input
	if err := json.Unmarshal(providerInputsJSON, &generated); err != nil {
		panic("contract: embedded provider-inputs.json is not valid: " + err.Error())
	}
	for i := range ts {
		if ts[i].ProviderResource == nil {
			continue
		}
		ts[i].Inputs = append([]Input{}, generated[ts[i].ID]...)
	}
	return ts
}

const (
	awsProvider     = "registry.opentofu.org/hashicorp/aws"
	githubProvider  = "registry.opentofu.org/integrations/github"
	hetznerProvider = "registry.opentofu.org/hetznercloud/hcloud"
)

// none is shared by every entry, so clone must copy it.
var none = []string{}

func timeBudgets(ensure, observe, delete time.Duration) map[string]time.Duration {
	return map[string]time.Duration{"ensure": ensure, "observe": observe, "delete": delete}
}

func transient(cause string) Failure { return Failure{Class: "transient", Cause: cause} }
func permanent(cause string) Failure { return Failure{Class: "permanent", Cause: cause} }

var (
	awsThrottled   = transient("the AWS API throttled the call or answered a server error")
	awsDenied      = permanent("the connected AWS role lacks permission for the call")
	githubLimited  = transient("the GitHub API rate-limited the call or answered a server error")
	githubDenied   = permanent("the GitHub App installation lacks the permission the call needs")
	hetznerLimited = transient("the Hetzner Cloud API rate-limited the call or answered a server error")
	hetznerDenied  = permanent("the project's Hetzner Cloud API token is invalid or lacks write access")
	hostDown       = transient("the host is not reachable through its exec mechanism")
)

// Each mapped type's ReplaceOn lists the arguments its provider schema forces a new resource on, and the region argument for every regional AWS resource.
var resourceTypeCatalogue = withProviderInputs([]ResourceType{
	{
		ID: "aws.ebs-volume", Kind: "block-volume", ReliesOn: none,
		Category: "storage", Languages: none, Lifecycle: "stateful", Handover: "snapshot", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: awsProvider, Resource: "aws_ebs_volume",
			ReplaceOn: []string{"availability_zone", "encrypted", "kms_key_id", "multi_attach_enabled", "outpost_arn", "region", "snapshot_id"}},
		Outputs:     []string{"dataSnapshotId"},
		Observe:     "DescribeVolumes returns the volume in state available or in-use",
		Absence:     "DescribeVolumes answers InvalidVolume.NotFound for the volume id",
		TimeBudgets: timeBudgets(5*time.Minute, 30*time.Second, 5*time.Minute),
		Failures: []Failure{
			awsThrottled, awsDenied,
			transient("the volume is still creating, attaching or detaching"),
			permanent("the account's EBS storage quota for the volume type is reached"),
			permanent("the KMS key the volume is encrypted with is disabled or not granted to the role"),
		},
	},
	{
		ID: "aws.ec2-instance", Kind: "host", ReliesOn: none,
		Category: "compute", Languages: []string{"any-container"}, Lifecycle: "replaceable", Upgrade: "recreate", Scope: "per-app",
		Priced: true, PricingKey: "AmazonEC2",
		Inputs: []Input{
			{Name: "instanceType", Accepts: "value", Replacement: "update"},
			{Name: "namePrefix", Required: true, Accepts: "value", Replacement: "replace"},
			{Name: "securityGroup", Required: true, Accepts: "resource:firewall", Place: "own", Replacement: "update"},
		},
		Outputs:     []string{"cloud.region", "execKind", "execTarget", "host.type", "hostId", "publicIp"},
		Observe:     "DescribeInstances returns the instance in state running, and SSM reports its agent online",
		Absence:     "DescribeInstances returns the instance in state terminated, or no longer returns it, and its Elastic IP, instance role and instance profile are gone",
		TimeBudgets: timeBudgets(10*time.Minute, time.Minute, 10*time.Minute),
		Failures: []Failure{
			awsThrottled, awsDenied,
			transient("the availability zone has no capacity for the instance type"),
			permanent("the account's vCPU quota for the instance family is reached"),
		},
	},
	{
		ID: "aws.ecr-image", Kind: "container-image", ReliesOn: none,
		Category: "artifact-registry", Languages: []string{"any-container"}, ArtifactType: "container-image",
		Lifecycle: "replaceable", Upgrade: "recreate", Scope: "per-app",
		Inputs: []Input{
			{Name: "repository", Required: true, Accepts: "output:container-repository/registryHost", Place: "own", Replacement: "replace"},
			{Name: "tag", Required: true, Accepts: "value", Replacement: "replace"},
		},
		Outputs:     []string{"imageRepo"},
		Observe:     "DescribeImages finds the tag in the repository",
		Absence:     "DescribeImages answers ImageNotFoundException for the tag",
		TimeBudgets: timeBudgets(2*time.Minute, 30*time.Second, 2*time.Minute),
		Failures: []Failure{
			awsThrottled, awsDenied,
			permanent("the repository the image is pushed to does not exist"),
		},
	},
	{
		ID: "aws.ecr-repository", Kind: "container-repository", ReliesOn: none,
		Category: "artifact-registry", Languages: []string{"any-container"}, ArtifactType: "container-image",
		Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: awsProvider, Resource: "aws_ecr_repository",
			ReplaceOn: []string{"encryption_configuration", "name", "region"}},
		Outputs:     []string{"registryHost"},
		Observe:     "DescribeRepositories finds the repository by name",
		Absence:     "DescribeRepositories answers RepositoryNotFoundException for the name",
		TimeBudgets: timeBudgets(2*time.Minute, 30*time.Second, 2*time.Minute),
		Failures: []Failure{
			awsThrottled, awsDenied,
			permanent("the repository name is taken in the account and region"),
			permanent("delete is refused because the repository still holds images and force_delete is off"),
		},
	},
	{
		ID: "aws.egress-proxy", Kind: "egress-proxy", ReliesOn: none,
		Category: "network", Languages: none, Lifecycle: "replaceable", Upgrade: "recreate", Scope: "per-app",
		Priced: true, PricingKey: "AmazonEC2",
		Inputs: []Input{
			{Name: "network", Required: true, Accepts: "resource:network", Place: "own", Replacement: "replace"},
		},
		Outputs:     none,
		Observe:     "DescribeInstances returns the proxy instance running with its public address, and the proxy answers on its port",
		Absence:     "DescribeInstances returns the proxy instance terminated, and its security group, role and instance profile answer not found",
		TimeBudgets: timeBudgets(10*time.Minute, time.Minute, 10*time.Minute),
		Failures: []Failure{
			awsThrottled, awsDenied,
			transient("the availability zone has no capacity for the proxy's instance type"),
			permanent("the account's vCPU quota for the instance family is reached"),
		},
	},
	{
		ID: "aws.iam-oidc-provider", Kind: "oidc-provider", ReliesOn: none,
		Category: "identity-and-access", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "org-wide",
		ProviderResource: &ProviderResource{Provider: awsProvider, Resource: "aws_iam_openid_connect_provider",
			ReplaceOn: []string{"url"}},
		Outputs:     []string{"oidcProviderArn"},
		Observe:     "GetOpenIDConnectProvider returns the provider with its client ids and thumbprints",
		Absence:     "GetOpenIDConnectProvider answers NoSuchEntity for the provider ARN",
		TimeBudgets: timeBudgets(time.Minute, 30*time.Second, time.Minute),
		Failures: []Failure{
			awsThrottled, awsDenied,
			permanent("a provider for the issuer URL already exists in the account"),
			permanent("the account's OIDC provider quota is reached"),
		},
	},
	{
		ID: "aws.iam-role", Kind: "iam-role", ReliesOn: none,
		Category: "identity-and-access", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: awsProvider, Resource: "aws_iam_role",
			ReplaceOn: []string{"name", "name_prefix", "path"}},
		Outputs:     []string{"roleArn"},
		Observe:     "GetRole returns the role with its trust policy",
		Absence:     "GetRole answers NoSuchEntity for the role name",
		TimeBudgets: timeBudgets(2*time.Minute, 30*time.Second, 2*time.Minute),
		Failures: []Failure{
			awsThrottled, awsDenied,
			transient("the new role is not yet visible to every AWS service"),
			permanent("the role name is taken in the account"),
			permanent("the trust policy is malformed or names a principal that does not exist"),
		},
	},
	{
		ID: "aws.route53-record", Kind: "app-address", ReliesOn: none,
		Category: "network", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: awsProvider, Resource: "aws_route53_record",
			ReplaceOn: []string{"name", "zone_id"}},
		Outputs:     []string{"appHostname", "appUrl"},
		Observe:     "ListResourceRecordSets returns the record, and GetChange reports its change INSYNC",
		Absence:     "ListResourceRecordSets no longer returns a record of the name and type",
		TimeBudgets: timeBudgets(5*time.Minute, 30*time.Second, 5*time.Minute),
		Failures: []Failure{
			awsThrottled, awsDenied,
			transient("Route 53 answered PriorRequestNotComplete for the zone"),
			permanent("the hosted zone does not exist or is not reachable with the credentials"),
			permanent("a record of the same name and type exists and allow_overwrite is off"),
		},
	},
	{
		ID: "aws.security-group", Kind: "firewall", ReliesOn: none,
		Category: "network", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: awsProvider, Resource: "aws_security_group",
			ReplaceOn: []string{"description", "name", "name_prefix", "region", "vpc_id"}},
		Outputs:     none,
		Observe:     "DescribeSecurityGroups returns the group in its VPC with its rules",
		Absence:     "DescribeSecurityGroups answers InvalidGroup.NotFound for the group id",
		TimeBudgets: timeBudgets(2*time.Minute, 30*time.Second, 15*time.Minute),
		Failures: []Failure{
			awsThrottled, awsDenied,
			transient("delete is refused while a network interface still uses the group"),
			permanent("the group name is taken in the VPC"),
			permanent("the VPC's security group quota is reached"),
		},
	},
	{
		ID: "aws.ssm-association", Kind: "host-configuration", ReliesOn: none,
		Category: "compute", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: awsProvider, Resource: "aws_ssm_association",
			ReplaceOn: []string{"name", "region"}},
		Outputs:     none,
		Observe:     "DescribeAssociation returns the association, and its overall status is Success",
		Absence:     "DescribeAssociation answers AssociationDoesNotExist for the association id",
		TimeBudgets: timeBudgets(15*time.Minute, time.Minute, 2*time.Minute),
		Failures: []Failure{
			awsThrottled, awsDenied,
			transient("the target instance's SSM agent is not yet online"),
			permanent("the SSM document does not exist or its parameters are invalid"),
			permanent("the document's run on the target exits with a failure"),
		},
	},
	{
		ID: "aws.ssm-parameter", Kind: "secret", ReliesOn: none,
		Category: "secrets", Languages: none, Lifecycle: "stateful", Handover: "copy", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: awsProvider, Resource: "aws_ssm_parameter",
			ReplaceOn: []string{"data_type", "name", "region", "tier"}},
		Outputs:     none,
		Observe:     "GetParameter returns the parameter at its name and version",
		Absence:     "GetParameter answers ParameterNotFound for the name",
		TimeBudgets: timeBudgets(time.Minute, 30*time.Second, time.Minute),
		Failures: []Failure{
			awsThrottled, awsDenied,
			permanent("a parameter of the name exists and overwrite is off"),
			permanent("the value exceeds the size limit of the parameter's tier"),
			permanent("the KMS key that encrypts the SecureString is disabled or not granted to the role"),
		},
	},
	{
		ID: "aws.vllm-instance", Kind: "llm-endpoint", ReliesOn: none,
		Category: "ai-model", Languages: none, Lifecycle: "replaceable", Upgrade: "recreate", Scope: "per-app",
		Priced: true, PricingKey: "AmazonEC2",
		Inputs: []Input{
			{Name: "allowedCidr", Accepts: "value", Replacement: "update"},
			{Name: "instanceType", Accepts: "value", Replacement: "update"},
			{Name: "modelId", Required: true, Accepts: "value", Replacement: "replace"},
		},
		Outputs:     []string{"llmEndpoint", "model"},
		Observe:     "DescribeInstances returns the GPU instance running with its Elastic IP associated, and the vLLM server lists the model at GET /v1/models",
		Absence:     "DescribeInstances returns the instance terminated, and DescribeAddresses and DescribeSecurityGroups answer not found for its Elastic IP and security group",
		TimeBudgets: timeBudgets(30*time.Minute, time.Minute, 10*time.Minute),
		Failures: []Failure{
			awsThrottled, awsDenied,
			transient("the availability zone has no GPU capacity for the instance type"),
			permanent("the account's GPU instance quota is reached"),
			permanent("the vLLM server cannot load the model the model id names"),
		},
	},
	{
		ID: "aws.vpc", Kind: "network", ReliesOn: none,
		Category: "network", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: awsProvider, Resource: "aws_vpc",
			ReplaceOn: []string{"cidr_block", "instance_tenancy", "ipv4_ipam_pool_id", "ipv4_netmask_length", "region"}},
		Outputs:     none,
		Observe:     "DescribeVpcs returns the VPC in state available",
		Absence:     "DescribeVpcs answers InvalidVpcID.NotFound for the VPC id",
		TimeBudgets: timeBudgets(5*time.Minute, 30*time.Second, 5*time.Minute),
		Failures: []Failure{
			awsThrottled, awsDenied,
			transient("delete is refused while a subnet, gateway or network interface still depends on the VPC"),
			permanent("the region's VPC quota is reached"),
			permanent("the CIDR block is invalid or overlaps one the account already routes"),
		},
	},
	{
		ID: "crossplane.reconcile-claim", Kind: "reconcile-claim", ReliesOn: none,
		Category: "build-and-delivery", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		Inputs: []Input{
			{Name: "driftPolicy", Accepts: "value", Replacement: "update"},
			{Name: "resources", Required: true, Accepts: "value", Replacement: "update"},
		},
		Outputs:     []string{"claim", "driftPolicy", "driftedResources", "reconciledResources"},
		Observe:     "the claim exists on the control plane with conditions Synced and Ready both True",
		Absence:     "the control plane's Kubernetes API answers 404 for the claim and for its composite resource",
		TimeBudgets: timeBudgets(15*time.Minute, time.Minute, 15*time.Minute),
		Failures: []Failure{
			transient("the control plane's Kubernetes API is unreachable"),
			transient("a composed resource is not yet ready"),
			permanent("the claim's composite resource definition or composition is not installed on the control plane"),
		},
	},
	{
		ID: "github.actions-secret", Kind: "secret", ReliesOn: none,
		Category: "secrets", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: githubProvider, Resource: "github_actions_secret",
			ReplaceOn: []string{"repository", "secret_name"}},
		Outputs:     none,
		Observe:     "the Actions secrets API returns the secret name on the repository",
		Absence:     "the Actions secrets API answers 404 for the secret name",
		TimeBudgets: timeBudgets(time.Minute, 30*time.Second, time.Minute),
		Failures: []Failure{
			githubLimited, githubDenied,
			permanent("the repository does not exist"),
		},
	},
	{
		ID: "github.actions-variable", Kind: "ci-variable", ReliesOn: none,
		Category: "build-and-delivery", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: githubProvider, Resource: "github_actions_variable",
			ReplaceOn: []string{"repository", "variable_name"}},
		Outputs:     none,
		Observe:     "the Actions variables API returns the variable with its value",
		Absence:     "the Actions variables API answers 404 for the variable name",
		TimeBudgets: timeBudgets(time.Minute, 30*time.Second, time.Minute),
		Failures: []Failure{
			githubLimited, githubDenied,
			permanent("the repository does not exist"),
		},
	},
	{
		ID: "github.commit", Kind: "source-revision", ReliesOn: none,
		Category: "source-and-work", Languages: none, Lifecycle: "replaceable", Upgrade: "recreate", Scope: "per-app",
		Inputs: []Input{
			{Name: "branch", Required: true, Accepts: "value", Replacement: "replace"},
			{Name: "files", Required: true, Accepts: "value", Replacement: "replace"},
			{Name: "message", Required: true, Accepts: "value", Replacement: "replace"},
			{Name: "repository", Required: true, Accepts: "output:git-repository/repo", Place: "own", Replacement: "replace"},
		},
		Outputs:     []string{"sourceBranch", "sourceSha"},
		Observe:     "the git commits API returns the commit, and the branch ref points at it or at a descendant",
		Absence:     "the git refs API answers 404 for the branch that carried the commit",
		TimeBudgets: timeBudgets(2*time.Minute, 30*time.Second, time.Minute),
		Failures: []Failure{
			githubLimited, githubDenied,
			transient("the branch ref moved while the commit was pushed"),
			permanent("the branch is protected against direct pushes"),
		},
	},
	{
		ID: "github.container-image", Kind: "container-image", ReliesOn: none,
		Category: "artifact-registry", Languages: []string{"any-container"}, ArtifactType: "container-image",
		Lifecycle: "replaceable", Upgrade: "recreate", Scope: "per-app",
		Inputs: []Input{
			{Name: "repository", Required: true, Accepts: "output:git-repository/repo", Place: "own", Replacement: "replace"},
			{Name: "tag", Required: true, Accepts: "value", Replacement: "replace"},
		},
		Outputs:     []string{"imageRepo"},
		Observe:     "the packages API returns the container package version carrying the tag",
		Absence:     "the packages API answers 404 for the package version",
		TimeBudgets: timeBudgets(2*time.Minute, 30*time.Second, 2*time.Minute),
		Failures: []Failure{
			githubLimited, githubDenied,
			permanent("the package holds no version carrying the tag"),
		},
	},
	{
		ID: "github.pull-request", Kind: "change-request", ReliesOn: none,
		Category: "source-and-work", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: githubProvider, Resource: "github_repository_pull_request",
			ReplaceOn: []string{"base_repository", "head_ref", "owner"}},
		Outputs:     []string{"pullRequestUrl"},
		Observe:     "the pulls API returns the pull request open from its head to its base",
		Absence:     "the pulls API returns the pull request closed",
		TimeBudgets: timeBudgets(time.Minute, 30*time.Second, time.Minute),
		Failures: []Failure{
			githubLimited, githubDenied,
			permanent("the head branch has no commit ahead of the base"),
			permanent("a pull request from the head to the base is already open"),
		},
	},
	{
		ID: "github.repository", Kind: "git-repository", ReliesOn: none,
		Category: "source-and-work", Languages: none, Lifecycle: "stateful", Handover: "copy", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: githubProvider, Resource: "github_repository",
			ReplaceOn: []string{"fork", "source_owner", "source_repo"}},
		Outputs:     []string{"repo"},
		Observe:     "the repos API returns the repository with its visibility and settings",
		Absence:     "the repos API answers 404 for the repository, or returns it archived when archive_on_destroy is set",
		TimeBudgets: timeBudgets(2*time.Minute, 30*time.Second, time.Minute),
		Failures: []Failure{
			githubLimited, githubDenied,
			permanent("the repository name is taken in the organisation"),
		},
	},
	{
		ID: "github.repository-webhook", Kind: "webhook", ReliesOn: none,
		Category: "build-and-delivery", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: githubProvider, Resource: "github_repository_webhook",
			ReplaceOn: []string{"repository"}},
		Outputs:     none,
		Observe:     "the hooks API returns the webhook with its URL and events",
		Absence:     "the hooks API answers 404 for the hook id",
		TimeBudgets: timeBudgets(time.Minute, 30*time.Second, time.Minute),
		Failures: []Failure{
			githubLimited, githubDenied,
			permanent("the webhook configuration's URL is not a valid HTTPS address"),
		},
	},
	{
		ID: "github.runner-group", Kind: "runner-group", ReliesOn: none,
		Category: "build-and-delivery", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: githubProvider, Resource: "github_actions_runner_group",
			ReplaceOn: []string{}},
		Outputs:     none,
		Observe:     "the Actions runner groups API returns the group in the organisation",
		Absence:     "the Actions runner groups API answers 404 for the group id",
		TimeBudgets: timeBudgets(time.Minute, 30*time.Second, time.Minute),
		Failures: []Failure{
			githubLimited, githubDenied,
			permanent("the organisation's plan does not allow runner groups"),
		},
	},
	{
		ID: "github.scaffold-contribution", Kind: "scaffold-contribution", ReliesOn: none,
		Category: "source-and-work", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		Inputs: []Input{
			{Name: "config", Accepts: "value", Replacement: "update"},
			{Name: "env", Accepts: "value", Replacement: "update"},
			{Name: "files", Required: true, Accepts: "value", Replacement: "update"},
		},
		Outputs:     []string{"scaffoldContribution"},
		Observe:     "the contents API returns every file the contribution names on the repository's default branch with the contributed content",
		Absence:     "the contents API answers 404 for every file the contribution added",
		TimeBudgets: timeBudgets(2*time.Minute, 30*time.Second, 2*time.Minute),
		Failures: []Failure{
			githubLimited, githubDenied,
			permanent("two contributions write the same file path"),
		},
	},
	{
		ID: "github.self-hosted-runner", Kind: "ci-runner", ReliesOn: none,
		Category: "build-and-delivery", Languages: none, Lifecycle: "replaceable", Upgrade: "recreate", Scope: "per-app",
		Inputs: []Input{
			// capability:aws is the narrowest place the evidence supports: the only creator installs the runner on an AWS EC2 box.
			{Name: "host", Required: true, Accepts: "resource:host", Place: "capability:aws", Replacement: "replace"},
			{Name: "label", Required: true, Accepts: "value", Replacement: "replace"},
			{Name: "repository", Required: true, Accepts: "output:git-repository/repo", Place: "own", Replacement: "replace"},
		},
		Outputs:     []string{"awsCredential", "runnerLabel", "runsOn"},
		Observe:     "the Actions runners API lists the runner online with its label",
		Absence:     "the Actions runners API no longer lists the runner",
		TimeBudgets: timeBudgets(10*time.Minute, 30*time.Second, 5*time.Minute),
		Failures: []Failure{
			githubLimited, githubDenied, hostDown,
			transient("the registration token expired before the runner registered"),
		},
	},
	{
		ID: "github.workflow-file", Kind: "ci-workflow", ReliesOn: none,
		Category: "build-and-delivery", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: githubProvider, Resource: "github_repository_file",
			ReplaceOn: []string{"branch", "file", "repository"}},
		Outputs:     []string{"ciCheckContribution", "workflowPath", "workflowUrl"},
		Observe:     "the contents API returns the file at its path on the branch with the committed content",
		Absence:     "the contents API answers 404 for the path on the branch",
		TimeBudgets: timeBudgets(time.Minute, 30*time.Second, time.Minute),
		Failures: []Failure{
			githubLimited, githubDenied,
			transient("the file changed on the branch after it was read"),
			permanent("the branch is protected against direct commits"),
		},
	},
	{
		ID: "hetzner.firewall", Kind: "firewall", ReliesOn: none,
		Category: "network", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: hetznerProvider, Resource: "hcloud_firewall",
			ReplaceOn: []string{}},
		Outputs:     none,
		Observe:     "GET /firewalls/{id} returns the firewall with its rules and the resources it applies to",
		Absence:     "GET /firewalls/{id} answers 404",
		TimeBudgets: timeBudgets(2*time.Minute, 30*time.Second, 5*time.Minute),
		Failures: []Failure{
			hetznerLimited, hetznerDenied,
			transient("delete is refused while the firewall is still applied to a resource"),
			permanent("a firewall of the name exists in the project"),
		},
	},
	{
		ID: "hetzner.k3s-cluster", Kind: "kubernetes-cluster", ReliesOn: none, Hosts: "kubernetes",
		Category: "compute", Languages: none, Lifecycle: "replaceable", Upgrade: "recreate", Scope: "org-wide",
		Priced: true, PricingKey: "HetznerCloud",
		Inputs: []Input{
			{Name: "agentNodes", Accepts: "value", Replacement: "update"},
			{Name: "clusterRepository", Required: true, Accepts: "output:git-repository/repo", Place: "capability:github", Replacement: "update"},
			{Name: "location", Required: true, Accepts: "value", Replacement: "replace"},
			{Name: "nodeDisk", Accepts: "value", Replacement: "replace"},
			{Name: "serverType", Required: true, Accepts: "value", Replacement: "update"},
		},
		Outputs:     []string{"apiEndpoint", "caCertificate", "clusterCredential", "cloud.region", "issuerUrl", "privateEndpoint"},
		Observe:     "the Kubernetes API at apiEndpoint answers /readyz with ok to the cluster credential, and every node reports Ready",
		Absence:     "GET /servers/{id} answers 404 for the cluster's server, and its firewall and primary IP answer 404",
		TimeBudgets: timeBudgets(20*time.Minute, time.Minute, 10*time.Minute),
		Failures: []Failure{
			hetznerLimited, hetznerDenied,
			transient("the location has no capacity for the server type"),
			transient("k3s has not yet reported every node Ready"),
			permanent("the project's server limit is reached"),
		},
	},
	{
		ID: "hetzner.primary-ip", Kind: "ip-address", ReliesOn: none,
		Category: "network", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: hetznerProvider, Resource: "hcloud_primary_ip",
			ReplaceOn: []string{"location", "type"}},
		Outputs:     []string{"publicIp"},
		Observe:     "GET /primary_ips/{id} returns the address",
		Absence:     "GET /primary_ips/{id} answers 404",
		TimeBudgets: timeBudgets(time.Minute, 30*time.Second, time.Minute),
		Failures: []Failure{
			hetznerLimited, hetznerDenied,
			permanent("the project's primary IP limit is reached"),
		},
	},
	{
		ID: "hetzner.server", Kind: "host", ReliesOn: none,
		Category: "compute", Languages: []string{"any-container"}, Lifecycle: "replaceable", Upgrade: "recreate", Scope: "per-app",
		Priced: true, PricingKey: "HetznerCloud",
		ProviderResource: &ProviderResource{Provider: hetznerProvider, Resource: "hcloud_server",
			ReplaceOn: []string{"image", "location", "ssh_keys", "user_data"}},
		Outputs:     []string{"cloud.region", "execKind", "execTarget", "host.type", "hostId"},
		Observe:     "GET /servers/{id} returns the server with status running",
		Absence:     "GET /servers/{id} answers 404",
		TimeBudgets: timeBudgets(10*time.Minute, time.Minute, 5*time.Minute),
		Failures: []Failure{
			hetznerLimited, hetznerDenied,
			transient("the location has no capacity for the server type"),
			permanent("the project's server limit is reached"),
			permanent("the image names no image available in the location"),
		},
	},
	{
		ID: "hetzner.ssh-key", Kind: "ssh-key", ReliesOn: none,
		Category: "identity-and-access", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: hetznerProvider, Resource: "hcloud_ssh_key",
			ReplaceOn: []string{"public_key"}},
		Outputs:     none,
		Observe:     "GET /ssh_keys/{id} returns the key with its fingerprint",
		Absence:     "GET /ssh_keys/{id} answers 404",
		TimeBudgets: timeBudgets(time.Minute, 30*time.Second, time.Minute),
		Failures: []Failure{
			hetznerLimited, hetznerDenied,
			permanent("a key with the same fingerprint exists in the project"),
			permanent("the public key is not a valid OpenSSH public key"),
		},
	},
	{
		ID: "hetzner.volume", Kind: "block-volume", ReliesOn: none,
		Category: "storage", Languages: none, Lifecycle: "stateful", Handover: "snapshot", Upgrade: "in-place", Scope: "per-app",
		ProviderResource: &ProviderResource{Provider: hetznerProvider, Resource: "hcloud_volume",
			ReplaceOn: []string{"location"}},
		Outputs:     []string{"dataSnapshotId"},
		Observe:     "GET /volumes/{id} returns the volume with status available",
		Absence:     "GET /volumes/{id} answers 404",
		TimeBudgets: timeBudgets(5*time.Minute, 30*time.Second, 5*time.Minute),
		Failures: []Failure{
			hetznerLimited, hetznerDenied,
			transient("the volume is still attaching to or detaching from a server"),
			permanent("the project's volume limit is reached"),
			permanent("the requested size is below the volume's current size"),
		},
	},
	{
		ID: "host.configuration", Kind: "host-configuration", ReliesOn: none,
		Category: "compute", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		Inputs: []Input{
			{Name: "customDomain", Accepts: "value", Replacement: "update"},
			{Name: "dataVolume", Accepts: "resource:block-volume", Place: "carrier", Replacement: "replace"},
			{Name: "domain", Accepts: "value", Replacement: "update"},
		},
		Outputs:     none,
		Observe:     "the configuration run on the host reports success, and the files and services it installs are in place",
		Absence:     "the files and services the configuration installed are gone from the host",
		TimeBudgets: timeBudgets(15*time.Minute, time.Minute, 5*time.Minute),
		Failures: []Failure{
			hostDown,
			permanent("a configuration command exits non-zero on the host"),
		},
	},
	{
		ID: "host.container", Kind: "container", ReliesOn: none,
		Category: "compute", Languages: []string{"any-container"}, Lifecycle: "replaceable", Upgrade: "recreate", Scope: "per-app",
		Inputs: []Input{
			{Name: "env", Secret: true, Accepts: "value", Replacement: "update"},
			{Name: "image", Required: true, Accepts: "value", Replacement: "update"},
			{Name: "port", Accepts: "value", Replacement: "update"},
		},
		Outputs:     []string{"url"},
		Observe:     "the container runtime on the host reports the container running from its image, and the container answers on its port",
		Absence:     "the container runtime on the host no longer lists the container",
		TimeBudgets: timeBudgets(10*time.Minute, time.Minute, 2*time.Minute),
		Failures: []Failure{
			hostDown,
			transient("pulling the image failed or timed out"),
			permanent("the image names no image its registry holds"),
			permanent("the container exits right after it starts"),
		},
	},
	{
		ID: "host.postgres-database", Kind: "postgres-database", ReliesOn: none,
		Category: "database", Languages: none, Lifecycle: "stateful", Handover: "copy", Upgrade: "in-place", Scope: "per-app",
		Inputs: []Input{
			{Name: "dataVolume", Required: true, Accepts: "resource:block-volume", Place: "carrier", Replacement: "replace"},
			{Name: "dbName", Required: true, Accepts: "value", Replacement: "replace"},
			{Name: "username", Required: true, Accepts: "value", Replacement: "replace"},
		},
		Outputs:     []string{"dbName", "host", "password", "port", "username"},
		Observe:     "a connection as the database user to the database succeeds and SELECT 1 answers",
		Absence:     "pg_database no longer lists the database and pg_roles no longer lists its user",
		TimeBudgets: timeBudgets(10*time.Minute, time.Minute, 5*time.Minute),
		Failures: []Failure{
			hostDown,
			transient("the Postgres server on the host is not yet accepting connections"),
			permanent("the data volume is not attached to the host or not mounted"),
		},
	},
	{
		ID: "kubernetes.namespace", Kind: "kubernetes-namespace", ReliesOn: none,
		Category: "compute", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		Inputs: []Input{
			// capability:hetzner is the narrowest place the evidence supports: hetzner.k3s-cluster is the only kubernetes-cluster type, and it lives in hetzner.
			{Name: "cluster", Required: true, Accepts: "resource:kubernetes-cluster", Place: "capability:hetzner", Replacement: "replace"},
		},
		Outputs:     []string{"namespace", "serviceAccount"},
		Observe:     "the Kubernetes API of the cluster returns the namespace in phase Active and the service account in it",
		Absence:     "the Kubernetes API of the cluster answers 404 for the namespace",
		TimeBudgets: timeBudgets(time.Minute, 30*time.Second, 10*time.Minute),
		Failures: []Failure{
			transient("the cluster's Kubernetes API is unreachable or answers a server error"),
			transient("the namespace is still terminating from an earlier delete"),
			permanent("the cluster credential lacks permission to create namespaces or service accounts"),
		},
	},
	{
		ID: "plain.api-token", Kind: "api-token", ReliesOn: none,
		Category: "identity-and-access", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		Inputs: []Input{
			{Name: "label", Required: true, Accepts: "value", Replacement: "replace"},
		},
		Outputs:     none,
		Observe:     "the Plain API accepts the token on an authenticated read",
		Absence:     "the Plain API answers 401 to the revoked token",
		TimeBudgets: timeBudgets(time.Minute, 30*time.Second, time.Minute),
		Failures: []Failure{
			transient("the Plain API is unreachable or answers a server error"),
			permanent("the connected Plain account lacks permission to issue tokens"),
		},
	},
	{
		ID: "ubicloud.runner", Kind: "ci-runner", ReliesOn: []string{"github"},
		Category: "build-and-delivery", Languages: none, Lifecycle: "replaceable", Upgrade: "in-place", Scope: "per-app",
		Inputs: []Input{
			{Name: "repository", Required: true, Accepts: "output:git-repository/repo", Place: "capability:github", Replacement: "replace"},
			{Name: "runnerSize", Accepts: "value", Replacement: "update"},
		},
		Outputs:     []string{"runsOn"},
		Observe:     "the Ubicloud API lists the GitHub installation that covers the repository",
		Absence:     "the Ubicloud API no longer lists the repository under the installation",
		TimeBudgets: timeBudgets(5*time.Minute, 30*time.Second, 5*time.Minute),
		Failures: []Failure{
			transient("the Ubicloud API is unreachable or answers a server error"),
			permanent("Ubicloud's GitHub App is not installed on the repository's organisation"),
		},
	},
})
