// Command replacement-probe derives which inputs of a provider-mapped catalogue
// type force its resource to be replaced, from the provider's own plan rather
// than from the schema, which carries no replacement rule.
//
// Usage:
//
//	replacement-probe -tofu <bin> -schema <schema.json> -work <dir> [-plugin-cache <dir>] <type-id>...
//
// For each top-level attribute input of each type it writes one probe
// resource whose prior state holds one value and whose configuration holds
// another, then runs one offline `tofu plan -refresh=false` per type. The aws
// provider gets fake credentials, skips every account lookup and points every
// endpoint at a closed local port, so no call can reach AWS. An attribute
// whose probe plans a replace forces replacement; one that plans an update
// does not. A probe the provider refuses, a nested block, and an attribute of
// a structured type are reported as undetermined.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mitstack-eu/operator-kit/contract"
)

type attr struct {
	Type     any  `json:"type"`
	Required bool `json:"required"`
	Optional bool `json:"optional"`
	Computed bool `json:"computed"`
}

type resSchema struct {
	Version int `json:"version"`
	Block   struct {
		Attributes map[string]attr `json:"attributes"`
		BlockTypes map[string]any  `json:"block_types"`
	} `json:"block"`
}

// result is what the probe found for one type.
type result struct {
	Type          string            `json:"type"`
	Resource      string            `json:"resource"`
	Replace       []string          `json:"replace"`
	Update        []string          `json:"update"`
	Undetermined  map[string]string `json:"undetermined"`
	HandWritten   []string          `json:"handWrittenReplaceOn"`
	Agree         bool              `json:"agree"`
	PlanSeconds   float64           `json:"planSeconds"`
}

