package cloudid_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/cloudid"
)

func TestProviderARN_IsConstructedNotDiscovered(t *testing.T) {
	got, err := cloudid.ProviderARN("123456789012", "https://api.mitstack.dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "arn:aws:iam::123456789012:oidc-provider/api.mitstack.dev"
	if got != want {
		t.Errorf("ProviderARN = %q, want %q", got, want)
	}
}

func TestProviderARN_AcceptsIssuerWithoutScheme(t *testing.T) {
	got, err := cloudid.ProviderARN("123456789012", "api.mitstack.dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(got, "oidc-provider/api.mitstack.dev") {
		t.Errorf("ProviderARN = %q", got)
	}
}

func TestProviderARN_StripsTrailingSlashAndPreservesPath(t *testing.T) {
	got, err := cloudid.ProviderARN("123456789012", "https://issuer.example.com/tenant/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "arn:aws:iam::123456789012:oidc-provider/issuer.example.com/tenant" {
		t.Errorf("ProviderARN = %q", got)
	}
}

func TestProviderARN_RejectsIncompleteInput(t *testing.T) {
	for _, c := range []struct{ account, issuer string }{
		{"", "https://api.mitstack.dev"},
		{"123456789012", ""},
		{"not-an-account", "https://api.mitstack.dev"},
	} {
		if _, err := cloudid.ProviderARN(c.account, c.issuer); err == nil {
			t.Errorf("ProviderARN(%q, %q) succeeded; want an error rather than a malformed ARN", c.account, c.issuer)
		}
	}
}

// fakeAWS stubs the aws CLI and records its argv and env, to prove the token never reaches a command line.
func fakeAWS(t *testing.T, script string) (bin string, record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "record")
	full := "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >> " + record + "\nprintf 'ROLE=%s TOKENFILE=%s SESSION=%s REGION=%s\\n' \"$AWS_ROLE_ARN\" \"$AWS_WEB_IDENTITY_TOKEN_FILE\" \"$AWS_ROLE_SESSION_NAME\" \"$AWS_REGION\" >> " + record + "\n" + script + "\n"
	bin = filepath.Join(dir, "aws")
	if err := os.WriteFile(bin, []byte(full), 0o755); err != nil {
		t.Fatalf("write fake aws: %v", err)
	}
	return dir, record
}

func identity() cloudid.Identity {
	return cloudid.Identity{
		RoleARN:          "arn:aws:iam::123456789012:role/mitstack-app",
		WebIdentityToken: "SUPER-SECRET-WEB-IDENTITY-TOKEN",
		Region:           "eu-central-1",
		SessionSuffix:    "app-42",
	}
}

func TestResolve_ReturnsTemporaryCredentials(t *testing.T) {
	dir, _ := fakeAWS(t, `echo '{"AccessKeyId":"AKIATEST","SecretAccessKey":"shh","SessionToken":"tok","Expiration":"2099-01-01T00:00:00Z"}'`)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	creds, err := cloudid.Resolve(context.Background(), identity())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.AccessKeyID != "AKIATEST" || creds.SecretAccessKey != "shh" || creds.SessionToken != "tok" {
		t.Errorf("credentials = %+v", creds)
	}
	if creds.Region != "eu-central-1" {
		t.Errorf("Region = %q, want the identity's region", creds.Region)
	}
	if creds.Expires.IsZero() {
		t.Error("Expires must be populated — the Refresher cannot renew ahead of an unknown expiry")
	}
}

func TestResolve_NeverPutsTheTokenOnACommandLine(t *testing.T) {
	// The token travels by file so it stays invisible in process listings.
	dir, record := fakeAWS(t, `echo '{"AccessKeyId":"A","SecretAccessKey":"B","SessionToken":"C","Expiration":"2099-01-01T00:00:00Z"}'`)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	if _, err := cloudid.Resolve(context.Background(), identity()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("reading fake aws record: %v", err)
	}
	if strings.Contains(string(rec), "SUPER-SECRET-WEB-IDENTITY-TOKEN") {
		t.Errorf("the web-identity token reached the aws argv/env directly:\n%s", rec)
	}
	if !strings.Contains(string(rec), "TOKENFILE=/") {
		t.Errorf("expected AWS_WEB_IDENTITY_TOKEN_FILE to be set to a path, got:\n%s", rec)
	}
}

