package credrefresh_test

import (
	"context"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/credrefresh"
	"github.com/mitstack-eu/operator-kit/kittest"
)

// The operator rule is that a secret an operator receives may go to the cloud
// it drives, but must never appear in what it hands back. The Refresher holds
// exactly the kind of value that rule is about, so the guarantee is pinned
// here once rather than trusted to every provider that embeds it.

const leakSecret = "PROVIDER-CREDENTIAL-MATERIAL-THAT-MUST-NEVER-SURFACE"

type secretCred struct {
	Token string
}

func TestNoSecretLeak_RefresherErrorCarriesNoCredential(t *testing.T) {
	clock := time.Now()
	r := credrefresh.New(func(context.Context) (secretCred, time.Time, error) {
		// Already dead on arrival: forces the fail-closed path.
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