func main() {
	bin := flag.String("tofu", "", "tofu binary")
	schemaPath := flag.String("schema", "", "tofu providers schema -json output")
	work := flag.String("work", "", "working directory")
	cache := flag.String("plugin-cache", "", "TF_PLUGIN_CACHE_DIR")
	flag.Parse()
	raw, err := os.ReadFile(*schemaPath)
	if err != nil {
		fail(err)
	}
	var doc struct {
		ProviderSchemas map[string]struct {
			ResourceSchemas map[string]resSchema `json:"resource_schemas"`
		} `json:"provider_schemas"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		fail(err)
	}
	var results []result
	for _, id := range flag.Args() {
		rt := lookup(id)
		if rt.ProviderResource == nil {
			fail(fmt.Errorf("%s maps onto no provider resource", id))
		}
		rs := doc.ProviderSchemas[rt.ProviderResource.Provider].ResourceSchemas[rt.ProviderResource.Resource]
		r := probe(*bin, *cache, filepath.Join(*work, id), rt, rs)
		results = append(results, r)
		fmt.Fprintf(os.Stderr, "%s: replace %v; undetermined %d; agrees with the hand-written list: %v\n", id, r.Replace, len(r.Undetermined), r.Agree)
	}
	out, _ := json.MarshalIndent(results, "", "  ")
	fmt.Println(string(out))
}

func lookup(id string) contract.ResourceType {
	for _, rt := range contract.ResourceTypes() {
		if rt.ID == id {
			return rt
		}
	}
	fail(fmt.Errorf("no catalogue type %s", id))
	return contract.ResourceType{}
}

// values returns a prior and a new value for an attribute, or ok false when
// the attribute's type is structured beyond what a probe can fill.
func values(name string, t any) (a, b any, ok bool) {
	switch tt := t.(type) {
	case string:
		switch tt {
		case "bool":
			return false, true, true
		case "number":
			return numberPair(name)
		case "string":
			x, y := stringPair(name)
			return x, y, true
		}
	case []any:
		if len(tt) == 2 && tt[1] == "string" {
			switch tt[0] {
			case "list", "set":
				if name == "records" {
					return []any{"203.0.113.1"}, []any{"203.0.113.2"}, true
				}
				return []any{"probe-a"}, []any{"probe-b"}, true
			case "map":
				return map[string]any{"k": "probe-a"}, map[string]any{"k": "probe-b"}, true
			}
		}
	}
	return nil, nil, false
}

func numberPair(name string) (any, any, bool) {
	switch name {
	case "iops":
		return 3000, 4000, true
	case "throughput":
		return 125, 200, true
	case "size":
		return 20, 30, true
	case "max_session_duration":
		return 3600, 7200, true
	case "ipv4_netmask_length", "ipv6_netmask_length":
		return 16, 20, true
	case "ttl":
		return 60, 120, true
	case "wait_for_success_timeout_seconds":
		return 60, 120, true
	}
	return 10, 20, true
}

func stringPair(name string) (string, string) {
	switch {
	case strings.Contains(name, "cidr"):
		return "10.0.0.0/16", "10.1.0.0/16"
	case name == "availability_zone":
		return "eu-central-1a", "eu-central-1b"
	case name == "region":
		return "eu-central-1", "eu-west-1"
	case strings.HasSuffix(name, "policy") || name == "assume_role_policy":
		return `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`,
			`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ecs.amazonaws.com"},"Action":"sts:AssumeRole"}]}`
	case strings.HasSuffix(name, "_arn") || name == "arn" || name == "permissions_boundary" || name == "kms_key_id" || name == "outpost_arn":
		return "arn:aws:iam::111111111111:policy/probe-a", "arn:aws:iam::111111111111:policy/probe-b"
	case name == "type":
		return "gp3", "gp2"
	case name == "image_tag_mutability":
		return "MUTABLE", "IMMUTABLE"
	case name == "instance_tenancy":
		return "default", "dedicated"
	case name == "compliance_severity":
		return "LOW", "HIGH"
	case name == "schedule_expression":
		return "rate(30 minutes)", "rate(1 hour)"
	case name == "max_concurrency" || name == "max_errors":
		return "1", "2"
	case name == "zone_id":
		return "Z0000000000000000000A", "Z0000000000000000000B"
	case name == "path":
		return "/a/", "/b/"
	case name == "snapshot_id":
		return "snap-0123456789abcdef0", "snap-0123456789abcdef1"
	case name == "vpc_id":
		return "vpc-0123456789abcdef0", "vpc-0123456789abcdef1"
	case name == "ipv4_ipam_pool_id" || name == "ipv6_ipam_pool_id":
		return "ipam-pool-0123456789abcdef0", "ipam-pool-0123456789abcdef1"
	}
	return "probe-a", "probe-b"
}

// baseline returns a valid value for every required attribute, so each
// probe differs from its prior state in one attribute only.
func baseline(resource string, rs resSchema) map[string]any {
	base := map[string]any{}
	for name, a := range rs.Block.Attributes {
		if a.Required {
			x, _, _ := values(name, a.Type)
			base[name] = x
		}
	}
	if resource == "aws_ebs_volume" {
		base["size"] = 20
	}
	if resource == "aws_route53_record" {
		base["type"] = "A"
		base["records"] = []any{"203.0.113.1"}
		base["ttl"] = 60
	}
	return base
}

