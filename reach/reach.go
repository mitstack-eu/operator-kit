// Package reach, an optional helper, evaluates whether an app is serving and records the verdict as the fixed output keys every provider emits.
package reach

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/settle"
)

const ProbeTimeout = 5 * time.Second

const (
	OutputAppURL      = "appUrl"
	OutputReachable   = "reachable"
	OutputCertTrusted = "certTrusted"
	// OutputAppHostname preserves the intended hostname when appUrl was rewritten to a fallback address.
	OutputAppHostname = "appHostname"
	// OutputPublicIP is the box's public address, written by the provisioner and read for the fallback probe.
	OutputPublicIP = "publicIp"
)

func Keys() []string {
	return []string{
		OutputAppHostname,
		OutputAppURL,
		OutputCertTrusted,
		OutputPublicIP,
		OutputReachable,
	}
}

type Result struct {
	Reachable   bool
	CertTrusted bool

	// Reason explains a false Reachable and never carries credentials.
	Reason string

	// EdgeUp reports that the front door answered at all, including a 5xx; settle gates read it so a provider never waits on its consumer's app.
	EdgeUp bool

	// DNSFailure marks a hostname that does not resolve, which is the platform's fault rather than the app's.
	DNSFailure bool

	// ServingURL is the URL that actually answered, which is the IP fallback when the hostname does not resolve.
	ServingURL string
}

type Prober func(ctx context.Context, url string) Result

type Diagnoser func(ctx context.Context, hostname string) string

// Checker evaluates reachability for one operator; its zero value probes for real.
type Checker struct {
	// Client is the HTTP client for the real probe; certificate verification is never disabled.
	Client   *http.Client
	Probe    Prober
	Diagnose Diagnoser
	// ExtraSuffixes widens TrustedAppURL's barrier for this evaluation only, and must come from VerifiedCustomDomain.
	ExtraSuffixes []string
}

// Outputs with no appUrl count as reachable, so non-web blocks are not stranded behind a web-only gate.
func (c Checker) Evaluate(ctx context.Context, outputs map[string]string) Result {
	appURL := outputs[OutputAppURL]
	if appURL == "" {
		return Result{Reachable: true, CertTrusted: false, EdgeUp: true}
	}

	res := c.probe(ctx, appURL)
	if res.Reachable || !res.DNSFailure {
		return c.promote(ctx, outputs, appURL, res)
	}

	diagnosis := c.diagnose(ctx, appURL)

	ip := outputs[OutputPublicIP]
	if ip == "" {
		return withDiagnosis(res, diagnosis)
	}

	fallbackURL := "https://" + ip + "/"
	fb := c.probe(ctx, fallbackURL)
	if !fb.Reachable {
		return withDiagnosis(res, diagnosis)
	}
	return withDiagnosis(Result{
		Reachable:   true,
		CertTrusted: false, // a bare-IP URL can only carry a self-signed cert
		EdgeUp:      true,
		ServingURL:  fallbackURL,
		Reason: fmt.Sprintf(
			"serving on %s; the app hostname does not resolve yet, so the platform is advertising the box address instead",
			fallbackURL),
	}, diagnosis)
}

func (c Checker) promote(ctx context.Context, outputs map[string]string, appURL string, res Result) Result {
	hostname := outputs[OutputAppHostname]
	if hostname == "" || hostname == appURL {
		return res
	}

	hres := c.probe(ctx, hostname)
	if hres.Reachable {
		hres.ServingURL = hostname
		return hres
	}

	if hres.DNSFailure {
		res.Reason = fmt.Sprintf(
			"serving on %s; the app hostname still does not resolve, so the platform is staying on the box address",
			appURL)
	}
	return res
}

func (c Checker) probe(ctx context.Context, url string) Result {
	if c.Probe != nil {
		return c.Probe(ctx, url)
	}
	return probeURL(ctx, c.Client, url, c.ExtraSuffixes, url)
}

func (c Checker) diagnose(ctx context.Context, hostname string) string {
	if c.Diagnose == nil {
		return ""
	}
	return c.Diagnose(ctx, hostname)
}

func withDiagnosis(res Result, diagnosis string) Result {
	if diagnosis == "" {
		return res
	}
	if res.Reason == "" {
		res.Reason = diagnosis
		return res
	}
	res.Reason += " — " + diagnosis
	return res
}

// PlatformAppsDomain is the platform's own apps zone, the one implicit entry in TrustedAppURL's allowlist.
const PlatformAppsDomain = "apps.mitstack.dev"

