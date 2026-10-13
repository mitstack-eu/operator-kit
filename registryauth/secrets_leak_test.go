package registryauth_test

// A credential may reach the registry CLI it drives but must never surface in argv or a returned error.

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
	"github.com/mitstack-eu/operator-kit/v2/registryauth"
)

const (
	leakOCICredential = "REGISTRY-PASSWORD-THAT-MUST-NEVER-SURFACE"
	leakECRPassword   = "ECR-LOGIN-PASSWORD-THAT-MUST-NEVER-SURFACE"
)

func TestNoSecretLeak_OCIBasicCredentialNeverReachesTheCommandLine(t *testing.T) {
	_, dockerRecord := fakeRegistryBins(t, "", dockerOK())

	r := ociRegistry("ghcr.io", "deploy-bot", leakOCICredential)
	if err := registryauth.Login(context.Background(), r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	kittest.AssertNoSecret(t, "docker login argv", recordedArgv(t, dockerRecord), leakOCICredential)

	stdin := recordedStdin(t, dockerRecord)
	if !strings.Contains(stdin, leakOCICredential) {
		t.Errorf("docker login stdin = %q, want it to carry the credential — that is the whole point of --password-stdin", stdin)
	}
}

func TestNoSecretLeak_ECRPasswordNeverReachesTheCommandLine(t *testing.T) {
	_, dockerRecord := fakeRegistryBins(t, awsEmits(leakECRPassword), dockerOK())

	r := ecrRegistry("123456789012.dkr.ecr.eu-central-1.amazonaws.com", "eu-central-1")
	if err := registryauth.Login(context.Background(), r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	kittest.AssertNoSecret(t, "docker login argv", recordedArgv(t, dockerRecord), leakECRPassword)

	stdin := recordedStdin(t, dockerRecord)
	if !strings.Contains(stdin, leakECRPassword) {
		t.Errorf("docker login stdin = %q, want the AWS-minted password piped through --password-stdin", stdin)
	}
}

func TestNoSecretLeak_OCIBasicFailureErrorCarriesNoCredential(t *testing.T) {
	fakeRegistryBins(t, "", dockerFails("denied: authentication required"))

	r := ociRegistry("ghcr.io", "deploy-bot", leakOCICredential)
	err := registryauth.Login(context.Background(), r)
	if err == nil {
		t.Fatal("expected an error from a failing docker login")
	}
	kittest.AssertNoSecret(t, "Login error", err.Error(), leakOCICredential)
}

func TestNoSecretLeak_ECRFailureErrorCarriesNoPassword(t *testing.T) {
	fakeRegistryBins(t, awsEmits(leakECRPassword), dockerFails("denied: authentication required"))

	r := ecrRegistry("123456789012.dkr.ecr.eu-central-1.amazonaws.com", "eu-central-1")
	err := registryauth.Login(context.Background(), r)
	if err == nil {
		t.Fatal("expected an error from a failing docker login")
	}
	kittest.AssertNoSecret(t, "Login error", err.Error(), leakECRPassword)
}

func TestNoSecretLeak_CredentialNeverAppearsInLogOutput(t *testing.T) {
	cases := map[string]struct {
		registry  registryauth.Registry
		awsScript string
		sentinel  string
	}{
		"ecr":       {ecrRegistry("123456789012.dkr.ecr.eu-central-1.amazonaws.com", "eu-central-1"), awsEmits(leakECRPassword), leakECRPassword},
		"oci-basic": {ociRegistry("ghcr.io", "deploy-bot", leakOCICredential), "", leakOCICredential},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fakeRegistryBins(t, c.awsScript, dockerOK())

			var buf bytes.Buffer
			log.SetOutput(&buf)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })

			if err := registryauth.Login(context.Background(), c.registry); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if strings.Contains(buf.String(), c.sentinel) {
				t.Errorf("LEAK: the credential surfaced in log output: %s", buf.String())
			}
		})
	}
}

func TestNoSecretLeak_PasswordFlagNeverAppearsInAnyRecordedCommandLine(t *testing.T) {
	// --password-stdin is the only safe form, since --password puts the credential on the command line.
	cases := map[string]registryauth.Registry{
		"ecr":       ecrRegistry("123456789012.dkr.ecr.eu-central-1.amazonaws.com", "eu-central-1"),
		"oci-basic": ociRegistry("ghcr.io", "deploy-bot", leakOCICredential),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			awsRecord, dockerRecord := fakeRegistryBins(t, awsEmits(leakECRPassword), dockerOK())

			if err := registryauth.Login(context.Background(), r); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, argv := range []string{recordedArgv(t, dockerRecord), argvIfInvoked(awsRecord)} {
				if strings.Contains(argv, "--password ") || strings.HasSuffix(strings.TrimSpace(argv), "--password") {
					t.Errorf("argv %q used the unsafe --password flag", argv)
				}
			}
			if !strings.Contains(recordedArgv(t, dockerRecord), "--password-stdin") {
				t.Errorf("docker login argv = %q, want --password-stdin so the credential travels over stdin, never argv", recordedArgv(t, dockerRecord))
			}
		})
	}
}

func argvIfInvoked(record string) string {
	if !invoked(record) {
		return ""
	}
	data, err := os.ReadFile(record)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "ARGV:") {
			return strings.TrimPrefix(line, "ARGV:")
		}
	}
	return ""
}
