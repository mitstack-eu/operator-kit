// Package cloudid_test pins how an identity becomes AWS credentials, how a Refresher renews them, and how an AWS failure is classified.
package cloudid_test

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/cloudid"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

const (
	webToken  = "SUPER-SECRET-WEB-IDENTITY-TOKEN"
	awsSecret = "AWS-SECRET-ACCESS-KEY-THAT-MUST-NEVER-SURFACE"
	exportOK  = `echo '{"AccessKeyId":"AKIATEST","SecretAccessKey":"shh","SessionToken":"tok","Expiration":"2099-01-01T00:00:00Z"}'`
)

func identity() cloudid.Identity {
	return cloudid.Identity{
		RoleARN:          "arn:aws:iam::123456789012:role/mitstack-app",
		WebIdentityToken: webToken,
		Region:           "eu-central-1",
		SessionSuffix:    "app-42",
	}
}

// awsCall is what the stub aws CLI saw: its argv and the identity variables Resolve set, plus the raw record.
type awsCall struct {
	vars map[string]string
	raw  string
}

// resolve runs Resolve against a stub aws CLI on PATH that records its call and then runs script.
func resolve(t *testing.T, script string, id cloudid.Identity) (cloudid.Credentials, error, awsCall) {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	stub := "#!/usr/bin/env bash\n" +
		"printf 'ARGV=%s\\nROLE=%s\\nTOKENFILE=%s\\nSESSION=%s\\nREGION=%s\\n' \"$*\" \"$AWS_ROLE_ARN\" \"$AWS_WEB_IDENTITY_TOKEN_FILE\" \"$AWS_ROLE_SESSION_NAME\" \"$AWS_REGION\" >> " + record + "\n" +
		script + "\n"
	if err := os.WriteFile(filepath.Join(dir, "aws"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub aws: %v", err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	creds, err := cloudid.Resolve(context.Background(), id)

	raw, _ := os.ReadFile(record)
	call := awsCall{vars: map[string]string{}, raw: string(raw)}
	sc := bufio.NewScanner(strings.NewReader(call.raw))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			call.vars[k] = v
		}
	}
	return creds, err, call
}