func TrustedAppURL(raw string, extraSuffixes ...string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("reach: app URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("reach: app URL %q is not usable", raw)
	}

	host := strings.ToLower(u.Host)
	scheme := strings.ToLower(u.Scheme)
	rebuilt := (&url.URL{Scheme: scheme, Host: host, Path: u.Path, RawQuery: u.RawQuery}).String()

	if trustedAppURLPattern(extraSuffixes).MatchString(rebuilt) {
		return rebuilt, nil
	}
	return "", fmt.Errorf(
		"reach: app URL host %q is not allowlisted for probing — must be https on %s or an operator-supplied suffix",
		host, PlatformAppsDomain)
}

// trustedAppURLPattern uses a regexp match rather than strings.HasSuffix because CodeQL's go/request-forgery query only recognises a regexp match as a barrier; RE2 cannot backtrack catastrophically.
func trustedAppURLPattern(extraSuffixes []string) *regexp.Regexp {
	suffixes := append([]string{PlatformAppsDomain}, extraSuffixes...)
	var alts []string
	for _, s := range suffixes {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		esc := regexp.QuoteMeta(s)
		// The label set excludes "/", "?" and "#" so a path cannot masquerade as a subdomain.
		alts = append(alts, esc, `[a-z0-9.-]+\.`+esc)
	}
	hostAlt := strings.Join(alts, "|")
	return regexp.MustCompile(`^https://(?:` + hostAlt + `)(?::\d+)?(?:/.*)?$`)
}

type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// One matching address suffices, since round-robin records are ordinary; a resolver error is always a refusal.
func VerifiedCustomDomain(ctx context.Context, resolver Resolver, domain string, expectedAddrs []string) (string, error) {
	if domain == "" || strings.ContainsAny(domain, ":/") {
		return "", fmt.Errorf("reach: custom domain %q is not a bare hostname", domain)
	}

	addrs, err := resolver.LookupHost(ctx, domain)
	if err != nil {
		return "", fmt.Errorf("reach: custom domain %q did not resolve: %w", domain, err)
	}

	for _, addr := range addrs {
		for _, want := range expectedAddrs {
			if addr == want {
				return domain, nil
			}
		}
	}
	return "", fmt.Errorf(
		"reach: custom domain %q resolved to %v, which does not match any address this subscription provisioned (%v)",
		domain, addrs, expectedAddrs)
}

// Serving means a status below 500, or a TLS failure on trust only; a 502 is the edge up with the app down.
// appURL must be in allowedURLs AND pass the barrier, since an OR would let a self-vouching caller reach a metadata endpoint.
func ProbeURL(ctx context.Context, client *http.Client, appURL string, allowedURLs ...string) Result {
	return probeURL(ctx, client, appURL, nil, allowedURLs...)
}

// probeURL adds extraSuffixes, which ProbeURL's trailing variadic cannot take without shifting call sites.
func probeURL(ctx context.Context, client *http.Client, appURL string, extraSuffixes []string, allowedURLs ...string) Result {
	if appURL == "" {
		return Result{Reason: "no app URL to probe"}
	}

	dialURL, err := resolveDialURL(appURL, allowedURLs, extraSuffixes)
	if err != nil {
		return Result{Reason: err.Error()}
	}

	if client == nil {
		client = &http.Client{Timeout: ProbeTimeout}
	}

	probeCtx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, dialURL, nil)
	if err != nil {
		return Result{Reason: fmt.Sprintf("app URL is not usable: %v", err)}
	}

	// Never follow a redirect: a 3xx to a response-chosen host would bypass the vouched URL.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	client = &noRedirect

	resp, err := client.Do(req)
	if err != nil {
		if settle.IsUntrustedCertificate(err) {
			return Result{Reachable: true, CertTrusted: false, EdgeUp: true, ServingURL: dialURL}
		}
		if settle.IsDNSFailure(err) {
			return Result{
				DNSFailure: true,
				Reason:     "app hostname does not resolve: " + err.Error(),
			}
		}
		return Result{Reason: "app URL is not answering: " + err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusInternalServerError {
		return Result{
			EdgeUp: true,
			Reason: fmt.Sprintf("app URL answered %d — the edge is up but the app is not serving yet", resp.StatusCode),
		}
	}
	return Result{Reachable: true, CertTrusted: true, EdgeUp: true, ServingURL: dialURL}
}

// resolveDialURL returns the string ProbeURL may dial, which comes only from the regexp-guarded barrier; no match is default-deny.
func resolveDialURL(appURL string, allowedURLs []string, extraSuffixes []string) (string, error) {
	vouched := false
	for _, candidate := range allowedURLs {
		if candidate == appURL {
			vouched = true
			break
		}
	}
	if !vouched {
		return "", fmt.Errorf(
			"reach: app URL %q is not vouched for by the caller's allowlist (MIT-1683) — TrustedAppURL alone is not sufficient",
			appURL)
	}

	dial, err := vouchBarrierCheckedURL(appURL, extraSuffixes)
	if err != nil {
		return "", fmt.Errorf(
			"reach: app URL %q is vouched for but clears neither the platform apps domain (%s) nor a loopback/non-production host (MIT-1686): %v",
			appURL, PlatformAppsDomain, err)
	}
	return dial, nil
}

// vouchBarrierCheckedURL tries each barrier in turn; each returns a value rebuilt from parsed parts, never the raw candidate.
func vouchBarrierCheckedURL(candidate string, extraSuffixes []string) (string, error) {
	if dial, err := TrustedAppURL(candidate, extraSuffixes...); err == nil {
		return dial, nil
	}
	if dial, err := publicUnicastIPCheckedURL(candidate); err == nil {
		return dial, nil
	}
	return neverRoutableCheckedURL(candidate)
}

// publicUnicastIPCheckedURL admits only a public unicast IP literal; link-local (the metadata endpoint) and private ranges are rejected.
func publicUnicastIPCheckedURL(candidate string) (string, error) {
	u, err := url.Parse(candidate)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("reach: vouched URL %q is not usable", candidate)
	}

	ip := net.ParseIP(u.Hostname()) // Hostname strips the port and IPv6 brackets
	if ip == nil {
		return "", fmt.Errorf("reach: vouched URL %q is not an IP address", candidate)
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return "", fmt.Errorf("reach: vouched URL %q is not a public unicast address", candidate)
	}

	host := ip.String()
	if port := u.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	} else if ip.To4() == nil {
		host = "[" + host + "]"
	}
	rebuilt := (&url.URL{
		Scheme:   strings.ToLower(u.Scheme),
		Host:     host,
		Path:     u.Path,
		RawQuery: u.RawQuery,
	}).String()

	// ParseIP makes this safe; the regexp match makes it visible to CodeQL, which recognises no boolean predicate as a barrier.
	if !publicUnicastURLPattern.MatchString(rebuilt) {
		return "", fmt.Errorf("reach: vouched URL %q is not usable", candidate)
	}
	return rebuilt, nil
}