func TestResolve_RemovesTheTokenFileAfterwards(t *testing.T) {
	dir, record := fakeAWS(t, `echo '{"AccessKeyId":"A","SecretAccessKey":"B","SessionToken":"C","Expiration":"2099-01-01T00:00:00Z"}'`)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	if _, err := cloudid.Resolve(context.Background(), identity()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec, _ := os.ReadFile(record)
	var tokenFile string
	for _, line := range strings.Split(string(rec), "\n") {
		if i := strings.Index(line, "TOKENFILE="); i >= 0 {
			tokenFile = strings.Fields(line[i+len("TOKENFILE="):])[0]
		}
	}
	if tokenFile == "" {
		t.Fatal("could not determine the token file path from the fake aws record")
	}
	if _, err := os.Stat(tokenFile); !os.IsNotExist(err) {
		t.Errorf("token file %s still exists after Resolve — a credential left on disk", tokenFile)
	}
}

func TestResolve_SessionNameCarriesTheSuffix(t *testing.T) {
	dir, record := fakeAWS(t, `echo '{"AccessKeyId":"A","SecretAccessKey":"B","SessionToken":"C","Expiration":"2099-01-01T00:00:00Z"}'`)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	if _, err := cloudid.Resolve(context.Background(), identity()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec, _ := os.ReadFile(record)
	if !strings.Contains(string(rec), "SESSION=mitstack-app-42") {
		t.Errorf("session name should identify the subscription for CloudTrail attribution, got:\n%s", rec)
	}
}

func TestResolve_SurfacesTheAWSErrorNotABareFailure(t *testing.T) {
	// The CLI's stderr carries the error code and role ARN and never the token, so it is safe to propagate.
	dir, _ := fakeAWS(t, `echo "An error occurred (AccessDenied) when calling AssumeRoleWithWebIdentity" >&2; exit 254`)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	_, err := cloudid.Resolve(context.Background(), identity())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("error = %v, want the real AWS failure surfaced", err)
	}
	if strings.Contains(err.Error(), "SUPER-SECRET-WEB-IDENTITY-TOKEN") {
		t.Error("the error leaked the web-identity token")
	}
}

func TestResolve_RequiresARegion(t *testing.T) {
	// A package default could shift under an existing app, so the caller must pass the pinned region.
	id := identity()
	id.Region = ""
	if _, err := cloudid.Resolve(context.Background(), id); err == nil {
		t.Error("Resolve with no region must fail rather than guess a default")
	}
}

func TestEnv_CarriesCredentialsAndRegion(t *testing.T) {
	creds := cloudid.Credentials{AccessKeyID: "A", SecretAccessKey: "B", SessionToken: "C", Region: "eu-central-1"}
	env := strings.Join(cloudid.Env(creds), "\n")

	for _, want := range []string{
		"AWS_ACCESS_KEY_ID=A", "AWS_SECRET_ACCESS_KEY=B", "AWS_SESSION_TOKEN=C",
		"AWS_REGION=eu-central-1", "AWS_DEFAULT_REGION=eu-central-1",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("Env() missing %q", want)
		}
	}
}

func TestRefresher_ReusesCredentialsUntilTheyNearExpiry(t *testing.T) {
	var calls int
	now := time.Now()
	r := cloudid.NewRefresher(func(context.Context) (cloudid.Credentials, error) {
		calls++
		return cloudid.Credentials{AccessKeyID: "A", Expires: now.Add(time.Hour)}, nil
	})
	r.Now = func() time.Time { return now }

	for range 3 {
		if _, err := r.Get(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("resolved %d times, want 1 — a valid credential must not be re-minted per call", calls)
	}
}

func TestRefresher_RenewsBeforeExpiry(t *testing.T) {
	var calls int
	now := time.Now()
	r := cloudid.NewRefresher(func(context.Context) (cloudid.Credentials, error) {
		calls++
		return cloudid.Credentials{AccessKeyID: "A", Expires: now.Add(time.Hour)}, nil
	})
	r.Now = func() time.Time { return now }

	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Inside the renewal margin but not expired, it renews rather than hand out a credential about to die.
	r.Now = func() time.Time { return now.Add(time.Hour - cloudid.RenewMargin/2) }
	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("resolved %d times, want 2 — a credential inside the renewal margin must be renewed", calls)
	}
}

func TestRefresher_FailsClosedWhenRenewalFails(t *testing.T) {
	// An expired credential must yield an error and no usable client, never the last known good one.
	now := time.Now()
	var fail bool
	r := cloudid.NewRefresher(func(context.Context) (cloudid.Credentials, error) {
		if fail {
			return cloudid.Credentials{}, errors.New("network down")
		}
		return cloudid.Credentials{AccessKeyID: "A", Expires: now.Add(time.Hour)}, nil
	})
	r.Now = func() time.Time { return now }

	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fail = true
	r.Now = func() time.Time { return now.Add(2 * time.Hour) } // past expiry

	got, err := r.Get(context.Background())
	if err == nil {
		t.Fatal("an expired credential that cannot be renewed MUST fail, never fall back to the stale one")
	}
	if got.AccessKeyID != "" {
		t.Errorf("returned credentials %+v alongside the error — the caller must get nothing usable", got)
	}
}