func TestProviderARN(t *testing.T) {
	cases := []struct {
		name, account, issuer, want string
	}{
		{"an issuer URL becomes the provider ARN without a discovery call", "123456789012", "https://api.mitstack.dev", "arn:aws:iam::123456789012:oidc-provider/api.mitstack.dev"},
		{"an issuer without a scheme is taken as the host", "123456789012", "api.mitstack.dev", "arn:aws:iam::123456789012:oidc-provider/api.mitstack.dev"},
		{"a trailing slash is stripped and the path kept", "123456789012", "https://issuer.example.com/tenant/", "arn:aws:iam::123456789012:oidc-provider/issuer.example.com/tenant"},
		{"an empty account is refused", "", "https://api.mitstack.dev", ""},
		{"an empty issuer is refused", "123456789012", "", ""},
		{"an account that is not 12 digits is refused", "not-an-account", "https://api.mitstack.dev", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cloudid.ProviderARN(tc.account, tc.issuer)

			if tc.want == "" {
				if err == nil {
					t.Errorf("ProviderARN(%q, %q) = %q; want an error rather than a malformed ARN", tc.account, tc.issuer, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("ProviderARN(%q, %q) = %q, %v; want %q", tc.account, tc.issuer, got, err, tc.want)
			}
		})
	}
}

func TestResolve_Succeeds(t *testing.T) {
	cases := []struct {
		name  string
		check func(t *testing.T, creds cloudid.Credentials, call awsCall)
	}{
		{"it returns the exported credentials in the identity's region with their expiry", func(t *testing.T, creds cloudid.Credentials, _ awsCall) {
			want := cloudid.Credentials{AccessKeyID: "AKIATEST", SecretAccessKey: "shh", SessionToken: "tok", Region: "eu-central-1",
				Expires: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)}
			if !creds.Expires.Equal(want.Expires) {
				t.Errorf("Expires = %v, want %v: a Refresher cannot renew ahead of an unknown expiry", creds.Expires, want.Expires)
			}
			creds.Expires = want.Expires
			if creds != want {
				t.Errorf("credentials = %+v, want %+v", creds, want)
			}
		}},
		{"the token reaches the CLI only as a path to a file", func(t *testing.T, _ cloudid.Credentials, call awsCall) {
			kittest.AssertNoSecret(t, "the aws argv and env", call.raw, webToken)
			if path := call.vars["TOKENFILE"]; !filepath.IsAbs(path) {
				t.Errorf("AWS_WEB_IDENTITY_TOKEN_FILE = %q, want an absolute path", path)
			}
		}},
		{"the token file is removed once Resolve returns", func(t *testing.T, _ cloudid.Credentials, call awsCall) {
			path := call.vars["TOKENFILE"]
			if path == "" {
				t.Fatalf("the stub recorded no token file path:\n%s", call.raw)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("os.Stat(%s) = %v; want not-exist, since a token left on disk is a leaked credential", path, err)
			}
		}},
		{"the session name carries the suffix for CloudTrail attribution", func(t *testing.T, _ cloudid.Credentials, call awsCall) {
			if got, want := call.vars["SESSION"], "mitstack-app-42"; got != want {
				t.Errorf("AWS_ROLE_SESSION_NAME = %q, want %q", got, want)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			creds, err, call := resolve(t, exportOK, identity())

			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			tc.check(t, creds, call)
		})
	}
}

func TestResolve_FailsWithoutLeakingTheToken(t *testing.T) {
	cases := []struct {
		name      string
		script    string
		mutate    func(*cloudid.Identity)
		wantInErr string
	}{
		{"the AWS error is surfaced, not a bare failure", `echo "An error occurred (AccessDenied) when calling AssumeRoleWithWebIdentity" >&2; exit 254`, nil, "AccessDenied"},
		{"a CLI failure naming the token's rejection", `echo "An error occurred (InvalidIdentityToken)" >&2; exit 254`, nil, "InvalidIdentityToken"},
		{"stdout that is not JSON", `echo 'not json at all'`, nil, ""},
		{"empty stdout", `echo -n ''`, nil, ""},
		{"no role", exportOK, func(id *cloudid.Identity) { id.RoleARN = "" }, ""},
		{"no region, rather than a default that could shift under a live app", exportOK, func(id *cloudid.Identity) { id.Region = "" }, ""},
		{"no token", exportOK, func(id *cloudid.Identity) { id.WebIdentityToken = "" }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := identity()
			if tc.mutate != nil {
				tc.mutate(&id)
			}

			_, err, _ := resolve(t, tc.script, id)

			if err == nil {
				t.Fatal("Resolve succeeded; want an error")
			}
			kittest.AssertNoSecret(t, "the Resolve error", err.Error(), webToken)
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("Resolve error = %v; want it to carry %q", err, tc.wantInErr)
			}
		})
	}
}

func TestEnv_CarriesCredentialsAndRegion(t *testing.T) {
	creds := cloudid.Credentials{AccessKeyID: "A", SecretAccessKey: "B", SessionToken: "C", Region: "eu-central-1"}

	env := cloudid.Env(creds)

	for _, want := range []string{
		"AWS_ACCESS_KEY_ID=A", "AWS_SECRET_ACCESS_KEY=B", "AWS_SESSION_TOKEN=C",
		"AWS_REGION=eu-central-1", "AWS_DEFAULT_REGION=eu-central-1",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("Env(%+v) lacks %q", creds, want)
		}
	}
}

type outcome int

const (
	usable outcome = iota
	refused
	either
)

// get is one Refresher.Get at an offset from the start, with the outcome it must have.
type get struct {
	at   time.Duration
	want outcome
}

