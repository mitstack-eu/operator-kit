package registryauth_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/kittest"
	"github.com/mitstack-eu/operator-kit/v3/registryauth"
)

const (
	ecrHost           = "123456789012.dkr.ecr.eu-central-1.amazonaws.com"
	leakOCICredential = "REGISTRY-PASSWORD-THAT-MUST-NEVER-SURFACE"
	leakECRPassword   = "ECR-LOGIN-PASSWORD-THAT-MUST-NEVER-SURFACE"
)

// call is what a stub CLI recorded: its argv split into fields and its stdin, or nil when it never ran.
type call struct {
	argv  []string
	stdin string
}

// fakeRegistryBins puts recording stubs for aws and docker first on PATH, so a test can assert a credential travelled over stdin and not merely that it was absent from argv.
func fakeRegistryBins(t *testing.T, awsScript, dockerScript string) (awsRecord, dockerRecord string) {
	t.Helper()
	dir := t.TempDir()
	awsRecord, dockerRecord = filepath.Join(dir, "aws.record"), filepath.Join(dir, "docker.record")
	for path, stub := range map[string][2]string{"aws": {awsRecord, awsScript}, "docker": {dockerRecord, dockerScript}} {
		body := "#!/usr/bin/env bash\nprintf 'ARGV:%s\\n' \"$*\" >> " + stub[0] + "\nIN=\"$(cat)\"\nprintf 'STDIN:%s\\n' \"$IN\" >> " + stub[0] + "\n" + stub[1] + "\n"
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", path, err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return awsRecord, dockerRecord
}

// recorded reads a stub's record; a stub only creates it on first run, so a missing file means never called.
func recorded(t *testing.T, record string) *call {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		return nil
	}
	c := &call{}
	for _, line := range strings.Split(string(data), "\n") {
		if argv, ok := strings.CutPrefix(line, "ARGV:"); ok {
			c.argv = strings.Fields(argv)
		}
		if stdin, ok := strings.CutPrefix(line, "STDIN:"); ok {
			c.stdin = strings.TrimSpace(stdin)
		}
	}
	return c
}

func ecrRegistry(host, region string) registryauth.Registry {
	return registryauth.Registry{Kind: registryauth.KindECR, Host: host, Region: region}
}

func ociRegistry(host, user, cred string) registryauth.Registry {
	return registryauth.Registry{Kind: registryauth.KindOCIBasic, Host: host, Username: user, Credential: cred}
}

func awsEmits(password string) string  { return "echo '" + password + "'" }
func awsFails(stderr string) string    { return "echo \"" + stderr + "\" >&2\nexit 254" }
func dockerFails(stderr string) string { return "echo \"" + stderr + "\" >&2\nexit 1" }
func withCredential(r registryauth.Registry, c string) registryauth.Registry {
	r.Credential = c
	return r
}

const dockerOK = "exit 0"

