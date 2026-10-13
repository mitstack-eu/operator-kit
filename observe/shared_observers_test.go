package observe_test

// Every shared observer reads the identity it is handed, and an identity missing its key is unknown without a call.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/observe"
)

var (
	_ handler.Observer = observe.S3Bucket{}
	_ handler.Observer = observe.VPC{}
	_ handler.Observer = observe.SecurityGroup{}
	_ handler.Observer = observe.EBSVolume{}
	_ handler.Observer = observe.ECRRepository{}
	_ handler.Observer = observe.IAMRole{}
	_ handler.Observer = observe.Route53Record{}
	_ handler.Observer = observe.SSMAssociation{}
	_ handler.Observer = observe.EC2Container{}
	_ handler.Observer = observe.EC2PostgresDatabase{}
	_ handler.Observer = observe.HetznerServer{}
	_ handler.Observer = observe.HetznerPrimaryIP{}
	_ handler.Observer = observe.HetznerFirewall{}
	_ handler.Observer = observe.HetznerNetwork{}
	_ handler.Observer = observe.GitHubRepository{}
	_ handler.Observer = observe.GitHubCommit{}
)

// existsFake answers every existence question from the keys it holds, and
// records each key it was asked about.
type existsFake struct {
	held  map[string]bool
	asked []string
	err   error
}

func (f *existsFake) answer(key string) (bool, error) {
	f.asked = append(f.asked, key)
	return f.held[key], f.err
}

func (f *existsFake) BucketExists(_ context.Context, _ contract.Desired, bucket string) (bool, error) {
	return f.answer(bucket)
}
func (f *existsFake) VPCExists(_ context.Context, _ contract.Desired, vpcID string) (bool, error) {
	return f.answer(vpcID)
}
func (f *existsFake) SecurityGroupExists(_ context.Context, _ contract.Desired, groupID string) (bool, error) {
	return f.answer(groupID)
}
func (f *existsFake) AssociationExists(_ context.Context, _ contract.Desired, associationID string) (bool, error) {
	return f.answer(associationID)
}
func (f *existsFake) NetworkExists(_ context.Context, _ contract.Desired, networkID string) (bool, error) {
	return f.answer(networkID)
}
func (f *existsFake) FirewallExists(_ context.Context, _ contract.Desired, firewallID string) (bool, error) {
	return f.answer(firewallID)
}
func (f *existsFake) RepositoryExists(_ context.Context, _ contract.Desired, fullName string) (bool, error) {
	return f.answer(fullName)
}
func (f *existsFake) CommitExists(_ context.Context, _ contract.Desired, repo, sha string) (bool, error) {
	return f.answer(repo + "@" + sha)
}

type observer interface {
	ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error)
}

// existenceObservers are the observers that ask only whether the object the
// identity names exists, each with the identity that names it, the key the
// fake is asked about, and the outputs a present object reports.
var existenceObservers = []struct {
	typ      string
	build    func(f *existsFake) observer
	identity map[string]string
	key      string
	outputs  map[string]string
}{
	{"aws.s3-bucket", func(f *existsFake) observer { return observe.S3Bucket{API: f} },
		map[string]string{"bucket": "app-state"}, "app-state", nil},
	{"aws.vpc", func(f *existsFake) observer { return observe.VPC{API: f} },
		map[string]string{"vpcId": "vpc-0abc"}, "vpc-0abc", nil},
	{"aws.security-group", func(f *existsFake) observer { return observe.SecurityGroup{API: f} },
		map[string]string{"groupId": "sg-0abc"}, "sg-0abc", nil},
	{"aws.ssm-association", func(f *existsFake) observer { return observe.SSMAssociation{API: f} },
		map[string]string{"associationId": "assoc-0abc"}, "assoc-0abc", nil},
	{"hetzner.firewall", func(f *existsFake) observer { return observe.HetznerFirewall{API: f} },
		map[string]string{"firewallId": "4711"}, "4711", nil},
	{"hetzner.network", func(f *existsFake) observer { return observe.HetznerNetwork{API: f} },
		map[string]string{"networkId": "77"}, "77", nil},
	{"github.repository", func(f *existsFake) observer { return observe.GitHubRepository{API: f} },
		map[string]string{"repo": "acme/app"}, "acme/app", map[string]string{"repo": "acme/app"}},
	{"github.commit", func(f *existsFake) observer { return observe.GitHubCommit{API: f} },
		map[string]string{"repo": "acme/app", "sha": "0123abcd", "branch": "main"}, "acme/app@0123abcd",
		map[string]string{"sourceSha": "0123abcd", "sourceBranch": "main"}},
}