func TestRefresher_NeverReturnsAnAlreadyExpiredCredential(t *testing.T) {
	// A cached STS response or clock skew can keep handing back a dead credential, and failing closed must cover it.
	now := time.Now()
	r := cloudid.NewRefresher(func(context.Context) (cloudid.Credentials, error) {
		return cloudid.Credentials{AccessKeyID: "A", Expires: now.Add(time.Hour)}, nil
	})
	r.Now = func() time.Time { return now }
	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	r.Now = func() time.Time { return now.Add(2 * time.Hour) }
	got, err := r.Get(context.Background())
	if err == nil {
		t.Fatal("a resolver returning an already-expired credential must fail closed, not pass it through")
	}
	if got.AccessKeyID != "" {
		t.Errorf("returned credentials %+v alongside the error — the caller must get nothing usable", got)
	}
}

func TestRefresher_RenewalYieldsAUsableCredential(t *testing.T) {
	now := time.Now()
	r := cloudid.NewRefresher(func(context.Context) (cloudid.Credentials, error) {
		return cloudid.Credentials{AccessKeyID: "A", Expires: now.Add(3 * time.Hour)}, nil
	})
	r.Now = func() time.Time { return now }
	if _, err := r.Get(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	r.Now = func() time.Time { return now.Add(2 * time.Hour) }
	got, err := r.Get(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Expires.After(r.Now()) {
		t.Errorf("Get returned an already-expired credential: expires %v, now %v", got.Expires, r.Now())
	}
}

func TestRefresher_ZeroExpiryIsTreatedAsUnusable(t *testing.T) {
	// An unknown expiry must not be read as never expiring, which would let a dead credential through.
	var calls int
	r := cloudid.NewRefresher(func(context.Context) (cloudid.Credentials, error) {
		calls++
		return cloudid.Credentials{AccessKeyID: "A"}, nil
	})
	for range 2 {
		_, _ = r.Get(context.Background())
	}
	if calls != 2 {
		t.Errorf("resolved %d times, want 2 — a credential with no known expiry must never be cached", calls)
	}
}

func TestClassify_NamesTheFailuresAnOperatorMustAct_On(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"EIP quota", "AddressLimitExceeded: The maximum number of addresses has been reached", cloudid.ErrQuotaExceeded},
		{"generic quota", "LimitExceeded: you have requested more instances", cloudid.ErrQuotaExceeded},
		{"no default VPC", "VPCIdNotSpecified: No default VPC for this user", cloudid.ErrNoDefaultVPC},
		{"instance type not in AZ", "Unsupported: Your requested instance type (t3.small) is not supported in your requested Availability Zone (eu-central-1a)", cloudid.ErrInstanceTypeUnavailable},
		{"no subnet", "InvalidSubnetID.NotFound: The subnet ID 'subnet-x' does not exist", cloudid.ErrNoSubnet},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cloudid.Classify(errors.New(c.in)); !errors.Is(got, c.want) {
				t.Errorf("Classify(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestClassify_PreservesTheOriginalMessage(t *testing.T) {
	// Naming the class must not throw away the detail the operator puts in statusDetail.
	err := cloudid.Classify(errors.New("AddressLimitExceeded: maximum number of addresses reached in eu-central-1"))
	if !strings.Contains(err.Error(), "eu-central-1") {
		t.Errorf("classified error %v dropped the original detail", err)
	}
}

func TestClassify_LeavesAnUnknownErrorAlone(t *testing.T) {
	orig := errors.New("something nobody predicted")
	got := cloudid.Classify(orig)
	if !errors.Is(got, orig) {
		t.Errorf("an unrecognised error must pass through unchanged, got %v", got)
	}
	for _, sentinel := range []error{cloudid.ErrQuotaExceeded, cloudid.ErrNoDefaultVPC, cloudid.ErrInstanceTypeUnavailable, cloudid.ErrNoSubnet} {
		if errors.Is(got, sentinel) {
			t.Errorf("unrecognised error was misclassified as %v", sentinel)
		}
	}
}

func TestClassify_NilIsNil(t *testing.T) {
	if got := cloudid.Classify(nil); got != nil {
		t.Errorf("Classify(nil) = %v, want nil", got)
	}
}