// Every row also checks that no credential reaches an argv, a returned error or the log.
func TestLogin(t *testing.T) {
	ecrAWS := []string{"ecr", "get-login-password", "--region", "eu-central-1"}
	dash := "-oProxyCommand=touch /tmp/pwned"
	cases := []struct {
		name                    string
		registry                registryauth.Registry
		awsScript, dockerScript string
		cancelled               bool
		wantErr                 bool
		wantInErr               string
		wantAWS, wantDocker     []string
		wantStdin               string
	}{
		{"an ECR login pipes the minted password into docker login --password-stdin as AWS", ecrRegistry(ecrHost, "eu-central-1"), awsEmits(leakECRPassword), dockerOK, false, false, "",
			ecrAWS, []string{"login", "--username", "AWS", "--password-stdin", ecrHost}, leakECRPassword},
		{"an OCI login pipes the credential into docker login --password-stdin as its user", ociRegistry("ghcr.io", "deploy-bot", leakOCICredential), "", dockerOK, false, false, "",
			nil, []string{"login", "--username", "deploy-bot", "--password-stdin", "ghcr.io"}, leakOCICredential},
		{"a failed get-login-password surfaces its stderr and never reaches docker login", ecrRegistry(ecrHost, "eu-central-1"), awsFails("An error occurred (AccessDenied) when calling GetAuthorizationToken"), dockerOK, false, true, "AccessDenied",
			ecrAWS, nil, ""},
		{"a failed ECR docker login surfaces its stderr", ecrRegistry(ecrHost, "eu-central-1"), awsEmits(leakECRPassword), dockerFails("denied: requested access to the resource is denied"), false, true, "denied",
			ecrAWS, []string{"login", "--username", "AWS", "--password-stdin", ecrHost}, leakECRPassword},
		{"a failed OCI docker login surfaces its stderr", ociRegistry("ghcr.io", "deploy-bot", leakOCICredential), "", dockerFails("denied: requested access to the resource is denied"), false, true, "denied",
			nil, []string{"login", "--username", "deploy-bot", "--password-stdin", "ghcr.io"}, leakOCICredential},
		{"an empty minted password never reaches docker login", ecrRegistry(ecrHost, "eu-central-1"), awsEmits(""), dockerOK, false, true, "", ecrAWS, nil, ""},
		{"a whitespace-only minted password never reaches docker login", ecrRegistry(ecrHost, "eu-central-1"), awsEmits("   "), dockerOK, false, true, "", ecrAWS, nil, ""},
		{"an unrecognised kind is named and runs nothing", registryauth.Registry{Kind: registryauth.Kind("s3"), Host: "example.com"}, awsEmits("pw"), dockerOK, false, true, "s3", nil, nil, ""},
		{"an empty kind is named and runs nothing", registryauth.Registry{Kind: registryauth.Kind(""), Host: "example.com"}, awsEmits("pw"), dockerOK, false, true, `""`, nil, nil, ""},
		{"an ECR registry with no region runs nothing", ecrRegistry(ecrHost, ""), awsEmits("pw"), dockerOK, false, true, "", nil, nil, ""},
		{"an ECR registry given a credential is refused rather than ignored", withCredential(ecrRegistry(ecrHost, "eu-central-1"), "should-not-be-set-for-ecr"), awsEmits("pw"), dockerOK, false, true, "", nil, nil, ""},
		{"an OCI registry with no username runs nothing", ociRegistry("ghcr.io", "", "the-credential"), "", dockerOK, false, true, "", nil, nil, ""},
		{"an OCI registry with no credential runs nothing", ociRegistry("ghcr.io", "deploy-bot", ""), "", dockerOK, false, true, "", nil, nil, ""},
		{"an ECR registry with no host runs nothing", ecrRegistry("", "eu-central-1"), awsEmits("pw"), dockerOK, false, true, "", nil, nil, ""},
		{"an OCI registry with no host runs nothing", ociRegistry("", "u", "c"), awsEmits("pw"), dockerOK, false, true, "", nil, nil, ""},
		{"an ECR host beginning with a dash, which docker would read as an option, runs nothing", ecrRegistry(dash, "eu-central-1"), awsEmits("pw"), dockerOK, false, true, "", nil, nil, ""},
		{"an OCI host beginning with a dash runs nothing", ociRegistry(dash, "u", "c"), awsEmits("pw"), dockerOK, false, true, "", nil, nil, ""},
		{"an ECR login under a cancelled context runs nothing", ecrRegistry(ecrHost, "eu-central-1"), awsEmits("pw"), dockerOK, true, true, "", nil, nil, ""},
		{"an OCI login under a cancelled context runs nothing", ociRegistry("ghcr.io", "deploy-bot", "the-credential"), awsEmits("pw"), dockerOK, true, true, "", nil, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			awsRecord, dockerRecord := fakeRegistryBins(t, tc.awsScript, tc.dockerScript)
			ctx, cancel := context.WithCancel(context.Background())
			if tc.cancelled {
				cancel()
			}
			defer cancel()
			var logs bytes.Buffer
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })

			err := registryauth.Login(ctx, tc.registry)

			if (err != nil) != tc.wantErr || (err != nil && !strings.Contains(err.Error(), tc.wantInErr)) {
				t.Errorf("Login(%+v) = %v; want error %v naming %q", tc.registry, err, tc.wantErr, tc.wantInErr)
			}
			aws, docker := recorded(t, awsRecord), recorded(t, dockerRecord)
			if got := argvOf(aws); !slices.Equal(got, tc.wantAWS) || (aws != nil) != (tc.wantAWS != nil) {
				t.Errorf("aws argv = %q, want %q (nil: never run)", got, tc.wantAWS)
			}
			if got := argvOf(docker); !slices.Equal(got, tc.wantDocker) || (docker != nil) != (tc.wantDocker != nil) {
				t.Errorf("docker argv = %q, want %q (nil: never run)", got, tc.wantDocker)
			}
			if docker != nil && docker.stdin != tc.wantStdin {
				t.Errorf("docker login stdin = %q, want %q", docker.stdin, tc.wantStdin)
			}
			for _, c := range []*call{aws, docker} {
				kittest.AssertNoSecret(t, "a recorded argv", strings.Join(argvOf(c), " "), leakOCICredential, leakECRPassword)
			}
			if err != nil {
				kittest.AssertNoSecret(t, "the Login error", err.Error(), leakOCICredential, leakECRPassword)
			}
			kittest.AssertNoSecret(t, "the log", logs.String(), leakOCICredential, leakECRPassword)
		})
	}
}

func argvOf(c *call) []string {
	if c == nil {
		return nil
	}
	return c.argv
}

// A context cancelled while a CLI runs surfaces as context.Canceled; the select is bounded so a regression fails rather than hangs.
func TestLogin_ACancellationMidSubprocessIsAttributedToTheContext(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		registry                registryauth.Registry
		awsScript, dockerScript string
	}{
		{"mid get-login-password", ecrRegistry(ecrHost, "eu-central-1"), "sleep 5", dockerOK},
		{"mid an ECR docker login", ecrRegistry(ecrHost, "eu-central-1"), awsEmits("pw"), "sleep 5"},
		{"mid an OCI docker login", ociRegistry("ghcr.io", "deploy-bot", "the-credential"), "", "sleep 5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeRegistryBins(t, tc.awsScript, tc.dockerScript)
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(50*time.Millisecond, cancel)
			done := make(chan error, 1)

			go func() { done <- registryauth.Login(ctx, tc.registry) }()

			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("Login = %v, want it to wrap context.Canceled rather than a bare subprocess failure", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Login did not return promptly after its context was cancelled, so the subprocess was not killed")
			}
		})
	}
}
