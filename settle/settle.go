// Package settle, an optional helper, decides whether a consumer's resource is actually serving, with every wait bounded by a required deadline.
package settle

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// State is the three-valued settle outcome, because "give it another moment" and "this will never work" must stay distinct.
type State int

const (
	// Converging means not ready but nothing is wrong, so the platform must not mark it failed or spend a retry.
	Converging State = iota
	// Serving means the resource actually answers.
	Serving
	// Failed means the deadline passed or a probe reported something waiting will not resolve.
	Failed
)

func (s State) String() string {
	switch s {
	case Serving:
		return "serving"
	case Failed:
		return "failed"
	default:
		return "converging"
	}
}

// Result is what a settle attempt concluded, with a reason suitable for statusDetail.
type Result struct {
	State   State
	Reason  string
	Elapsed time.Duration
}

// Probe returns (false, nil) for "not yet" and (false, err) for "this looks wrong".
type Probe func(ctx context.Context) (ready bool, err error)

// Gate evaluates probes against a deadline and cannot be constructed without one.
type Gate struct {
	deadline time.Time
	probes   []namedProbe
	now      func() time.Time
}

type namedProbe struct {
	name  string
	probe Probe
}

// New rejects a zero deadline rather than reading it as "no limit", which would hang a subscription forever.
func New(deadline time.Time) (*Gate, error) {
	if deadline.IsZero() {
		return nil, errors.New("settle: a deadline is required — an unbounded wait is how a subscription hangs forever (MIT-1291)")
	}
	return &Gate{deadline: deadline, now: time.Now}, nil
}

// MustNew is New for a deadline built from a duration.
func MustNew(within time.Duration) *Gate {
	g, err := New(time.Now().Add(within))
	if err != nil {
		panic(err)
	}
	return g
}

// SetClock injects a clock for tests.
func (g *Gate) SetClock(now func() time.Time) { g.now = now }

// Add registers a named probe, and evaluation stops at the first probe that is not ready so a cheap check can guard an expensive one.
func (g *Gate) Add(name string, p Probe) *Gate {
	g.probes = append(g.probes, namedProbe{name: name, probe: p})
	return g
}

// Check evaluates the probes once without sleeping, so the caller decides the cadence.
func (g *Gate) Check(ctx context.Context) Result {
	now := g.now()
	elapsed := time.Duration(0)
	if !g.deadline.IsZero() {
		elapsed = now.Sub(g.deadline)
	}

	if len(g.probes) == 0 {
		// A gate with no probes would report Serving for anything.
		return Result{State: Failed, Reason: "settle: no probes registered — this gate would report serving for anything"}
	}

	for _, np := range g.probes {
		ready, err := np.probe(ctx)
		if err != nil {
			if !now.Before(g.deadline) {
				return Result{State: Failed, Reason: fmt.Sprintf("%s failed and the deadline passed: %v", np.name, err), Elapsed: elapsed}
			}
			return Result{State: Converging, Reason: fmt.Sprintf("%s not ready: %v", np.name, err), Elapsed: elapsed}
		}
		if !ready {
			if !now.Before(g.deadline) {
				return Result{State: Failed, Reason: fmt.Sprintf("%s still not ready when the deadline passed", np.name), Elapsed: elapsed}
			}
			return Result{State: Converging, Reason: np.name + " not ready yet", Elapsed: elapsed}
		}
	}
	return Result{State: Serving, Reason: "all probes ready", Elapsed: elapsed}
}

// DNS reports whether host resolves, and only ever says "not yet" because the gate's deadline turns that into a failure.
func DNS(host string) Probe {
	return func(ctx context.Context) (bool, error) {
		var r net.Resolver
		addrs, err := r.LookupHost(ctx, host)
		if err != nil {
			return false, err
		}
		return len(addrs) > 0, nil
	}
}

// TCP reports whether addr accepts a connection.
func TCP(addr string) Probe {
	return func(ctx context.Context) (bool, error) {
		d := net.Dialer{}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return false, err
		}
		_ = conn.Close()
		return true, nil
	}
}

// HTTP reports whether rawURL answers with a non-5xx status, counting an untrusted certificate as serving without disabling verification.
func HTTP(rawURL string) Probe {
	client := &http.Client{Timeout: 10 * time.Second}
	return func(ctx context.Context) (bool, error) {
		u, err := url.Parse(rawURL)
		if err != nil || u.Host == "" {
			return false, fmt.Errorf("settle: invalid url %q", rawURL)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return false, err
		}
		resp, err := client.Do(req)
		if err != nil {
			if IsUntrustedCertificate(err) {
				// A certificate error can only come from a peer that completed a TLS handshake.
				return true, nil
			}
			return false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 500 {
			return false, nil
		}
		return true, nil
	}
}

// IsUntrustedCertificate reports whether err is a certificate-trust failure, which proves a TLS peer is up where a connection failure proves nothing.
func IsUntrustedCertificate(err error) bool {
	if err == nil {
		return false
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verification *tls.CertificateVerificationError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostname) ||
		errors.As(err, &invalid) || errors.As(err, &verification) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "x509:") || strings.Contains(msg, "certificate is not trusted")
}

// IsDNSFailure reports whether err is a name-resolution failure, which callers narrate differently from a connection refusal.
func IsDNSFailure(err error) bool {
	if err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	return strings.Contains(err.Error(), "no such host")
}