func TestExistenceObservers_AnswerFromTheProvider(t *testing.T) {
	for _, o := range existenceObservers {
		t.Run(o.typ, func(t *testing.T) {
			t.Run("present", func(t *testing.T) {
				f := &existsFake{held: map[string]bool{o.key: true}}
				got, err := o.build(f).ObserveResource(context.Background(), refFor(o.typ, o.identity))
				wantObservation(t, got, err, contract.Present, o.outputs, "")
				if len(f.asked) != 1 || f.asked[0] != o.key {
					t.Errorf("asked about %q, want %q once", f.asked, o.key)
				}
			})
			t.Run("absent", func(t *testing.T) {
				got, err := o.build(&existsFake{}).ObserveResource(context.Background(), refFor(o.typ, o.identity))
				wantObservation(t, got, err, contract.Absent, nil, "")
			})
			t.Run("no identity", func(t *testing.T) {
				f := &existsFake{held: map[string]bool{o.key: true}}
				got, err := o.build(f).ObserveResource(context.Background(), refFor(o.typ, nil))
				wantObservation(t, got, err, contract.Unknown, nil, "")
				if len(f.asked) != 0 {
					t.Errorf("asked about %q with no identity, want no call", f.asked)
				}
			})
			t.Run("an API error", func(t *testing.T) {
				boom := errors.New("provider: throttled")
				_, err := o.build(&existsFake{err: boom}).ObserveResource(context.Background(), refFor(o.typ, o.identity))
				if !errors.Is(err, boom) {
					t.Errorf("error = %v, want the API's error", err)
				}
			})
		})
	}
}

type fakeVolumes map[string]observe.EBSVolumeDescription

func (f fakeVolumes) DescribeVolume(_ context.Context, _ contract.Desired, volumeID string) (observe.EBSVolumeDescription, bool, error) {
	v, ok := f[volumeID]
	return v, ok, nil
}

func TestEBSVolume_ReadsTheVolumesState(t *testing.T) {
	cases := []struct {
		state string
		want  contract.State
	}{
		{"available", contract.Present},
		{"in-use", contract.Present},
		{"creating", contract.Unknown},
		{"deleting", contract.Unknown},
		{"deleted", contract.Absent},
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			api := fakeVolumes{"vol-0abc": {VolumeID: "vol-0abc", State: tc.state}}
			got, err := observe.EBSVolume{API: api}.ObserveResource(context.Background(), refFor("aws.ebs-volume", map[string]string{"volumeId": "vol-0abc"}))
			wantObservation(t, got, err, tc.want, nil, "")
		})
	}
	got, err := observe.EBSVolume{API: fakeVolumes{}}.ObserveResource(context.Background(), refFor("aws.ebs-volume", map[string]string{"volumeId": "vol-0abc"}))
	wantObservation(t, got, err, contract.Absent, nil, "")
}

type fakeRepositories map[string]observe.ECRRepositoryDescription

func (f fakeRepositories) DescribeRepository(_ context.Context, _ contract.Desired, name string) (observe.ECRRepositoryDescription, bool, error) {
	r, ok := f[name]
	return r, ok, nil
}

func TestECRRepository_ReportsTheRegistryHost(t *testing.T) {
	api := fakeRepositories{"app": {Name: "app", URI: "123456789012.dkr.ecr.eu-central-1.amazonaws.com/app"}}
	ref := refFor("aws.ecr-repository", map[string]string{"repositoryName": "app"})
	got, err := observe.ECRRepository{API: api}.ObserveResource(context.Background(), ref)
	wantObservation(t, got, err, contract.Present, map[string]string{"registryHost": "123456789012.dkr.ecr.eu-central-1.amazonaws.com"}, "")

	got, err = observe.ECRRepository{API: fakeRepositories{}}.ObserveResource(context.Background(), ref)
	wantObservation(t, got, err, contract.Absent, nil, "")
}

