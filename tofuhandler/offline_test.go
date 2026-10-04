package tofuhandler

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// sampleInputs are representative inputs for every type, shaped like the
// values aws-app-infra, postgres-db and plain set today.
var sampleInputs = map[string]map[string]any{
	"aws.vpc": {"cidr_block": "172.31.0.0/16", "enable_dns_hostnames": true, "enable_dns_support": true},
	"aws.security-group": {
		"name": "spike-app", "description": "The app host, open on 80 and 443 only", "vpc_id": "vpc-0123456789abcdef0",
		"ingress": []any{
			map[string]any{"description": "HTTP", "from_port": 80, "to_port": 80, "protocol": "tcp", "cidr_blocks": []any{"0.0.0.0/0"},
				"ipv6_cidr_blocks": []any{}, "prefix_list_ids": []any{}, "security_groups": []any{}, "self": false},
			map[string]any{"description": "HTTPS", "from_port": 443, "to_port": 443, "protocol": "tcp", "cidr_blocks": []any{"0.0.0.0/0"},
				"ipv6_cidr_blocks": []any{}, "prefix_list_ids": []any{}, "security_groups": []any{}, "self": false},
		},
	},
	"aws.ebs-volume":     {"availability_zone": "eu-central-1a", "size": 20, "type": "gp3", "encrypted": true},
	"aws.ecr-repository": {"name": "spike-app", "force_delete": true, "image_scanning_configuration": []any{map[string]any{"scan_on_push": true}}},
	"aws.iam-role": {"name": "spike-app-deploy",
		"assume_role_policy": `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`},
	"aws.ssm-association": {"name": "AWS-RunShellScript", "parameters": map[string]any{"commands": "true"},
		"targets": []any{map[string]any{"key": "InstanceIds", "values": []any{"i-0123456789abcdef0"}}}},
	"aws.route53-record": {"zone_id": "Z0123456789ABCDEFGHIJ", "name": "spike.apps.mitstack.dev", "type": "A", "ttl": 60,
		"records": []any{"203.0.113.10"}},
	"aws.ec2-instance": {"name_prefix": "mitstack-spike", "app_name": "spike", "ami_id": "ami-0123456789abcdef0",
		"subnet_id": "subnet-0123456789abcdef0", "security_group_id": "sg-0123456789abcdef0", "region": "eu-central-1",
		"ecr_repository_arn": "arn:aws:ecr:eu-central-1:111111111111:repository/spike-app"},
	"aws.egress-proxy": {"name_prefix": "mitstack-spike", "ami_id": "ami-0123456789abcdef0", "vpc_id": "vpc-0123456789abcdef0",
		"subnet_id": "subnet-0123456789abcdef0", "app_security_group_id": "sg-0123456789abcdef0", "proxy_script": "dnf install -y squid"},
	"host.container": {"docker_host": "unix:///var/run/docker.sock", "name": "mitstack-plain-spike", "image": "plain:local",
		"env": map[string]any{"TRACKER_DB_HOST": "db", "TRACKER_DB_PASSWORD": "x"}, "health_command": "wget -qO- -T 2 http://127.0.0.1:8080/healthz"},
	"host.postgres-database": {"docker_host": "unix:///var/run/docker.sock", "name": "mitstack-postgres-spike",
		"data_dir": "/tmp/mitstack-postgres-spike", "db_name": "plain", "username": "plain", "image": "postgres:16-alpine"},
}

// TestOfflinePlan renders every type with sampleInputs, then runs tofu
// init, validate and a create plan against it. Every AWS endpoint points at
// a closed local port and every account lookup is skipped, so no command can
// reach AWS. It runs only when TOFU_BIN names a tofu binary.
func TestOfflinePlan(t *testing.T) {
	bin := os.Getenv("TOFU_BIN")
	if bin == "" {
		t.Skip("TOFU_BIN is unset")
	}
	e := &Engine{Bin: bin, Root: t.TempDir(), PluginCache: os.Getenv("TF_PLUGIN_CACHE_DIR"), Offline: true,
		StatePassphrase: "offline-plan-passphrase"}
	ids := make([]string, 0, len(Specs))
	for id := range Specs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			if _, ok := sampleInputs[id]; !ok {
				t.Fatalf("no sample inputs for %s", id)
			}
			files, err := render(Specs[id], sampleInputs[id], "eu-central-1", nil, true)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(e.Root, id)
			if err := writeRoot(dir, files); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			for _, args := range [][]string{
				{"init", "-input=false", "-no-color"},
				{"validate", "-no-color"},
				{"plan", "-input=false", "-no-color", "-refresh=false", "-lock=false"},
			} {
				start := time.Now()
				out, err := e.run(ctx, dir, args...)
				if err != nil {
					t.Fatalf("tofu %s: %v\n%s", args[0], err, out)
				}
				t.Logf("tofu %s: %s", args[0], time.Since(start).Round(10*time.Millisecond))
				if args[0] == "plan" {
					t.Logf("%s", lastLines(out, 2))
				}
			}
		})
	}
}