func probe(bin, cache, dir string, rt contract.ResourceType, rs resSchema) result {
	res := rt.ProviderResource.Resource
	r := result{Type: rt.ID, Resource: res, Undetermined: map[string]string{}, HandWritten: rt.ProviderResource.ReplaceOn}
	base := baseline(res, rs)

	var names []string
	for _, in := range rt.Inputs {
		names = append(names, in.Name)
	}
	sort.Strings(names)

	probes := map[string]string{} // probe resource name to the attribute it probes
	before := map[string]any{}
	after := map[string]any{}
	for _, name := range names {
		if _, isBlock := rs.Block.BlockTypes[name]; isBlock {
			r.Undetermined[name] = "a nested block, which the probe does not fill"
			continue
		}
		a, ok := rs.Block.Attributes[name]
		if !ok {
			r.Undetermined[name] = "not a top-level attribute of the schema"
			continue
		}
		vb, va, ok := values(name, a.Type)
		if !ok {
			r.Undetermined[name] = "a structured attribute, which the probe does not fill"
			continue
		}
		if res == "aws_route53_record" && name == "records" {
			vb, va = []any{"203.0.113.1"}, []any{"203.0.113.2"}
		}
		if res == "aws_route53_record" && name == "type" {
			vb, va = "A", "TXT"
		}
		pname := "p_" + name
		probes[pname] = name
		cb, ca := map[string]any{}, map[string]any{}
		for k, v := range base {
			cb[k], ca[k] = v, v
		}
		cb[name], ca[name] = vb, va
		before[pname], after[pname] = cb, ca
	}
	drop := func(refused map[string]string) {
		for pname, why := range refused {
			r.Undetermined[probes[pname]] = "the provider refused the probe value: " + why
			delete(probes, pname)
			delete(before, pname)
			delete(after, pname)
		}
	}

	// The prior state is what the provider itself plans to create from the
	// before value, defaults included, which the schema does not carry.
	if err := writeProbe(dir, rt, before, nil); err != nil {
		fail(err)
	}
	if out, err := tofu(bin, cache, dir, "init", "-input=false", "-no-color"); err != nil {
		fail(fmt.Errorf("init %s: %v\n%s", rt.ID, err, out))
	}
	var planned map[string]map[string]any
	for attempt := 0; attempt < 6 && len(probes) > 0; attempt++ {
		if err := writeProbe(dir, rt, before, nil); err != nil {
			fail(err)
		}
		_, refused, err := plan(bin, cache, dir, probes, "-out=before.tfplan")
		if len(refused) > 0 {
			drop(refused)
			continue
		}
		if err != nil {
			fail(fmt.Errorf("create plan %s: %v", rt.ID, err))
		}
		planned = plannedValues(bin, cache, dir, "before.tfplan")
		break
	}
	var instances []any
	for pname := range probes {
		state := map[string]any{}
		for k, v := range planned[pname] {
			if v != nil {
				state[k] = v
			}
		}
		state["id"] = "probe-" + probes[pname]
		instances = append(instances, map[string]any{
			"mode": "managed", "type": res, "name": pname,
			"provider":  `provider["` + rt.ProviderResource.Provider + `"]`,
			"instances": []any{map[string]any{"schema_version": rs.Version, "attributes": state}},
		})
	}

	for attempt := 0; attempt < 6 && len(probes) > 0; attempt++ {
		if err := writeProbe(dir, rt, after, instances); err != nil {
			fail(err)
		}
		actions, refused, err := plan(bin, cache, dir, probes)
		if len(refused) > 0 {
			drop(refused)
			instances = slices.DeleteFunc(instances, func(i any) bool { _, ok := probes[i.(map[string]any)["name"].(string)]; return !ok })
			continue
		}
		if err != nil {
			fail(fmt.Errorf("change plan %s: %v", rt.ID, err))
		}
		for pname, name := range probes {
			switch actions[pname] {
			case "replace":
				r.Replace = append(r.Replace, name)
			case "update":
				r.Update = append(r.Update, name)
			default:
				r.Undetermined[name] = "the plan answered " + actions[pname]
			}
		}
		break
	}
	sort.Strings(r.Replace)
	sort.Strings(r.Update)
	hand := append([]string(nil), r.HandWritten...)
	sort.Strings(hand)
	r.Agree = slices.Equal(hand, r.Replace)
	return r
}