type fakeRoles map[string]observe.IAMRoleDescription

func (f fakeRoles) DescribeRole(_ context.Context, _ contract.Desired, name string) (observe.IAMRoleDescription, bool, error) {
	r, ok := f[name]
	return r, ok, nil
}

// aws.iam-role marks roleArn secret, so it is reported among the secret
// outputs and never among the plain ones.
func TestIAMRole_ReportsItsARNAsASecretOutput(t *testing.T) {
	const arn = "arn:aws:iam::123456789012:role/deploy"
	api := fakeRoles{"deploy": {Name: "deploy", ARN: arn}}
	ref := refFor("aws.iam-role", map[string]string{"roleName": "deploy"})
	got, err := observe.IAMRole{API: api}.ObserveResource(context.Background(), ref)
	wantObservation(t, got, err, contract.Present, nil, "")
	if got.SecretOutputs["roleArn"] != arn {
		t.Errorf("secret outputs = %v, want roleArn %q", got.SecretOutputs, arn)
	}
	if _, ok := got.Outputs["roleArn"]; ok {
		t.Errorf("outputs = %v carry roleArn, which the type marks secret", got.Outputs)
	}

	got, err = observe.IAMRole{API: fakeRoles{}}.ObserveResource(context.Background(), ref)
	wantObservation(t, got, err, contract.Absent, nil, "")
}

type fakeRecords map[string]observe.Route53RecordDescription

func (f fakeRecords) DescribeRecord(_ context.Context, _ contract.Desired, zoneID, name string) (observe.Route53RecordDescription, bool, error) {
	r, ok := f[zoneID+"/"+name]
	return r, ok, nil
}

func TestRoute53Record_ReportsTheAppsHostnameAndURL(t *testing.T) {
	api := fakeRecords{"Z0ABC/app.example.com": {Name: "app.example.com.", Type: "A", Values: []string{"203.0.113.7"}}}
	ref := refFor("aws.route53-record", map[string]string{"hostedZoneId": "Z0ABC", "recordName": "app.example.com"})
	got, err := observe.Route53Record{API: api}.ObserveResource(context.Background(), ref)
	wantObservation(t, got, err, contract.Present, map[string]string{"appHostname": "app.example.com", "appUrl": "https://app.example.com"}, "")

	got, err = observe.Route53Record{API: fakeRecords{}}.ObserveResource(context.Background(), ref)
	wantObservation(t, got, err, contract.Absent, nil, "")
}

// fakeHost answers a script run on an instance with the stdout the test
// gives the object the script names, and records where it ran. A
// HostCommander reports a nonzero exit as an error, so err stands for a
// probe that failed on the host.
type fakeHost struct {
	instance string
	stdout   map[string]string
	err      error
	ranOn    []string
	scripts  []string
}

func (f *fakeHost) RunOnInstance(_ context.Context, _ contract.Desired, instanceID, script string) (string, error) {
	f.ranOn = append(f.ranOn, instanceID)
	f.scripts = append(f.scripts, script)
	if instanceID != f.instance {
		return "", errors.New("no such instance " + instanceID)
	}
	if f.err != nil {
		return "", f.err
	}
	for name, out := range f.stdout {
		if strings.Contains(script, name) {
			return out, nil
		}
	}
	return "", nil
}

