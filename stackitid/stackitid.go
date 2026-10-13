// Package stackitid exchanges the platform-signed OIDC assertion for a short-lived STACKIT access token under RFC 8693, failing closed and never carrying either secret in an error.
package stackitid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/credrefresh"
)

const grantType = "urn:ietf:params:oauth:grant-type:token-exchange"

const subjectTokenType = "urn:ietf:params:oauth:token-type:jwt"

// Bounds the response read so a hostile endpoint cannot exhaust memory.
const maxResponseBody = 1 << 20 // 1 MiB

// Identity is what Resolve needs to exchange the platform's assertion for a STACKIT access token.
type Identity struct {
	// TokenURL is required and must be absolute https, since the assertion is a bearer credential sent in the request body.
	TokenURL string
	Audience string
	// Assertion is the platform-signed OIDC assertion and is never logged or placed in a URL or error.
	Assertion string
	Scope     string
	// Client is the injectable HTTP seam, defaulting to http.DefaultClient.
	Client *http.Client
}

// Credential is the access token resulting from the exchange.
type Credential struct {
	AccessToken string
	TokenType   string
	// Expires is when the token stops working; zero means unknown and is never read as no expiry.
	Expires time.Time
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

type errorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// ErrInvalidGrant names a rejected assertion so an operator can map it to a statusDetail.
var ErrInvalidGrant = errors.New("stackitid: assertion rejected (invalid_grant)")

// Resolve exchanges id's assertion for a STACKIT access token, validating before any network attempt.
func Resolve(ctx context.Context, id Identity) (Credential, error) {
	if strings.TrimSpace(id.TokenURL) == "" {
		return Credential{}, errors.New("stackitid: TokenURL is required")
	}
	if strings.TrimSpace(id.Audience) == "" {
		return Credential{}, errors.New("stackitid: Audience is required")
	}
	if strings.TrimSpace(id.Assertion) == "" {
		return Credential{}, errors.New("stackitid: Assertion is required")
	}

	parsed, err := url.Parse(id.TokenURL)
	if err != nil {
		return Credential{}, fmt.Errorf("stackitid: parsing TokenURL: %w", err)
	}
	if parsed.Scheme != "https" {
		return Credential{}, errors.New("stackitid: TokenURL must use https")
	}

	form := url.Values{
		"grant_type":         {grantType},
		"subject_token":      {id.Assertion},
		"subject_token_type": {subjectTokenType},
		"audience":           {id.Audience},
	}
	if id.Scope != "" {
		form.Set("scope", id.Scope)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, id.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Credential{}, fmt.Errorf("stackitid: building token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := id.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		// The transport error's text may contain request contents, so only ctx.Err() is wrapped.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Credential{}, fmt.Errorf("stackitid: token exchange request: %w", ctxErr)
		}
		return Credential{}, errors.New("stackitid: token exchange request failed")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return Credential{}, fmt.Errorf("stackitid: reading token exchange response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errBody errorResponse
		if jsonErr := json.Unmarshal(body, &errBody); jsonErr == nil && errBody.Error != "" {
			if errBody.Error == "invalid_grant" {
				return Credential{}, fmt.Errorf("%w: %s", ErrInvalidGrant, errBody.ErrorDescription)
			}
			return Credential{}, fmt.Errorf("stackitid: token exchange failed: %s: %s", errBody.Error, errBody.ErrorDescription)
		}
		return Credential{}, fmt.Errorf("stackitid: token exchange failed with status %d", resp.StatusCode)
	}

	var got tokenResponse
	if err := json.Unmarshal(body, &got); err != nil {
		return Credential{}, fmt.Errorf("stackitid: parsing token exchange response: %w", err)
	}
	if got.AccessToken == "" {
		// A truncated response must not be cached by the Refresher as a usable credential.
		return Credential{}, errors.New("stackitid: token exchange returned an incomplete credential")
	}

	cred := Credential{
		AccessToken: got.AccessToken,
		TokenType:   got.TokenType,
	}
	if got.ExpiresIn > 0 {
		cred.Expires = time.Now().Add(time.Duration(got.ExpiresIn) * time.Second)
	}
	return cred, nil
}

// Refresher holds a STACKIT access token across work that outlives a single exchange, and fails closed.
type Refresher struct {
	Now func() time.Time

	inner *credrefresh.Refresher[Credential]
}

// NewRefresher returns a Refresher that mints access tokens through resolve.
func NewRefresher(resolve func(context.Context) (Credential, error)) *Refresher {
	r := &Refresher{}
	r.inner = credrefresh.New(func(ctx context.Context) (Credential, time.Time, error) {
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

// FromIdentity returns a Refresher that re-resolves the given identity.
func FromIdentity(id Identity) *Refresher {
	return NewRefresher(func(ctx context.Context) (Credential, error) { return Resolve(ctx, id) })
}

// Get returns a usable access token, renewing when absent, inside the renewal margin or of unknown expiry, and never returns an expired one.
func (r *Refresher) Get(ctx context.Context) (Credential, error) {
	cred, err := r.inner.Get(ctx)
	if err != nil {
		return Credential{}, fmt.Errorf("stackitid: refreshing access token (failing closed, no stale credential returned): %w", err)
	}
	return cred, nil
}
