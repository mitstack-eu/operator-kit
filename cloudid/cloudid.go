// Package cloudid turns the `CloudIdentity` mitstack delivers in a signed
// lifecycle webhook into usable, short-lived AWS credentials — once, correctly,
// for every operator (MIT-1467).
//
// mitstack never holds a consumer's cloud credentials. The webhook carries a
// short-lived RS256 web-identity token plus a role ARN, and the operator calls
// AssumeRoleWithWebIdentity itself. That much is simple. What is not simple is
// everything around it, and every failure this package prevents cost a live
// incident first:
//
//   - T-562  — the provider ARN is CONSTRUCTED, never discovered. Listing OIDC
//     providers needs iam:ListOpenIDConnectProviders, a permission an operator
//     has no business holding, and it broke in accounts that denied it.
//   - #439   — credentials come back through `aws configure export-credentials`;
//     an earlier version passed a CLI flag that does not exist.
//   - MIT-1132 — the region is the SUBSCRIPTION's pinned region. A package
//     default that shifts silently strands every existing app.
//   - MIT-412 / MIT-889 — a reconcile loop outlives the ~1-hour token, so a
//     Refresher must renew ahead of expiry and FAIL CLOSED. A stale credential
//     must never produce a wrong or destructive action.
//   - #436 and friends — predictable AWS failures become NAMED errors an
//     operator can map to statusDetail, not an opaque 422.
//
// The package is deliberately AWS-specific and named so. Azure and GCP federate
// differently enough that a shared "cloud" abstraction would leak.
package cloudid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mitstack-eu/operator-kit/credrefresh"
)

// Identity is the CloudIdentity an operator receives in the signed webhook.
type Identity struct {
	// RoleARN is the org-scoped federated role to assume.
	RoleARN string
	// WebIdentityToken is the short-lived RS256 token mitstack signed. It is
	// written to a temp file and never placed on a command line.
	WebIdentityToken string
	// Region is the SUBSCRIPTION's pinned region. Required — this package will
	// not supply a default, because a default that changes is MIT-1132.
	Region string
	// SessionSuffix identifies the subscription in the assumed-role session
	// name, so CloudTrail attributes actions to the right app. Optional.
	SessionSuffix string
}

// Credentials are the temporary credentials resulting from the assume-role.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
	// Expires is when these credentials stop working. A ZERO value means
	// unknown, and is treated as unusable-for-caching — never as "no expiry".
	Expires time.Time
}

// exportedCredentials is the shape of `aws configure export-credentials
// --format process` output. Unlike `aws sts assume-role-with-web-identity`,
// this is not wrapped in a Credentials object.
type exportedCredentials struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}

var accountIDPattern = regexp.MustCompile(`^\d{12}$`)

// ProviderARN builds the IAM OIDC provider ARN for an issuer in an account.
//
// It is a pure function on purpose (T-562). The alternative — listing the
// account's OIDC providers to find the matching one — requires
// iam:ListOpenIDConnectProviders on every consumer account, which is both a
// permission we should not ask for and a call that fails in accounts that deny
// it. The ARN was always derivable; nothing needed to be discovered.
func ProviderARN(accountID, issuer string) (string, error) {
	if !accountIDPattern.MatchString(accountID) {
		return "", fmt.Errorf("cloudid: %q is not a 12-digit AWS account id", accountID)
	}
	if strings.TrimSpace(issuer) == "" {
		return "", errors.New("cloudid: issuer is required")
	}
	host := issuer
	if strings.Contains(issuer, "://") {
		u, err := url.Parse(issuer)
		if err != nil {
			return "", fmt.Errorf("cloudid: parsing issuer %q: %w", issuer, err)
		}
		host = u.Host + u.Path
	}
	host = strings.Trim(host, "/")
	if host == "" {
		return "", fmt.Errorf("cloudid: issuer %q has no host", issuer)
	}
	return "arn:aws:iam::" + accountID + ":oidc-provider/" + host, nil
}

