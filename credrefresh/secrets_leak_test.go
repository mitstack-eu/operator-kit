package credrefresh_test

import (
	"context"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/credrefresh"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

const leakSecret = "PROVIDER-CREDENTIAL-MATERIAL-THAT-MUST-NEVER-SURFACE"

type secretCred struct {
	Token string
}

func TestNoSecretLeak_RefresherErrorCarriesNoCredential(t *testing.T) {
	clock := time.Now()
	r := credrefresh.New(func(context.Context) (secretCred, time.Time, error) {
		return secretCred{Token: leakSecret}, clock.Add(-time.Hour), nil
	})
	r.Now = func() time.Time { return clock }

	got, err := r.Get(context.Background())
	if err == nil {
		t.Fatal("expected the fail-closed error")
	}
	kittest.AssertNoSecret(t, "Refresher error", err.Error(), leakSecret)
	if got.Token != "" {
		t.Error("Refresher returned credential material alongside its error")
	}
}
