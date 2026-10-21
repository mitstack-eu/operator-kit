package settle_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/settle"
)

func ready(context.Context) (bool, error)  { return true, nil }
func notYet(context.Context) (bool, error) { return false, nil }
func broken(context.Context) (bool, error) { return false, errors.New("connection refused") }

func TestNew_RefusesAZeroDeadline(t *testing.T) {
	// A zero deadline read as "no limit" is the fail-open reading that hung a subscription forever.
	if _, err := settle.New(time.Time{}); err == nil {
		t.Fatal("a zero deadline must be refused, never treated as unbounded")
	}
}

func TestGate_ReportsFailedOnceTheDeadlineHasPassed(t *testing.T) {
	now := time.Now()
	g, err := settle.New(now.Add(time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g.SetClock(func() time.Time { return now })
	g.Add("http", notYet)

	if got := g.Check(context.Background()); got.State != settle.Converging {
		t.Fatalf("before the deadline: State = %v, want Converging", got.State)
	}

	g.SetClock(func() time.Time { return now.Add(2 * time.Minute) })
	got := g.Check(context.Background())
	if got.State != settle.Failed {
		t.Errorf("past the deadline: State = %v, want Failed", got.State)
	}
	if got.Reason == "" {
		t.Error("a failure must carry a reason for statusDetail")
	}
}

func TestGate_ANotYetProbeIsConvergingNotFailed(t *testing.T) {
	g := settle.MustNew(time.Hour)
	g.Add("http", notYet)

	if got := g.Check(context.Background()); got.State != settle.Converging {
		t.Errorf("State = %v, want Converging — a block that is merely waiting must never be marked failed or spend a retry", got.State)
	}
}

func TestGate_AProbeErrorInsideTheDeadlineIsAlsoConverging(t *testing.T) {
	g := settle.MustNew(time.Hour)
	g.Add("tcp", broken)

	got := g.Check(context.Background())
	if got.State != settle.Converging {
		t.Errorf("State = %v, want Converging", got.State)
	}
	if got.Reason == "" {
		t.Error("the reason should name the probe and the error")
	}
}

func TestGate_ServingOnlyWhenEveryProbePasses(t *testing.T) {
	g := settle.MustNew(time.Hour)
	g.Add("dns", ready).Add("tcp", ready).Add("http", ready)

	if got := g.Check(context.Background()); got.State != settle.Serving {
		t.Errorf("State = %v, want Serving", got.State)
	}
}

func TestGate_OneFailingProbeIsEnoughToWithholdServing(t *testing.T) {
	g := settle.MustNew(time.Hour)
	g.Add("dns", ready).Add("http", notYet)

	if got := g.Check(context.Background()); got.State == settle.Serving {
		t.Error("Serving must require every probe")
	}
}

func TestGate_StopsAtTheFirstUnreadyProbeAndNamesIt(t *testing.T) {
	// The reason must name the step actually blocking, not the last one tried.
	var httpCalled bool
	g := settle.MustNew(time.Hour)
	g.Add("dns", notYet).Add("http", func(context.Context) (bool, error) {
		httpCalled = true
		return true, nil
	})

	got := g.Check(context.Background())
	if httpCalled {
		t.Error("evaluation must stop at the first unready probe")
	}
	if got.Reason == "" || !strings.Contains(got.Reason, "dns") {
		t.Errorf("Reason = %q, want the blocking probe named", got.Reason)
	}
}

func TestGate_WithNoProbesIsFailedNotServing(t *testing.T) {
	// A gate with no probes would report Serving for anything.
	g := settle.MustNew(time.Hour)

	if got := g.Check(context.Background()); got.State != settle.Failed {
		t.Errorf("State = %v, want Failed — an empty gate must never report serving", got.State)
	}
}

func TestState_ZeroValueIsConverging(t *testing.T) {
	// An unset State must be Converging: Serving would flip a subscription active on a forgotten path, and Failed would mark a healthy block dead.
	var s settle.State
	if s != settle.Converging {
		t.Errorf("the zero State = %v, want Converging", s)
	}
}

func TestHTTP_ServingOn200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ok, err := settle.HTTP(srv.URL)(context.Background())
	if err != nil || !ok {
		t.Errorf("ok=%v err=%v, want ready", ok, err)
	}
}

func TestHTTP_NotReadyOn5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	ok, err := settle.HTTP(srv.URL)(context.Background())
	if ok {
		t.Error("a 502 is not serving")
	}
	if err != nil {
		t.Errorf("a 502 is 'not yet', not an error: %v", err)
	}
}

func TestHTTP_A404CountsAsServing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	ok, _ := settle.HTTP(srv.URL)(context.Background())
	if !ok {
		t.Error("a 404 means something is serving")
	}
}

func TestHTTP_ToleratesASelfSignedCertificate(t *testing.T) {
	// A fresh edge serves self-signed TLS, and the probe must reach "serving" by classifying the cert error, not by disabling verification.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ok, err := settle.HTTP(srv.URL)(context.Background())
	if err != nil || !ok {
		t.Errorf("ok=%v err=%v, want a self-signed edge to count as serving", ok, err)
	}
}

func TestIsUntrustedCertificate_DistinguishesCertFailuresFromConnectionFailures(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("expected a certificate error from a self-signed test server")
	}
	if !settle.IsUntrustedCertificate(err) {
		t.Errorf("IsUntrustedCertificate(%v) = false, want true", err)
	}

	if settle.IsUntrustedCertificate(errors.New("dial tcp 127.0.0.1:1: connect: connection refused")) {
		t.Error("a connection refusal is not a certificate failure — nothing answered at all")
	}
	if settle.IsUntrustedCertificate(nil) {
		t.Error("IsUntrustedCertificate(nil) must be false")
	}
}

func TestHTTP_AConnectionRefusalIsStillNotServing(t *testing.T) {
	// The cert carve-out must not turn "nothing is listening" into "serving".
	ok, err := settle.HTTP("https://127.0.0.1:1")(context.Background())
	if ok {
		t.Error("a refused connection must never count as serving")
	}
	if err == nil {
		t.Error("the connection failure should be reported")
	}
}

func TestHTTP_RejectsAnInvalidURL(t *testing.T) {
	if _, err := settle.HTTP("not a url")(context.Background()); err == nil {
		t.Error("an unparseable url must be an error, not a silent 'not yet'")
	}
}

func TestDNS_AnUnresolvableNameIsNeverReady(t *testing.T) {
	ok, err := settle.DNS("this-host-does-not-exist.invalid")(context.Background())
	if ok {
		t.Error("an unresolvable name must never report ready")
	}
	if err == nil {
		t.Error("the resolution failure should be reported so the reason can name it")
	}
}

func TestIsDNSFailure(t *testing.T) {
	_, err := settle.DNS("this-host-does-not-exist.invalid")(context.Background())
	if !settle.IsDNSFailure(err) {
		t.Errorf("IsDNSFailure(%v) = false, want true", err)
	}
	if settle.IsDNSFailure(errors.New("connection refused")) {
		t.Error("a connection refusal is not a DNS failure — they narrate differently")
	}
	if settle.IsDNSFailure(nil) {
		t.Error("IsDNSFailure(nil) must be false")
	}
}