// plan runs one offline plan and returns each probe's planned action, and the
// first diagnostic summary for each probe the provider refused.
func plan(bin, cache, dir string, probes map[string]string, extra ...string) (map[string]string, map[string]string, error) {
	args := append([]string{"plan", "-refresh=false", "-lock=false", "-input=false", "-json"}, extra...)
	out, err := tofu(bin, cache, dir, args...)
	refused := map[string]string{}
	actions := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var line struct {
			Type       string `json:"type"`
			Diagnostic struct {
				Summary string `json:"summary"`
				Address string `json:"address"`
			} `json:"diagnostic"`
			Change struct {
				Resource struct {
					Name string `json:"resource_name"`
				} `json:"resource"`
				Action string `json:"action"`
			} `json:"change"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		switch line.Type {
		case "planned_change":
			actions[line.Change.Resource.Name] = line.Change.Action
		case "diagnostic":
			for pname := range probes {
				if strings.HasSuffix(line.Diagnostic.Address, "."+pname) {
					if _, seen := refused[pname]; !seen {
						refused[pname] = line.Diagnostic.Summary
					}
				}
			}
		}
	}
	if err != nil {
		return actions, refused, fmt.Errorf("%v\n%s", err, lastLines(string(out), 20))
	}
	return actions, refused, nil
}

// plannedValues reads each probe's planned values from a saved plan.
func plannedValues(bin, cache, dir, planFile string) map[string]map[string]any {
	out, err := tofu(bin, cache, dir, "show", "-json", planFile)
	if err != nil {
		fail(fmt.Errorf("show %s: %v", planFile, err))
	}
	var doc struct {
		PlannedValues struct {
			RootModule struct {
				Resources []struct {
					Name   string         `json:"name"`
					Values map[string]any `json:"values"`
				} `json:"resources"`
			} `json:"root_module"`
		} `json:"planned_values"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		fail(fmt.Errorf("reading %s: %v", planFile, err))
	}
	vals := map[string]map[string]any{}
	for _, r := range doc.PlannedValues.RootModule.Resources {
		vals[r.Name] = r.Values
	}
	return vals
}

// writeProbe writes the configuration and, when instances is not nil, the
// prior state; with nil it removes any state, for a create plan.
func writeProbe(dir string, rt contract.ResourceType, config map[string]any, instances []any) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dead := "http://127.0.0.1:9"
	main := map[string]any{
		"terraform": map[string]any{"required_providers": map[string]any{"aws": map[string]any{"source": "hashicorp/aws", "version": "6.64.0"}}},
		"provider": map[string]any{"aws": map[string]any{
			"region": "eu-central-1", "access_key": "offline", "secret_key": "offline",
			"skip_credentials_validation": true, "skip_requesting_account_id": true,
			"skip_metadata_api_check": true, "skip_region_validation": true,
			"endpoints": []any{map[string]any{"ec2": dead, "iam": dead, "ecr": dead, "ssm": dead, "route53": dead, "sts": dead}},
		}},
		"resource": map[string]any{rt.ProviderResource.Resource: config},
	}
	raw, _ := json.MarshalIndent(main, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "main.tf.json"), raw, 0o600); err != nil {
		return err
	}
	if instances == nil {
		if err := os.Remove(filepath.Join(dir, "terraform.tfstate")); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	state :=map[string]any{"version": 4, "terraform_version": "1.12.3", "serial": 1, "lineage": "replacement-probe",
		"outputs": map[string]any{}, "resources": instances}
	raw, _ = json.MarshalIndent(state, "", "  ")
	return os.WriteFile(filepath.Join(dir, "terraform.tfstate"), raw, 0o600)
}

func tofu(bin, cache, dir string, args ...string) ([]byte, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1", "CHECKPOINT_DISABLE=1",
		"AWS_CONFIG_FILE=/dev/null", "AWS_SHARED_CREDENTIALS_FILE=/dev/null", "AWS_EC2_METADATA_DISABLED=true", "AWS_PROFILE=")
	if cache != "" {
		cmd.Env = append(cmd.Env, "TF_PLUGIN_CACHE_DIR="+cache)
	}
	return cmd.CombinedOutput()
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "replacement-probe:", err)
	os.Exit(1)
}
