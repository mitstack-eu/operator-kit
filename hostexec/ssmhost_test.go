package hostexec_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/hostexec"
)

const (
	ssmHostTarget     = "i-0123456789abcdef0"
	ssmHostCredential = "arn:aws:iam::000000000000:role/ssmhost-credential-sentinel"
	ssmHostRegion     = "eu-central-1"
)

func ssmHostEnv() []string {
	return []string{"AWS_ROLE_ARN=arn:aws:iam::000000000000:role/ssmhost-env-sentinel", "AWS_REGION=eu-central-1"}
}

func TestSSMHost_RefusesEveryKindOtherThanAwsSSM(t *testing.T) {
	for _, kind := range []hostexec.Kind{hostexec.KindSSH, "", "local", "AWS-SSM", " aws-ssm", "aws-ssm ", "ssm"} {
		t.Run(string(kind), func(t *testing.T) {
			h, err := hostexec.SSMHost(kind, ssmHostTarget, ssmHostCredential, ssmHostRegion, ssmHostEnv())
			if err == nil {
				t.Fatalf("SSMHost(%q) returned no error; only %q is driven over ssm", kind, hostexec.KindSSM)
			}
			if !errors.Is(err, hostexec.ErrNotSSM) {
				t.Errorf("SSMHost(%q) error = %v, want one that errors.Is hostexec.ErrNotSSM", kind, err)
			}
			if !reflect.DeepEqual(h, hostexec.Host{}) {
				t.Errorf("SSMHost(%q) refused but returned a non-zero Host %+v", kind, h)
			}
		})
	}
}

func TestSSMHost_RefusalCarriesNoPartOfTheHost(t *testing.T) {
	const kindSentinel = "kind-sentinel-ssmhost"
	env := ssmHostEnv()
	for _, kind := range []hostexec.Kind{hostexec.KindSSH, "", kindSentinel} {
		t.Run(string(kind), func(t *testing.T) {
			_, err := hostexec.SSMHost(kind, ssmHostTarget, ssmHostCredential, ssmHostRegion, env)
			if err == nil {
				t.Fatalf("SSMHost(%q) returned no error", kind)
			}
			msg := err.Error()
			leaks := append([]string{ssmHostCredential, ssmHostTarget, ssmHostRegion, kindSentinel}, env...)
			for _, s := range leaks {
				if strings.Contains(msg, s) {
					t.Errorf("the refusal %q carries %q from the host it refused", msg, s)
				}
			}
		})
	}
}

func TestSSMHost_CarriesTheCredentialWhenGiven(t *testing.T) {
	h, err := hostexec.SSMHost(hostexec.KindSSM, ssmHostTarget, ssmHostCredential, ssmHostRegion, ssmHostEnv())
	if err != nil {
		t.Fatalf("SSMHost(aws-ssm): %v", err)
	}
	if h.Credential != ssmHostCredential {
		t.Errorf("Host.Credential = %q, want the credential the caller gave, %q", h.Credential, ssmHostCredential)
	}
}

func TestSSMHost_LeavesTheCredentialEmptyWhenNotGiven(t *testing.T) {
	h, err := hostexec.SSMHost(hostexec.KindSSM, ssmHostTarget, "", ssmHostRegion, ssmHostEnv())
	if err != nil {
		t.Fatalf("SSMHost(aws-ssm): %v", err)
	}
	if h.Credential != "" {
		t.Errorf("Host.Credential = %q, want it empty when the caller gave none", h.Credential)
	}
}

func TestSSMHost_CarriesTargetRegionAndEnvIntoTheHost(t *testing.T) {
	for _, tc := range []struct {
		name       string
		credential string
		env        []string
	}{
		{"with credential and env", ssmHostCredential, ssmHostEnv()},
		{"without credential", "", ssmHostEnv()},
		{"nil env", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := hostexec.SSMHost(hostexec.KindSSM, ssmHostTarget, tc.credential, ssmHostRegion, tc.env)
			if err != nil {
				t.Fatalf("SSMHost(aws-ssm): %v", err)
			}
			want := hostexec.Host{Kind: hostexec.KindSSM, Target: ssmHostTarget, Credential: tc.credential, Region: ssmHostRegion, Env: tc.env}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("SSMHost = %+v, want %+v", got, want)
			}
		})
	}
}

func TestSSMHost_LeavesTargetAndRegionToRun(t *testing.T) {
	h, err := hostexec.SSMHost(hostexec.KindSSM, "", "", "", nil)
	if err != nil {
		t.Fatalf("SSMHost(aws-ssm) with no target or region = %v, want nil; Run judges the target and region", err)
	}
	if !reflect.DeepEqual(h, hostexec.Host{Kind: hostexec.KindSSM}) {
		t.Errorf("SSMHost = %+v, want only the aws-ssm kind set", h)
	}
	if err := hostexec.Run(context.Background(), h, "true"); err == nil {
		t.Error("Run accepted an ssm host with no target")
	}
}