// An aws.ec2-container's script prints the container's state, or the word
// absent when Docker answers that no such container exists, run on the
// instance its identity names. Empty output proves nothing, so it is
// unknown, never absent.
func TestEC2Container_AsksTheInstanceItRunsOn(t *testing.T) {
	cases := []struct {
		label, stdout string
		state         contract.State
		detail        string
	}{
		{"running", "running\n", contract.Present, ""},
		{"no such container", "absent\n", contract.Absent, ""},
		{"no output", "", contract.Unknown, ""},
		{"exited", "exited\n", contract.Unknown, "exited"},
		{"restarting", "restarting\n", contract.Unknown, "restarting"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			host := &fakeHost{instance: "i-0abc", stdout: map[string]string{"plain-tracker": tc.stdout}}
			got, err := observe.EC2Container{API: host}.ObserveResource(context.Background(),
				refFor("aws.ec2-container", map[string]string{"instanceId": "i-0abc", "container": "plain-tracker"}))
			wantObservation(t, got, err, tc.state, nil, tc.detail)
			if len(host.ranOn) != 1 || host.ranOn[0] != "i-0abc" {
				t.Errorf("ran on %q, want i-0abc once", host.ranOn)
			}
		})
	}
	t.Run("a probe that failed on the host", func(t *testing.T) {
		boom := errors.New("ssm: command exited 125")
		got, err := observe.EC2Container{API: &fakeHost{instance: "i-0abc", err: boom}}.ObserveResource(context.Background(),
			refFor("aws.ec2-container", map[string]string{"instanceId": "i-0abc", "container": "plain-tracker"}))
		if !errors.Is(err, boom) {
			t.Errorf("observation %+v, error %v; want the host's error returned, never read as absence", got, err)
		}
	})
	t.Run("no instance in the identity", func(t *testing.T) {
		host := &fakeHost{instance: "i-0abc"}
		got, err := observe.EC2Container{API: host}.ObserveResource(context.Background(),
			refFor("aws.ec2-container", map[string]string{"container": "plain-tracker"}))
		wantObservation(t, got, err, contract.Unknown, nil, "")
		if len(host.ranOn) != 0 {
			t.Errorf("ran on %q, want nothing run without an instance id", host.ranOn)
		}
	})
}

// postgres-db runs Postgres in a container per subscription. The probe
// prints PRESENT=1 when Docker inspects the container, PRESENT=0 when Docker
// answers and has no such container, and PRESENT=unknown when Docker itself
// does not answer; for a container that exists it then prints READY=<the
// exit status of pg_isready>, 0 when it accepts connections.
func TestEC2PostgresDatabase_ProbesItsContainerOnTheInstance(t *testing.T) {
	cases := []struct {
		label, stdout string
		state         contract.State
		detail        string
	}{
		{"accepting connections", "PRESENT=1\nREADY=0\n", contract.Present, ""},
		{"not yet accepting connections", "PRESENT=1\nREADY=2\n", contract.Unknown, "ready"},
		{"no readiness answer", "PRESENT=1\n", contract.Unknown, ""},
		{"gone", "PRESENT=0\n", contract.Absent, ""},
		{"docker not answering", "PRESENT=unknown\n", contract.Unknown, ""},
		{"no output", "", contract.Unknown, ""},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			host := &fakeHost{instance: "i-0abc", stdout: map[string]string{"pg-sub-1": tc.stdout}}
			got, err := observe.EC2PostgresDatabase{API: host}.ObserveResource(context.Background(),
				refFor("aws.ec2-postgres-database", map[string]string{"instanceId": "i-0abc", "container": "pg-sub-1", "database": "tracker"}))
			var outputs map[string]string
			if tc.state == contract.Present {
				outputs = map[string]string{"dbName": "tracker"}
			}
			wantObservation(t, got, err, tc.state, outputs, tc.detail)
			if len(host.ranOn) != 1 || host.ranOn[0] != "i-0abc" {
				t.Fatalf("ran on %q, want i-0abc once", host.ranOn)
			}
			for _, part := range []string{"pg-sub-1", "docker inspect", "pg_isready", "PRESENT=", "READY="} {
				if !strings.Contains(host.scripts[0], part) {
					t.Errorf("probe script %q does not carry %q", host.scripts[0], part)
				}
			}
		})
	}
}

func TestEC2PostgresDatabase_ReportsDBNameOnlyWhenTheIdentityCarriesIt(t *testing.T) {
	host := &fakeHost{instance: "i-0abc", stdout: map[string]string{"pg-sub-1": "PRESENT=1\nREADY=0\n"}}
	got, err := observe.EC2PostgresDatabase{API: host}.ObserveResource(context.Background(),
		refFor("aws.ec2-postgres-database", map[string]string{"instanceId": "i-0abc", "container": "pg-sub-1"}))
	wantObservation(t, got, err, contract.Present, nil, "")
	if v, ok := got.Outputs["dbName"]; ok {
		t.Errorf("outputs carry dbName %q, want none without a database in the identity", v)
	}
}

