package tofuhandler

import (
	"embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mitstack-eu/operator-kit/contract"
)

// Provider is one OpenTofu provider a type's module needs, pinned.
type Provider struct {
	Local, Source, Version string
}

var (
	awsProvider    = Provider{"aws", "hashicorp/aws", "6.64.0"}
	dockerProvider = Provider{"docker", "kreuzwerker/docker", "4.6.0"}
	randomProvider = Provider{"random", "hashicorp/random", "3.7.2"}
)

// Spec is one resource type as data. A generated type names the provider
// resource its catalogue entry maps onto and is rendered from the inputs
// alone. A hand-written type carries its module as HCL.
type Spec struct {
	TypeID    string
	Providers []Provider
	// HCL is a hand-written type's module: its variables, provider
	// configuration other than aws, resources and outputs. Empty for a
	// generated type.
	HCL string
	// Outputs maps each output the type publishes to an expression, for a
	// generated type, where `this` names the one provider resource.
	Outputs map[string]string
	// OutputNames maps a tofu output name to the kind's output name where the
	// two differ, as for host.type, which is no valid tofu identifier.
	OutputNames map[string]string
	// SecretOutputs names the outputs a generated type marks sensitive.
	SecretOutputs []string
	// StateOnly names provider resource types the absence proof skips:
	// random_password, which creates nothing outside the state, and
	// docker_image, which keep_locally leaves cached on the host.
	StateOnly []string
}

//go:embed modules/*.tf
var modules embed.FS

