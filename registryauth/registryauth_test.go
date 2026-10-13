package registryauth_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/registryauth"
)

// fakeRegistryBins puts recording stubs for aws and docker first on PATH, so a test can assert a credential travelled over stdin and not merely that it was absent from argv.
func fakeRegistryBins(t *testing.T, awsScript, dockerScript string) (awsRecord, dockerRecord string) {
	t.Helper()
	dir := t.TempDir()
	awsRecord = filepath.Join(dir, "aws.record")
	dockerRecord = filepath.Join(dir, "docker.record")
	writeStub(t, filepath.Join(dir, "aws"), awsRecord, awsScript)
	writeStub(t, filepath.Join(dir, "docker"), dockerRecord, dockerScript)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return awsRecord, dockerRecord
}

func writeStub(t *testing.T, path, record, script string) {
	t.Helper()
	body := "#!/usr/bin/env bash\n" +
		"printf 'ARGV:%s\\n' \"$*\" >> " + record + "\n" +
		"IN=\"$(cat)\"\n" +
		"printf 'STDIN:%s\\n' \"$IN\" >> " + record + "\n" +
		script + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", filepath.Base(path), err)
	}
}

func recordedArgv(t *testing.T, record string) string {
	t.Helper()
	return fieldFromRecord(t, record, "ARGV:")
}

func recordedStdin(t *testing.T, record string) string {
	t.Helper()
	return fieldFromRecord(t, record, "STDIN:")
}

