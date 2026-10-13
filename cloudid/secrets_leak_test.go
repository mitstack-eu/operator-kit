package cloudid_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/cloudid"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

const (
	leakToken  = "WEB-IDENTITY-TOKEN-THAT-MUST-NEVER-SURFACE"
	leakSecret = "AWS-SECRET-ACCESS-KEY-THAT-MUST-NEVER-SURFACE"
)

func TestNoSecretLeak_ResolveErrorsCarryNoCredentialMaterial(t *testing.T) {
	// No failure path may echo the token back.
	cases := map[string]string{
		"cli failure":        `echo "An error occurred (InvalidIdentityToken)" >&2; exit 254`,
		"unparseable stdout": `echo 'not json at all'`,
		"empty stdout":       `echo -n ''`,
	}

	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			dir, _ := fakeAWS(t, script)
			t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

			_, err := cloudid.Resolve(context.Background(), cloudid.Identity{
				RoleARN:          "arn:aws:iam::123456789012:role/r",
				WebIdentityToken: leakToken,
				Region:           "eu-central-1",
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			kittest.AssertNoSecret(t, "Resolve error", err.Error(), leakToken)
		})
	}
}

func TestNoSecretLeak_ValidationErrorsCarryNoToken(t *testing.T) {
	for _, id := range []cloudid.Identity{
		{WebIdentityToken: leakToken, Region: "eu-central-1"},                                    // no role
		{RoleARN: "arn:aws:iam::123456789012:role/r", WebIdentityToken: leakToken},               // no region
		{RoleARN: "arn:aws:iam::123456789012:role/r", WebIdentityToken: "", Region: "eu-west-1"}, // no token
	} {
		_, err := cloudid.Resolve(context.Background(), id)
		if err == nil {
			t.Fatalf("expected a validation error for %+v", id)
		}
		kittest.AssertNoSecret(t, "Resolve validation error", err.Error(), leakToken)
	}
}

func TestNoSecretLeak_RefresherErrorCarriesNoCredential(t *testing.T) {
	now := time.Now()
	r := cloudid.NewRefresher(func(context.Context) (cloudid.Credentials, error) {
		return cloudid.Credentials{
			AccessKeyID:     "AKIA",
			SecretAccessKey: leakSecret,
			Expires:         now.Add(-time.Hour), // already dead: forces the fail-closed path
		}, nil
	})
	r.Now = func() time.Time { return now }

	got, err := r.Get(context.Background())
	if err == nil {
		t.Fatal("expected the fail-closed error")
	}
	kittest.AssertNoSecret(t, "Refresher error", err.Error(), leakSecret)
	if got.SecretAccessKey != "" {
		t.Error("Refresher returned credential material alongside its error")
	}
}

func TestNoSecretLeak_ClassifyDoesNotInventCredentialText(t *testing.T) {
	// Classify must not add anything to the original message.
	err := cloudid.Classify(errors.New("AddressLimitExceeded: maximum addresses reached"))
	if !strings.Contains(err.Error(), "AddressLimitExceeded") {
		t.Errorf("Classify dropped the original message: %v", err)
	}
	if !errors.Is(err, cloudid.ErrQuotaExceeded) {
		t.Errorf("Classify did not name the class: %v", err)
	}
}
