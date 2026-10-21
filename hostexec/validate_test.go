package hostexec_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/hostexec"
)

// Target values come from manifests and subscription inputs, and neither ssh nor the aws CLI has an end-of-options marker, so validation before any spawn is the only defence.
func TestRun_RefusesAnInvalidHostOrOptionBeforeSpawningAnything(t *testing.T) {
	type refusal struct {
		name   string
		host   hostexec.Host
		opts   []hostexec.SendOption
		errHas string
	}
	cases := []refusal{
		{"an unknown kind is named rather than defaulted", hostexec.Host{Kind: "telnet", Target: "somehost", Region: fixtureRegion}, nil, "telnet"},
		{"an empty kind is named as a quoted empty string", hostexec.Host{Target: "somehost", Region: fixtureRegion}, nil, `""`},
		{"an ssm host with no target", hostexec.Host{Kind: hostexec.KindSSM, Region: fixtureRegion}, nil, "Target is required"},
		{"an ssh host with no target", hostexec.Host{Kind: hostexec.KindSSH}, nil, "Target is required"},
		{"an ssm host with no region, which is never defaulted", hostexec.Host{Kind: hostexec.KindSSM, Target: fixtureInstance}, nil, "Region is required"},
		{"an ssh target beginning with a dash", hostexec.Host{Kind: hostexec.KindSSH, Target: "-oProxyCommand=touch /tmp/pwned", Credential: "PEM-DATA"}, nil, `begins with "-"`},
		{"an ssm target beginning with a dash", hostexec.Host{Kind: hostexec.KindSSM, Target: "-oProxyCommand=touch /tmp/pwned", Region: fixtureRegion}, nil, `begins with "-"`},
	}
	for _, o := range []struct {
		name   string
		opt    hostexec.SendOption
		errHas string
	}{
		{"a nil option", nil, "nil SendOption"},
		{"an empty comment", hostexec.WithComment(""), "comment is empty"},
		{"a comment of 101 bytes, never truncated, since a caller finds its command by it", hostexec.WithComment(strings.Repeat("c", 101)), "over SSM's 100-byte limit"},
		{"a comment of 51 runes but 102 bytes", hostexec.WithComment(strings.Repeat("é", 51)), "over SSM's 100-byte limit"},
		{"a comment beginning with a dash", hostexec.WithComment("-mitstack app-runner install lane-a"), `begins with "-"`},
		{"a comment beginning with file://", hostexec.WithComment("file://comment-fixture"), "file://"},
		{"a comment beginning with fileb://", hostexec.WithComment("fileb://comment-fixture"), "fileb://"},
		{"a comment beginning with FILEB://", hostexec.WithComment("FILEB://comment-fixture"), "fileb://"},
		{"a comment beginning with http://", hostexec.WithComment("http://comment-fixture"), "http://"},
		{"a comment beginning with https://", hostexec.WithComment("https://comment-fixture"), "https://"},
		{"a comment beginning with HTTPS://", hostexec.WithComment("HTTPS://comment-fixture"), "https://"},
		{"a delivery timeout of zero", hostexec.WithDeliveryTimeout(0), "outside"},
		{"a negative delivery timeout", hostexec.WithDeliveryTimeout(-600 * time.Second), "outside"},
		{"a delivery timeout below 30s", hostexec.WithDeliveryTimeout(29 * time.Second), "outside"},
		{"a delivery timeout above 2592000s", hostexec.WithDeliveryTimeout(2592001 * time.Second), "outside"},
		{"a delivery timeout not in whole seconds", hostexec.WithDeliveryTimeout(30500 * time.Millisecond), "not a whole number of seconds"},
		{"an execution timeout of zero", hostexec.WithExecutionTimeout(0), "outside"},
		{"a negative execution timeout", hostexec.WithExecutionTimeout(-1 * time.Second), "outside"},
		{"an execution timeout below 1s", hostexec.WithExecutionTimeout(500 * time.Millisecond), "not a whole number of seconds"},
		{"an execution timeout above 172800s", hostexec.WithExecutionTimeout(172801 * time.Second), "outside"},
		{"an execution timeout not in whole seconds", hostexec.WithExecutionTimeout(1500 * time.Millisecond), "not a whole number of seconds"},
	} {
		cases = append(cases, refusal{"ssm with " + o.name, ssmHost(), []hostexec.SendOption{o.opt}, o.errHas})
	}
	for _, o := range []struct {
		name string
		opt  hostexec.SendOption
	}{
		{"comment", hostexec.WithComment(fixtureComment)},
		{"delivery timeout", hostexec.WithDeliveryTimeout(600 * time.Second)},
		{"execution timeout", hostexec.WithExecutionTimeout(1800 * time.Second)},
		{"detached poll", hostexec.WithDetachedPoll()},
	} {
		cases = append(cases, refusal{"ssh with the " + o.name + " option, rejected rather than ignored", sshHost("PEM-DATA"), []hostexec.SendOption{o.opt}, "send options apply only"})
	}
	for _, ep := range entryPoints {
		for _, tc := range cases {
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				f := newFakes(t, awsFake{stdout: "READY=0"}, `printf 'READY=0\n'`)

				out, err := ep.call(context.Background(), tc.host, fixtureScript, tc.opts...)

				if err == nil || !strings.Contains(err.Error(), tc.errHas) || out != "" {
					t.Errorf("%s(%+v) = %q, %v; want no output and an error saying %q", ep.name, tc.host, out, err, tc.errHas)
				}
				if n := len(f.calls("aws")) + len(f.calls("ssh")); n != 0 {
					t.Errorf("%s spawned %d subprocess(es), want validation before any spawn", ep.name, n)
				}
			})
		}
	}
}