var publicUnicastURLPattern = regexp.MustCompile(
	`^https?://(?:\d{1,3}(?:\.\d{1,3}){3}|\[[0-9a-fA-F:.]+\])(?::\d+)?(?:/.*)?$`)

// neverRoutableCheckedURL admits loopback and the reserved test TLDs, assuming the public DNS root.
func neverRoutableCheckedURL(candidate string) (string, error) {
	u, err := url.Parse(candidate)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("reach: vouched URL %q is not usable", candidate)
	}
	host := strings.ToLower(u.Host)
	scheme := strings.ToLower(u.Scheme)
	rebuilt := (&url.URL{Scheme: scheme, Host: host, Path: u.Path, RawQuery: u.RawQuery}).String()
	if !neverRoutableURLPattern.MatchString(rebuilt) {
		return "", fmt.Errorf("reach: vouched URL %q is not usable", candidate)
	}
	return rebuilt, nil
}

var neverRoutableURLPattern = regexp.MustCompile(
	`^https?://(?:127\.0\.0\.1|localhost|\[::1\]|(?:example|test|invalid)|[a-z0-9.-]+\.(?:example|test|invalid))(?::\d+)?(?:/.*)?$`)

// Apply copies the caller's map and never mutates it.
func Apply(outputs map[string]string, res Result) map[string]string {
	merged := make(map[string]string, len(outputs)+len(Keys()))
	for k, v := range outputs {
		merged[k] = v
	}
	merged[OutputReachable] = BoolString(res.Reachable)
	merged[OutputCertTrusted] = BoolString(res.CertTrusted)

	switch {
	case res.ServingURL != "" && res.ServingURL == merged[OutputAppHostname]:
		// Promotion: appUrl is the hostname again, so the preserved field must go or it reads as a fresh fallback.
		merged[OutputAppURL] = res.ServingURL
		delete(merged, OutputAppHostname)
	case res.ServingURL != "" && res.ServingURL != merged[OutputAppURL]:
		// Fallback: advertise the address that answered and preserve the intended hostname.
		merged[OutputAppHostname] = merged[OutputAppURL]
		merged[OutputAppURL] = res.ServingURL
	}
	return merged
}

// BoolString renders a bool as "true" or "false", the only spelling the console reads.
func BoolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// safeProbeURL rebuilds a URL from checked parts and accepts only http and https, since file: or gopher: would read whatever the process can reach.
func safeProbeURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("app URL must be http or https")
	}
	if u.Host == "" {
		return "", errors.New("app URL has no host")
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawQuery: u.RawQuery}).String(), nil
}