// Resolve assumes the identity's role via AssumeRoleWithWebIdentity and returns
// the temporary credentials.
//
// It uses the AWS CLI's env-based web-identity provider rather than the
// `aws sts assume-role-with-web-identity` subcommand, which has no file-based
// flag — only an inline `--web-identity-token <value>`, which would put the
// secret on the process command line for anything running `ps` to read. The
// token goes into a temp file pointed at by AWS_WEB_IDENTITY_TOKEN_FILE, and
// the file is removed before Resolve returns. No static AWS keys are stored or
// accepted, anywhere.
func Resolve(ctx context.Context, id Identity) (Credentials, error) {
	if strings.TrimSpace(id.RoleARN) == "" {
		return Credentials{}, errors.New("cloudid: RoleARN is required")
	}
	if strings.TrimSpace(id.WebIdentityToken) == "" {
		return Credentials{}, errors.New("cloudid: WebIdentityToken is required")
	}
	if strings.TrimSpace(id.Region) == "" {
		// MIT-1132: no default. The caller holds the subscription's pinned
		// region; a default here is a value that can change under a live app.
		return Credentials{}, errors.New("cloudid: Region is required (use the subscription's pinned region, never a package default)")
	}

	tf, err := os.CreateTemp("", "wi-token-*")
	if err != nil {
		return Credentials{}, fmt.Errorf("cloudid: create web-identity token file: %w", err)
	}
	defer os.Remove(tf.Name())
	if _, err := tf.WriteString(id.WebIdentityToken); err != nil {
		tf.Close()
		return Credentials{}, fmt.Errorf("cloudid: write web-identity token file: %w", err)
	}
	tf.Close()

	sessionName := "mitstack"
	if id.SessionSuffix != "" {
		sessionName += "-" + id.SessionSuffix
	}

	cmd := exec.CommandContext(ctx, "aws", "configure", "export-credentials", "--format", "process")
	cmd.Env = append(os.Environ(),
		"AWS_ROLE_ARN="+id.RoleARN,
		"AWS_WEB_IDENTITY_TOKEN_FILE="+tf.Name(),
		"AWS_ROLE_SESSION_NAME="+sessionName,
		"AWS_REGION="+id.Region,
		"AWS_DEFAULT_REGION="+id.Region,
	)

	out, err := cmd.Output()
	if err != nil {
		// Surface the CLI's stderr — it carries the error code and role ARN
		// (AccessDenied, InvalidIdentityToken, ...) and never the token, which
		// is delivered by file and never echoed. A bare failure here is #436.
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return Credentials{}, fmt.Errorf("cloudid: assume-role-with-web-identity: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return Credentials{}, fmt.Errorf("cloudid: assume-role-with-web-identity: %w", err)
	}

	var got exportedCredentials
	if err := json.Unmarshal(out, &got); err != nil {
		return Credentials{}, fmt.Errorf("cloudid: parse export-credentials output: %w", err)
	}
	if got.AccessKeyID == "" || got.SecretAccessKey == "" {
		// A malformed or truncated CLI response could otherwise be cached and
		// served as a usable credential by the Refresher for the full renewal
		// window, rather than causing every call to re-resolve.
		return Credentials{}, errors.New("cloudid: assume-role-with-web-identity returned an incomplete credential")
	}

	creds := Credentials{
		AccessKeyID:     got.AccessKeyID,
		SecretAccessKey: got.SecretAccessKey,
		SessionToken:    got.SessionToken,
		Region:          id.Region,
	}
	if got.Expiration != "" {
		if exp, err := time.Parse(time.RFC3339, got.Expiration); err == nil {
			creds.Expires = exp
		}
	}
	return creds, nil
}

// Env returns an environment slice carrying the credentials, suitable for
// exec.Cmd.Env when shelling out to an AWS-authenticated subprocess.
func Env(c Credentials) []string {
	return append(os.Environ(),
		"AWS_ACCESS_KEY_ID="+c.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY="+c.SecretAccessKey,
		"AWS_SESSION_TOKEN="+c.SessionToken,
		"AWS_REGION="+c.Region,
		"AWS_DEFAULT_REGION="+c.Region,
	)
}

// RenewMargin is how long before expiry a Refresher renews. It is generous
// because the failure it avoids — a credential that dies partway through a
// provisioning run — is far more expensive than an early renewal.
const RenewMargin = credrefresh.DefaultRenewMargin

// Refresher holds credentials for work that outlives a single webhook.
//
// This is the mechanism MIT-889 built for the Crossplane twin, whose reconcile
// loop runs continuously while the web-identity token lives about an hour. It
// is here because every reconciler-shaped block has the same problem, and the
// property that matters is easy to get wrong:
//
// **It fails closed.** When the credential has expired and renewal fails, Get
// returns an error and NO credentials. It never returns the stale ones. A
// reconcile loop acting on a dead credential does not merely error — it can
// take a wrong or destructive action against a consumer's account, and "we
// returned the last known good value" is exactly how that happens.
//
// It is a thin adapter over credrefresh, which owns the renewal mechanics —
// this type only knows how to turn a Credentials value into the (value,
// expiry, error) shape that package generalises over.
type Refresher struct {
	// Now is injectable so the expiry behaviour is testable without sleeping.
	Now func() time.Time

	inner *credrefresh.Refresher[Credentials]
}

// NewRefresher returns a Refresher that mints credentials through resolve.
func NewRefresher(resolve func(context.Context) (Credentials, error)) *Refresher {
	r := &Refresher{}
	r.inner = credrefresh.New(func(ctx context.Context) (Credentials, time.Time, error) {
		c, err := resolve(ctx)
		return c, c.Expires, err
	})
	// r.Now is read through this closure on every inner Get call, so setting
	// r.Now after construction — or between calls — still reaches it.
	r.inner.Now = func() time.Time {
		if r.Now != nil {
			return r.Now()
		}
		return time.Now()
	}
	return r
}

// FromIdentity returns a Refresher that re-resolves the given identity. The
// identity's web-identity token is itself short-lived, so callers whose token
// is refreshed out-of-band (e.g. by a periodic revalidate webhook) should use
// NewRefresher with a closure that reads the CURRENT identity instead.
func FromIdentity(id Identity) *Refresher {
	return NewRefresher(func(ctx context.Context) (Credentials, error) { return Resolve(ctx, id) })
}

// Get returns usable credentials, renewing when they are absent, inside the
// renewal margin, or of unknown expiry. It never returns expired credentials.
func (r *Refresher) Get(ctx context.Context) (Credentials, error) {
	creds, err := r.inner.Get(ctx)
	if err != nil {
		return Credentials{}, fmt.Errorf("cloudid: refreshing credentials (failing closed, no stale credential returned): %w", err)
	}
	return creds, nil
}

// --- Typed failures ---------------------------------------------------------

// The failures an operator can predict and must be able to act on. Each maps to
// an actionable statusDetail rather than an opaque 422 (#436).
var (
	// ErrQuotaExceeded — an account limit was hit. Reclaim your own orphans
	// before giving up (T-602).
	ErrQuotaExceeded = errors.New("aws account quota exceeded")
	// ErrNoDefaultVPC — the account has no default VPC; provision a fallback
	// network rather than failing (MIT-1114).
	ErrNoDefaultVPC = errors.New("aws account has no default vpc")
	// ErrInstanceTypeUnavailable — the instance type is not offered in the
	// chosen AZ; pick a subnet in an AZ that offers it (MIT-1112).
	ErrInstanceTypeUnavailable = errors.New("instance type not available in the selected availability zone")
	// ErrNoSubnet — no usable subnet (MIT-1113).
	ErrNoSubnet = errors.New("no usable subnet")
)

var classifiers = []struct {
	pattern *regexp.Regexp
	class   error
}{
	{regexp.MustCompile(`(?i)AddressLimitExceeded|LimitExceeded|RequestLimitExceeded|MaxSpotInstanceCountExceeded|VcpuLimitExceeded|quota.*exceeded`), ErrQuotaExceeded},
	{regexp.MustCompile(`(?i)VPCIdNotSpecified|no default vpc`), ErrNoDefaultVPC},
	{regexp.MustCompile(`(?i)not supported in your requested Availability Zone|InvalidParameterValue.*instance type.*Availability Zone|Unsupported.*instance type`), ErrInstanceTypeUnavailable},
	{regexp.MustCompile(`(?i)InvalidSubnetID\.NotFound|no subnet|SubnetId.*does not exist`), ErrNoSubnet},
}

// Classify wraps err with a named class when it matches a failure an operator
// should handle specifically. An unrecognised error passes through unchanged —
// guessing would be worse than saying nothing — and the original message is
// always preserved, because the operator still needs something actionable to
// put in statusDetail.
func Classify(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, c := range classifiers {
		if c.pattern.MatchString(msg) {
			return fmt.Errorf("%w: %s", c.class, msg)
		}
	}
	return err
}