func fieldFromRecord(t *testing.T, record, prefix string) string {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("reading record %s: %v", record, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	t.Fatalf("record %s has no %s line:\n%s", record, prefix, data)
	return ""
}

// A stub only creates its record file on first invocation, so a missing file means never called.
func invoked(record string) bool {
	_, err := os.ReadFile(record)
	return err == nil
}

func ecrRegistry(host, region string) registryauth.Registry {
	return registryauth.Registry{Kind: registryauth.KindECR, Host: host, Region: region}
}

func ociRegistry(host, user, cred string) registryauth.Registry {
	return registryauth.Registry{Kind: registryauth.KindOCIBasic, Host: host, Username: user, Credential: cred}
}

func awsEmits(password string) string { return "echo '" + password + "'" }
func awsFails(stderr string) string   { return "echo \"" + stderr + "\" >&2\nexit 254" }
func dockerOK() string                { return "exit 0" }
func dockerFails(stderr string) string {
	return "echo \"" + stderr + "\" >&2\nexit 1"
}

func TestLogin_UnknownKindNamesTheKindInTheError(t *testing.T) {
	err := registryauth.Login(context.Background(), registryauth.Registry{Kind: registryauth.Kind("s3"), Host: "example.com"})
	if err == nil {
		t.Fatal("Login with an unrecognised Kind must fail rather than fall back to a default driver")
	}
	if !strings.Contains(err.Error(), "s3") {
		t.Errorf("error = %q, want it to name the unrecognised kind %q", err.Error(), "s3")
	}
}

func TestLogin_EmptyKindNamesTheKindInTheError(t *testing.T) {
	err := registryauth.Login(context.Background(), registryauth.Registry{Kind: registryauth.Kind(""), Host: "example.com"})
	if err == nil {
		t.Fatal("Login with an empty Kind must fail rather than silently pick a default driver")
	}
	if !strings.Contains(err.Error(), `""`) {
		t.Errorf(`error = %q, want it to name the empty kind (e.g. contain %q)`, err.Error(), `""`)
	}
}

func TestLogin_UnknownKindNeverInvokesASubprocess(t *testing.T) {
	awsRecord, dockerRecord := fakeRegistryBins(t, awsEmits("pw"), dockerOK())
	_ = registryauth.Login(context.Background(), registryauth.Registry{Kind: registryauth.Kind("s3"), Host: "example.com"})
	if invoked(awsRecord) || invoked(dockerRecord) {
		t.Error("Login ran a subprocess for a kind it cannot recognise")
	}
}

func TestLogin_ECR_InvokesAWSGetLoginPasswordThenDockerLoginWithHost(t *testing.T) {
	awsRecord, dockerRecord := fakeRegistryBins(t, awsEmits("ecr-password"), dockerOK())

	host := "123456789012.dkr.ecr.eu-central-1.amazonaws.com"
	r := ecrRegistry(host, "eu-central-1")
	if err := registryauth.Login(context.Background(), r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotAWS := strings.Fields(recordedArgv(t, awsRecord))
	wantAWS := []string{"ecr", "get-login-password", "--region", "eu-central-1"}
	if !fieldsEqual(gotAWS, wantAWS) {
		t.Errorf("aws argv = %v, want %v", gotAWS, wantAWS)
	}

	gotDocker := strings.Fields(recordedArgv(t, dockerRecord))
	wantDocker := []string{"login", "--username", "AWS", "--password-stdin", host}
	if !fieldsEqual(gotDocker, wantDocker) {
		t.Errorf("docker argv = %v, want %v", gotDocker, wantDocker)
	}
}

func TestLogin_ECR_PipesTheFetchedPasswordIntoDockerLoginStdin(t *testing.T) {
	_, dockerRecord := fakeRegistryBins(t, awsEmits("the-fetched-password"), dockerOK())

	r := ecrRegistry("123456789012.dkr.ecr.eu-central-1.amazonaws.com", "eu-central-1")
	if err := registryauth.Login(context.Background(), r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stdin := strings.TrimSpace(recordedStdin(t, dockerRecord))
	if stdin != "the-fetched-password" {
		t.Errorf("docker login stdin = %q, want the password aws emitted", stdin)
	}
}

func TestLogin_ECR_AbortsBeforeDockerLoginWhenGetLoginPasswordFails(t *testing.T) {
	_, dockerRecord := fakeRegistryBins(t, awsFails("An error occurred (AccessDenied) when calling GetAuthorizationToken"), dockerOK())

	r := ecrRegistry("123456789012.dkr.ecr.eu-central-1.amazonaws.com", "eu-central-1")
	err := registryauth.Login(context.Background(), r)
	if err == nil {
		t.Fatal("expected an error when aws ecr get-login-password fails")
	}
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("error = %v, want the captured aws stderr surfaced", err)
	}
	if invoked(dockerRecord) {
		t.Error("docker login ran despite aws ecr get-login-password failing — a stale or empty password must never reach it")
	}
}

func TestLogin_ECR_ReturnsErrorWhenDockerLoginFails(t *testing.T) {
	fakeRegistryBinsAndAssertDockerFailureSurfaces(t, ecrRegistry("123456789012.dkr.ecr.eu-central-1.amazonaws.com", "eu-central-1"), awsEmits("pw"))
}

func TestLogin_ECR_RejectsEmptyRegion(t *testing.T) {
	awsRecord, dockerRecord := fakeRegistryBins(t, awsEmits("pw"), dockerOK())
	r := ecrRegistry("123456789012.dkr.ecr.eu-central-1.amazonaws.com", "")
	if err := registryauth.Login(context.Background(), r); err == nil {
		t.Error("Login for an ecr registry with no Region must fail rather than call the AWS CLI with a blank --region")
	}
	if invoked(awsRecord) || invoked(dockerRecord) {
		t.Error("Login ran a subprocess despite a missing Region")
	}
}

func TestLogin_ECR_RejectsANonEmptyCredential(t *testing.T) {
	// A non-empty Credential must be refused rather than silently ignored, which would hide a misconfiguration.
	awsRecord, dockerRecord := fakeRegistryBins(t, awsEmits("pw"), dockerOK())
	r := ecrRegistry("123456789012.dkr.ecr.eu-central-1.amazonaws.com", "eu-central-1")
	r.Credential = "should-not-be-set-for-ecr"
	if err := registryauth.Login(context.Background(), r); err == nil {
		t.Error("Login for an ecr registry with a non-empty Credential must fail rather than ignore it")
	}
	if invoked(awsRecord) || invoked(dockerRecord) {
		t.Error("Login ran a subprocess despite an invalid ecr Credential")
	}
}

func TestLogin_ECR_RejectsAnEmptyMintedPassword(t *testing.T) {
	// An empty minted password must be rejected rather than run docker login with nothing on stdin.
	_, dockerRecord := fakeRegistryBins(t, awsEmits(""), dockerOK())
	r := ecrRegistry("123456789012.dkr.ecr.eu-central-1.amazonaws.com", "eu-central-1")
	if err := registryauth.Login(context.Background(), r); err == nil {
		t.Error("Login must fail when aws ecr get-login-password mints an empty password rather than running docker login with nothing on stdin")
	}
	if invoked(dockerRecord) {
		t.Error("docker login ran despite an empty minted password")
	}
}

func TestLogin_ECR_RejectsAWhitespaceOnlyMintedPassword(t *testing.T) {
	_, dockerRecord := fakeRegistryBins(t, awsEmits("   "), dockerOK())
	r := ecrRegistry("123456789012.dkr.ecr.eu-central-1.amazonaws.com", "eu-central-1")
	if err := registryauth.Login(context.Background(), r); err == nil {
		t.Error("Login must fail when aws ecr get-login-password mints a whitespace-only password rather than treating it as usable")
	}
	if invoked(dockerRecord) {
		t.Error("docker login ran despite a whitespace-only minted password")
	}
}

func TestLogin_OCIBasic_InvokesDockerLoginWithUsernameAndPasswordStdin(t *testing.T) {
	_, dockerRecord := fakeRegistryBins(t, "", dockerOK())

	r := ociRegistry("ghcr.io", "deploy-bot", "the-credential")
	if err := registryauth.Login(context.Background(), r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotArgv := strings.Fields(recordedArgv(t, dockerRecord))
	wantArgv := []string{"login", "--username", "deploy-bot", "--password-stdin", "ghcr.io"}
	if !fieldsEqual(gotArgv, wantArgv) {
		t.Errorf("docker argv = %v, want %v", gotArgv, wantArgv)
	}

	stdin := strings.TrimSpace(recordedStdin(t, dockerRecord))
	if stdin != "the-credential" {
		t.Errorf("docker login stdin = %q, want the configured Credential", stdin)
	}
}

func TestLogin_OCIBasic_ReturnsErrorWhenDockerLoginFails(t *testing.T) {
	fakeRegistryBinsAndAssertDockerFailureSurfaces(t, ociRegistry("ghcr.io", "deploy-bot", "the-credential"), "")
}

func TestLogin_OCIBasic_RejectsEmptyUsername(t *testing.T) {
	_, dockerRecord := fakeRegistryBins(t, "", dockerOK())
	r := ociRegistry("ghcr.io", "", "the-credential")
	if err := registryauth.Login(context.Background(), r); err == nil {
		t.Error("Login for an oci-basic registry with no Username must fail rather than call docker login with a blank --username")
	}
	if invoked(dockerRecord) {
		t.Error("Login ran docker login despite a missing Username")
	}
}

func TestLogin_OCIBasic_RejectsEmptyCredential(t *testing.T) {
	_, dockerRecord := fakeRegistryBins(t, "", dockerOK())
	r := ociRegistry("ghcr.io", "deploy-bot", "")
	if err := registryauth.Login(context.Background(), r); err == nil {
		t.Error("Login for an oci-basic registry with no Credential must fail rather than run docker login with nothing on stdin")
	}
	if invoked(dockerRecord) {
		t.Error("Login ran docker login despite a missing Credential")
	}
}

func TestLogin_RejectsEmptyHost(t *testing.T) {
	cases := map[string]registryauth.Registry{
		"ecr":       {Kind: registryauth.KindECR, Host: "", Region: "eu-central-1"},
		"oci-basic": {Kind: registryauth.KindOCIBasic, Host: "", Username: "u", Credential: "c"},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			awsRecord, dockerRecord := fakeRegistryBins(t, awsEmits("pw"), dockerOK())
			if err := registryauth.Login(context.Background(), r); err == nil {
				t.Error("Login with an empty Host must fail rather than pass an empty final argument to the CLI")
			}
			if invoked(awsRecord) || invoked(dockerRecord) {
				t.Error("Login ran a subprocess despite a missing Host")
			}
		})
	}
}

func TestLogin_HonoursContextCancellation(t *testing.T) {
	cases := map[string]registryauth.Registry{
		"ecr":       ecrRegistry("123456789012.dkr.ecr.eu-central-1.amazonaws.com", "eu-central-1"),
		"oci-basic": ociRegistry("ghcr.io", "deploy-bot", "the-credential"),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			awsRecord, dockerRecord := fakeRegistryBins(t, awsEmits("pw"), dockerOK())

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			if err := registryauth.Login(ctx, r); err == nil {
				t.Error("Login with an already-cancelled context must fail rather than proceed")
			}
			if invoked(awsRecord) || invoked(dockerRecord) {
				t.Error("Login ran a subprocess despite a cancelled context")
			}
		})
	}
}

func fakeRegistryBinsAndAssertDockerFailureSurfaces(t *testing.T, r registryauth.Registry, awsScript string) {
	t.Helper()
	_, _ = fakeRegistryBins(t, awsScript, dockerFails("denied: requested access to the resource is denied"))
	err := registryauth.Login(context.Background(), r)
	if err == nil {
		t.Fatal("expected an error when docker login fails")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("error = %v, want the captured docker stderr surfaced", err)
	}
}

func fieldsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