func mustModule(name string) string {
	b, err := modules.ReadFile("modules/" + name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Specs holds every type the spike drives: the resource types ADR 0083's
// worked example declares for aws-app-infra, postgres-db and plain.
var Specs = map[string]Spec{
	// Generated from the catalogue's provider mapping. The only lines
	// written per type are the outputs.
	"aws.vpc":            {TypeID: "aws.vpc", Providers: []Provider{awsProvider}},
	"aws.security-group": {TypeID: "aws.security-group", Providers: []Provider{awsProvider}},
	"aws.ebs-volume": {TypeID: "aws.ebs-volume", Providers: []Provider{awsProvider},
		Outputs: map[string]string{"dataSnapshotId": "${aws_ebs_volume.this.snapshot_id}"}},
	"aws.ecr-repository": {TypeID: "aws.ecr-repository", Providers: []Provider{awsProvider},
		Outputs: map[string]string{"registryHost": `${split("/", aws_ecr_repository.this.repository_url)[0]}`}},
	"aws.iam-role": {TypeID: "aws.iam-role", Providers: []Provider{awsProvider},
		Outputs: map[string]string{"roleArn": "${aws_iam_role.this.arn}"}, SecretOutputs: []string{"roleArn"}},
	"aws.ssm-association": {TypeID: "aws.ssm-association", Providers: []Provider{awsProvider}},
	"aws.route53-record": {TypeID: "aws.route53-record", Providers: []Provider{awsProvider},
		Outputs: map[string]string{"appHostname": "${aws_route53_record.this.fqdn}", "appUrl": "https://${aws_route53_record.this.fqdn}"}},

	// Composite types, written by hand under either approach, as ADR 0083
	// says a composite type must be.
	"aws.ec2-instance": {TypeID: "aws.ec2-instance", Providers: []Provider{awsProvider},
		HCL: mustModule("aws-ec2-instance.tf"), OutputNames: map[string]string{"host_type": "host.type", "cloud_region": "cloud.region"}},
	"aws.egress-proxy": {TypeID: "aws.egress-proxy", Providers: []Provider{awsProvider},
		HCL: mustModule("aws-egress-proxy.tf")},

	// Types that live on a host. No provider resource maps onto them, so
	// each is a hand-written module over the docker provider.
	"host.container": {TypeID: "host.container", Providers: []Provider{dockerProvider},
		HCL: mustModule("host-container.tf"), StateOnly: []string{"docker_image"}},
	"host.postgres-database": {TypeID: "host.postgres-database", Providers: []Provider{dockerProvider, randomProvider},
		HCL: mustModule("host-postgres-database.tf"), StateOnly: []string{"random_password", "docker_image"}},
}

// catalogueType returns the kit catalogue's entry for id.
func catalogueType(id string) (contract.ResourceType, bool) {
	for _, rt := range contract.ResourceTypes() {
		if rt.ID == id {
			return rt, true
		}
	}
	return contract.ResourceType{}, false
}

// rendered is the files one resource's working directory holds.
type rendered struct {
	mainJSON []byte // terraform, provider and, for a generated type, the resource
	hcl      string // a hand-written type's module
	tfvars   []byte // the variable values
}

// render builds the root module for spec with in as its inputs. A generated
// type writes each non-secret input literally into the resource and passes
// each secret one through a sensitive variable. A hand-written type passes
// every input as a variable its HCL declares.
func render(spec Spec, in map[string]any, region string, ci *contract.CloudIdentity, offline bool) (rendered, error) {
	req := map[string]any{}
	for _, p := range spec.Providers {
		req[p.Local] = map[string]any{"source": p.Source, "version": p.Version}
	}
	root := map[string]any{"terraform": map[string]any{"required_providers": req}}
	if slices.ContainsFunc(spec.Providers, func(p Provider) bool { return p.Local == "aws" }) {
		root["provider"] = map[string]any{"aws": awsProviderConfig(region, ci, offline)}
	}

	vars := map[string]any{}
	if spec.HCL == "" {
		rt, ok := catalogueType(spec.TypeID)
		if !ok || rt.ProviderResource == nil {
			return rendered{}, fmt.Errorf("%s has no provider mapping in the catalogue, so it cannot be generated", spec.TypeID)
		}
		known := map[string]contract.Input{}
		for _, i := range rt.Inputs {
			known[i.Name] = i
		}
		body := map[string]any{}
		variables := map[string]any{}
		for name, v := range in {
			def, ok := known[name]
			if !ok {
				return rendered{}, fmt.Errorf("%s: input %q is not an input of %s", spec.TypeID, name, rt.ProviderResource.Resource)
			}
			if def.Secret {
				variables[name] = map[string]any{"sensitive": true}
				body[name] = "${var." + name + "}"
				vars[name] = v
				continue
			}
			body[name] = v
		}
		for _, i := range rt.Inputs {
			if i.Required {
				if _, ok := in[i.Name]; !ok {
					return rendered{}, fmt.Errorf("%s: required input %q is missing", spec.TypeID, i.Name)
				}
			}
		}
		root["resource"] = map[string]any{rt.ProviderResource.Resource: map[string]any{"this": body}}
		if len(variables) > 0 {
			root["variable"] = variables
		}
		outputs := map[string]any{}
		for name, expr := range spec.Outputs {
			outputs[name] = map[string]any{"value": expr, "sensitive": slices.Contains(spec.SecretOutputs, name)}
		}
		if len(outputs) > 0 {
			root["output"] = outputs
		}
	} else {
		for k, v := range in {
			vars[k] = v
		}
	}

	mainJSON, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return rendered{}, err
	}
	tfvars, err := json.MarshalIndent(vars, "", "  ")
	if err != nil {
		return rendered{}, err
	}
	return rendered{mainJSON: mainJSON, hcl: spec.HCL, tfvars: tfvars}, nil
}

// awsProviderConfig federates into the consumer's account with the
// request's web identity. Offline, it points every endpoint the spike's types
// use at a closed local port and skips every account lookup, so a plan makes
// no AWS call.
func awsProviderConfig(region string, ci *contract.CloudIdentity, offline bool) map[string]any {
	cfg := map[string]any{"region": region}
	switch {
	case offline:
		dead := "http://127.0.0.1:9"
		cfg["access_key"] = "offline"
		cfg["secret_key"] = "offline"
		cfg["skip_credentials_validation"] = true
		cfg["skip_requesting_account_id"] = true
		cfg["skip_metadata_api_check"] = true
		cfg["skip_region_validation"] = true
		cfg["endpoints"] = []any{map[string]any{"ec2": dead, "iam": dead, "ecr": dead, "ssm": dead, "route53": dead, "sts": dead, "s3": dead}}
	case ci != nil:
		cfg["assume_role_with_web_identity"] = []any{map[string]any{
			"role_arn": ci.RoleARN, "web_identity_token": ci.WebIdentityToken, "session_name": "mitstack-tofuhandler",
		}}
	}
	return cfg
}

// DockerHost turns a host's exec kind and target into the address the docker
// provider reaches its daemon at. The provider speaks to a Unix socket, TCP
// or SSH, so an SSM-only host, which offers none of them, is refused.
func DockerHost(execKind, execTarget string) (string, error) {
	switch execKind {
	case "docker-local":
		if execTarget == "" {
			return "unix:///var/run/docker.sock", nil
		}
		return execTarget, nil
	case "ssh":
		return "ssh://" + execTarget, nil
	case "aws-ssm":
		return "", fmt.Errorf("the docker provider reaches a daemon over a Unix socket, TCP or SSH, and the host %s offers only SSM", execTarget)
	}
	return "", fmt.Errorf("exec kind %q is not one the docker provider can reach", execKind)
}

// kindOutput maps a tofu output name to the kind's output name.
func (s Spec) kindOutput(name string) string {
	if k, ok := s.OutputNames[name]; ok {
		return k
	}
	return name
}

func (s Spec) stateOnly(resourceType string) bool {
	return slices.Contains(s.StateOnly, resourceType) || strings.HasPrefix(resourceType, "terraform_data")
}
