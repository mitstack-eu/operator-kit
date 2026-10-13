package registryauth_test

// docker login has no end-of-options marker, so a Host beginning with "-" must be rejected up front.

import (
	"context"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/registryauth"
)

func TestLogin_RejectsAHostBeginningWithADashBeforeSpawningAnySubprocess(t *testing.T) {
	cases := map[string]registryauth.Registry{
		"ecr":       {Kind: registryauth.KindECR, Host: "-oProxyCommand=touch /tmp/pwned", Region: "eu-central-1"},
		"oci-basic": {Kind: registryauth.KindOCIBasic, Host: "-oProxyCommand=touch /tmp/pwned", Username: "u", Credential: "c"},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			awsRecord, dockerRecord := fakeRegistryBins(t, awsEmits("pw"), dockerOK())
			if err := registryauth.Login(context.Background(), r); err == nil {
				t.Error("Login must reject a Host beginning with \"-\" before spawning any subprocess")
			}
			if invoked(awsRecord) || invoked(dockerRecord) {
				t.Error("Login ran a subprocess despite a Host beginning with \"-\"")
			}
		})
	}
}
