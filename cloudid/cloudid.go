// Package cloudid, an optional helper, turns the CloudIdentity delivered in a signed lifecycle webhook into short-lived AWS credentials, and is AWS-specific on purpose.
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

	"github.com/mitstack-eu/operator-kit/v2/credrefresh"
)

type Identity struct {
	RoleARN string
	// WebIdentityToken is written to a temp file and never placed on a command line.
	WebIdentityToken string
	// Region is the subscription's pinned region and is required, because a package default could shift under a live app.
	Region string
	// SessionSuffix names the subscription in the session so CloudTrail attributes actions to the right app.
	SessionSuffix string
}

type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
	// Expires is when these credentials stop working; zero means unknown and is never read as no expiry.
	Expires time.Time
}

type exportedCredentials struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}

var accountIDPattern = regexp.MustCompile(`^\d{12}$`)

// ProviderARN builds the IAM OIDC provider ARN for an issuer in an account, without the iam:ListOpenIDConnectProviders call that some accounts deny.
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

// Resolve assumes the identity's role and returns the temporary credentials, passing the token by file because the sts subcommand would put it on the command line.
func Resolve(ctx context.Context, id Identity) (Credentials, error) {
	if strings.TrimSpace(id.RoleARN) == "" {
		return Credentials{}, errors.New("cloudid: RoleARN is required")
	}
	if strings.TrimSpace(id.WebIdentityToken) == "" {
		return Credentials{}, errors.New("cloudid: WebIdentityToken is required")
	}
	if strings.TrimSpace(id.Region) == "" {
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
		// The CLI's stderr carries the error code and role ARN and never the token, so it is safe to surface.
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
		// A truncated response must not be cached by the Refresher as a usable credential.
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

func Env(c Credentials) []string {
	return append(os.Environ(),
		"AWS_ACCESS_KEY_ID="+c.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY="+c.SecretAccessKey,
		"AWS_SESSION_TOKEN="+c.SessionToken,
		"AWS_REGION="+c.Region,
		"AWS_DEFAULT_REGION="+c.Region,
	)
}

// RenewMargin is how long before expiry a Refresher renews, early because a credential dying mid-run costs more than an early renewal.
const RenewMargin = credrefresh.DefaultRenewMargin

// Refresher holds credentials for work that outlives a single webhook and fails closed, returning an error and no credentials rather than a stale one.
type Refresher struct {
	Now func() time.Time

	inner *credrefresh.Refresher[Credentials]
}

func NewRefresher(resolve func(context.Context) (Credentials, error)) *Refresher {
	r := &Refresher{}
	r.inner = credrefresh.New(func(ctx context.Context) (Credentials, time.Time, error) {
		c, err := resolve(ctx)
		return c, c.Expires, err
	})
	// Read through a closure so setting r.Now after construction still reaches the inner refresher.
	r.inner.Now = func() time.Time {
		if r.Now != nil {
			return r.Now()
		}
		return time.Now()
	}
	return r
}

// FromIdentity returns a Refresher that re-resolves the given identity, whose short-lived token callers refreshed out-of-band must replace by using NewRefresher.
func FromIdentity(id Identity) *Refresher {
	return NewRefresher(func(ctx context.Context) (Credentials, error) { return Resolve(ctx, id) })
}

// Get returns usable credentials, renewing when absent, inside the renewal margin or of unknown expiry, and never returns expired ones.
func (r *Refresher) Get(ctx context.Context) (Credentials, error) {
	creds, err := r.inner.Get(ctx)
	if err != nil {
		return Credentials{}, fmt.Errorf("cloudid: refreshing credentials (failing closed, no stale credential returned): %w", err)
	}
	return creds, nil
}

var (
	ErrQuotaExceeded           = errors.New("aws account quota exceeded")
	ErrNoDefaultVPC            = errors.New("aws account has no default vpc")
	ErrInstanceTypeUnavailable = errors.New("instance type not available in the selected availability zone")
	ErrNoSubnet                = errors.New("no usable subnet")
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

// Classify wraps err with a named class when it matches a predictable failure, passing an unrecognised error through unchanged and keeping the original message.
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