func TestSSMHost(t *testing.T) {
	const credential = "arn:aws:iam::000000000000:role/ssmhost-credential-sentinel"
	env := []string{"AWS_ROLE_ARN=arn:aws:iam::000000000000:role/ssmhost-env-sentinel", "AWS_REGION=eu-central-1"}
	for _, kind := range []hostexec.Kind{hostexec.KindSSH, "", "local", "AWS-SSM", " aws-ssm", "aws-ssm ", "ssm", "kind-sentinel-ssmhost"} {
		t.Run("the kind "+string(kind)+" is refused, carrying no part of the host", func(t *testing.T) {
			h, err := hostexec.SSMHost(kind, fixtureInstance, credential, fixtureRegion, env)

			if !errors.Is(err, hostexec.ErrNotSSM) || !reflect.DeepEqual(h, hostexec.Host{}) {
				t.Fatalf("SSMHost(%q) = %+v, %v; want a zero Host and ErrNotSSM", kind, h, err)
			}
			for _, part := range append([]string{credential, fixtureInstance, fixtureRegion, "kind-sentinel-ssmhost"}, env...) {
				if strings.Contains(err.Error(), part) {
					t.Errorf("the refusal %q carries %q from the host it refused", err, part)
				}
			}
		})
	}
	for _, tc := range []struct {
		name               string
		target, credential string
		region             string
		env                []string
	}{
		{"it carries the target, credential, region and env", fixtureInstance, credential, fixtureRegion, env},
		{"it leaves the credential empty when none is given", fixtureInstance, "", fixtureRegion, env},
		{"it carries a nil env", fixtureInstance, "", fixtureRegion, nil},
		{"it leaves an empty target and region for Run to judge", "", "", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := hostexec.SSMHost(hostexec.KindSSM, tc.target, tc.credential, tc.region, tc.env)

			want := hostexec.Host{Kind: hostexec.KindSSM, Target: tc.target, Credential: tc.credential, Region: tc.region, Env: tc.env}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("SSMHost = %+v, %v; want %+v", got, err, want)
			}
			if tc.target == "" && hostexec.Run(context.Background(), got, "true") == nil {
				t.Error("Run accepted an ssm host with no target")
			}
		})
	}
}