func TestRefresher(t *testing.T) {
	start := time.Now()
	expiresIn := func(d time.Duration) func(int) (cloudid.Credentials, error) {
		return func(int) (cloudid.Credentials, error) {
			return cloudid.Credentials{AccessKeyID: "A", Expires: start.Add(d)}, nil
		}
	}
	cases := []struct {
		name      string
		resolve   func(call int) (cloudid.Credentials, error)
		gets      []get
		wantCalls int
		realClock bool
	}{
		{"a valid credential is reused rather than re-minted per call", expiresIn(time.Hour),
			[]get{{0, usable}, {0, usable}, {0, usable}}, 1, false},
		{"a credential inside the renewal margin is renewed", expiresIn(time.Hour),
			[]get{{0, usable}, {time.Hour - cloudid.RenewMargin/2, usable}}, 2, false},
		{"a renewal that fails past expiry fails closed instead of returning the stale credential", func(call int) (cloudid.Credentials, error) {
			if call > 1 {
				return cloudid.Credentials{}, errors.New("network down")
			}
			return cloudid.Credentials{AccessKeyID: "A", Expires: start.Add(time.Hour)}, nil
		}, []get{{0, usable}, {2 * time.Hour, refused}}, 0, false},
		{"a resolver handing back an already expired credential fails closed", expiresIn(time.Hour),
			[]get{{0, usable}, {2 * time.Hour, refused}}, 0, false},
		{"a renewal yields a credential that is not yet expired", expiresIn(3 * time.Hour),
			[]get{{0, usable}, {2 * time.Hour, usable}}, 0, false},
		{"a credential with no known expiry is never cached", func(int) (cloudid.Credentials, error) {
			return cloudid.Credentials{AccessKeyID: "A"}, nil
		}, []get{{0, either}, {0, either}}, 2, true},
		{"the fail-closed error carries no credential material", func(int) (cloudid.Credentials, error) {
			return cloudid.Credentials{AccessKeyID: "AKIA", SecretAccessKey: awsSecret, Expires: start.Add(-time.Hour)}, nil
		}, []get{{0, refused}}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			r := cloudid.NewRefresher(func(context.Context) (cloudid.Credentials, error) {
				calls++
				return tc.resolve(calls)
			})

			for i, g := range tc.gets {
				now := start.Add(g.at)
				if !tc.realClock {
					r.Now = func() time.Time { return now }
				}

				got, err := r.Get(context.Background())

				switch {
				case g.want == usable && err != nil:
					t.Fatalf("Get #%d at +%v: %v; want a usable credential", i+1, g.at, err)
				case g.want == usable && !got.Expires.After(now):
					t.Errorf("Get #%d at +%v returned a credential expiring %v; want one still valid", i+1, g.at, got.Expires)
				case g.want == refused && err == nil:
					t.Fatalf("Get #%d at +%v = %+v; want an error, never a stale credential", i+1, g.at, got)
				case g.want == refused:
					if got != (cloudid.Credentials{}) {
						t.Errorf("Get #%d returned %+v alongside its error; want nothing usable", i+1, got)
					}
					kittest.AssertNoSecret(t, "the Refresher error", err.Error(), awsSecret)
				}
			}
			if tc.wantCalls != 0 && calls != tc.wantCalls {
				t.Errorf("resolved %d times, want %d", calls, tc.wantCalls)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	unknown := errors.New("something nobody predicted")
	sentinels := []error{cloudid.ErrQuotaExceeded, cloudid.ErrNoDefaultVPC, cloudid.ErrInstanceTypeUnavailable, cloudid.ErrNoSubnet}
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"an EIP quota is a quota failure, keeping the detail", errors.New("AddressLimitExceeded: maximum number of addresses reached in eu-central-1"), cloudid.ErrQuotaExceeded},
		{"a generic limit is a quota failure", errors.New("LimitExceeded: you have requested more instances"), cloudid.ErrQuotaExceeded},
		{"a missing default VPC", errors.New("VPCIdNotSpecified: No default VPC for this user"), cloudid.ErrNoDefaultVPC},
		{"an instance type the zone does not offer", errors.New("Unsupported: Your requested instance type (t3.small) is not supported in your requested Availability Zone (eu-central-1a)"), cloudid.ErrInstanceTypeUnavailable},
		{"a subnet that does not exist", errors.New("InvalidSubnetID.NotFound: The subnet ID 'subnet-x' does not exist"), cloudid.ErrNoSubnet},
		{"an unrecognised error passes through unchanged", unknown, unknown},
		{"nil stays nil", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cloudid.Classify(tc.in)

			if tc.in == nil {
				if got != nil {
					t.Errorf("Classify(nil) = %v, want nil", got)
				}
				return
			}
			if !errors.Is(got, tc.want) {
				t.Errorf("Classify(%q) = %v, want it to wrap %v", tc.in, got, tc.want)
			}
			if !strings.Contains(got.Error(), tc.in.Error()) {
				t.Errorf("Classify(%q) = %q; want the original message kept for statusDetail", tc.in, got)
			}
			for _, s := range sentinels {
				if s != tc.want && errors.Is(got, s) {
					t.Errorf("Classify(%q) = %v, also classified as %v", tc.in, got, s)
				}
			}
		})
	}
}