func TestEC2PostgresDatabase_NeedsTheInstanceAndTheContainer(t *testing.T) {
	for label, identity := range map[string]map[string]string{
		"no container, only the retired database key": {"instanceId": "i-0abc", "database": "tracker"},
		"no instance": {"container": "pg-sub-1"},
	} {
		t.Run(label, func(t *testing.T) {
			host := &fakeHost{instance: "i-0abc", stdout: map[string]string{"pg-sub-1": "PRESENT=1\nREADY=0\n", "tracker": "PRESENT=1\nREADY=0\n"}}
			got, err := observe.EC2PostgresDatabase{API: host}.ObserveResource(context.Background(), refFor("aws.ec2-postgres-database", identity))
			wantObservation(t, got, err, contract.Unknown, nil, "")
			if len(host.ranOn) != 0 {
				t.Errorf("ran on %q, want nothing run", host.ranOn)
			}
		})
	}
}

func TestEC2PostgresDatabase_ReturnsAProbeThatFailedOnTheHost(t *testing.T) {
	boom := errors.New("ssm: command exited 1")
	got, err := observe.EC2PostgresDatabase{API: &fakeHost{instance: "i-0abc", err: boom}}.ObserveResource(context.Background(),
		refFor("aws.ec2-postgres-database", map[string]string{"instanceId": "i-0abc", "container": "pg-sub-1"}))
	if !errors.Is(err, boom) {
		t.Errorf("observation %+v, error %v; want the host's error returned, never read as absence", got, err)
	}
}

type fakeServers map[string]observe.HetznerServerDescription

func (f fakeServers) GetServer(_ context.Context, _ contract.Desired, serverID string) (observe.HetznerServerDescription, bool, error) {
	s, ok := f[serverID]
	return s, ok, nil
}

func TestHetznerServer_ReadsTheServersStatus(t *testing.T) {
	server := func(status string) fakeServers {
		return fakeServers{"4711": {ID: "4711", Status: status, PublicIP: "198.51.100.9", Location: "fsn1"}}
	}
	ref := refFor("hetzner.server", map[string]string{"serverId": "4711"})
	got, err := observe.HetznerServer{API: server("running")}.ObserveResource(context.Background(), ref)
	wantObservation(t, got, err, contract.Present,
		map[string]string{"hostId": "4711", "cloud.region": "fsn1", "execKind": "ssh", "execTarget": "198.51.100.9"}, "")
	got, err = observe.HetznerServer{API: server("off")}.ObserveResource(context.Background(), ref)
	wantObservation(t, got, err, contract.Unknown, nil, "off")
	got, err = observe.HetznerServer{API: fakeServers{}}.ObserveResource(context.Background(), ref)
	wantObservation(t, got, err, contract.Absent, nil, "")
}

type fakePrimaryIPs map[string]observe.HetznerPrimaryIPDescription

func (f fakePrimaryIPs) GetPrimaryIP(_ context.Context, _ contract.Desired, id string) (observe.HetznerPrimaryIPDescription, bool, error) {
	p, ok := f[id]
	return p, ok, nil
}

func TestHetznerPrimaryIP_ReportsItsAddress(t *testing.T) {
	ref := refFor("hetzner.primary-ip", map[string]string{"primaryIpId": "99"})
	got, err := observe.HetznerPrimaryIP{API: fakePrimaryIPs{"99": {ID: "99", IP: "198.51.100.9"}}}.ObserveResource(context.Background(), ref)
	wantObservation(t, got, err, contract.Present, map[string]string{"publicIp": "198.51.100.9"}, "")
	got, err = observe.HetznerPrimaryIP{API: fakePrimaryIPs{}}.ObserveResource(context.Background(), ref)
	wantObservation(t, got, err, contract.Absent, nil, "")
}
