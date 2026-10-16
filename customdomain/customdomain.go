// Package customdomain, an optional helper, validates the consumer-supplied vanity hostname, which is data that must never reach a rendered Caddy or ACME config unless it is a bare hostname.
package customdomain

import (
	"fmt"
	"strings"
)

const (
	// Presentation-form limits: 253 total (browsers and ACME clients) and 63 per label.
	maxHostnameLength = 253
	maxLabelLength    = 63
)

// Validate rejects anything that is not a bare RFC-1035 hostname, with empty as the always-valid opt-out.
func Validate(domain string) error {
	if domain == "" {
		return nil
	}

	for _, r := range domain {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("customDomain must not contain control characters (found %q)", r)
		}
	}
	if strings.ContainsAny(domain, " \t{}") {
		return fmt.Errorf("customDomain must not contain spaces or braces — it must be a bare hostname, not config")
	}
	if strings.Contains(domain, "://") {
		return fmt.Errorf("customDomain must not include a URL scheme — pass a bare hostname")
	}
	if strings.ContainsAny(domain, "/:") {
		return fmt.Errorf("customDomain must be a bare hostname, not a URL with a path or port")
	}
	if strings.Contains(domain, "*") {
		return fmt.Errorf("customDomain must not be a wildcard")
	}
	if len(domain) > maxHostnameLength {
		return fmt.Errorf("customDomain exceeds the maximum hostname length of %d characters", maxHostnameLength)
	}

	for _, label := range strings.Split(domain, ".") {
		if err := validateLabel(label); err != nil {
			return err
		}
	}
	return nil
}

func validateLabel(label string) error {
	if label == "" {
		return fmt.Errorf("customDomain must not contain an empty label (leading dot, trailing dot, or double dot)")
	}
	if len(label) > maxLabelLength {
		return fmt.Errorf("customDomain label %q exceeds the maximum length of %d characters", label, maxLabelLength)
	}
	for i, r := range label {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-':
			if i == 0 || i == len(label)-1 {
				return fmt.Errorf("customDomain label %q must not start or end with a hyphen", label)
			}
		default:
			return fmt.Errorf("customDomain label %q contains an invalid character %q", label, r)
		}
	}
	return nil
}
