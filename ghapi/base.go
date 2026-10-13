// Package ghapi is the GitHub REST API client for operators, whose host is the operator's decision and always resolved over https with no loopback or http carve-out.
package ghapi

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/mitstack-eu/operator-kit/v2/safe"
)

const APIGitHubHost = "api.github.com"

const HostsEnv = "MITSTACK_GITHUB_API_HOSTS"

func BaseFromEnv(endpoint string) (safe.Base, error) {
	return Base(endpoint, strings.Split(os.Getenv(HostsEnv), ",")...)
}

// Base builds its safe.Base from the allowlist entry, never from endpoint, and refuses a private, loopback, bare-IP, localhost or metadata host even when the operator listed it.
func Base(endpoint string, extraHosts ...string) (safe.Base, error) {
	allow := safe.NewAllowlist(append([]string{APIGitHubHost}, extraHosts...)...)
	b, err := safe.TrustedBase(endpoint, allow)
	if err != nil {
		return safe.Base{}, err
	}
	host := strings.TrimPrefix(b.String(), "https://")
	if !isPublicHost(host) {
		return safe.Base{}, fmt.Errorf("ghapi: host %q is not an acceptable GitHub API host — private, loopback, link-local, metadata and bare-IP hosts are refused even when operator-allowlisted", host)
	}
	return b, nil
}

// isPublicHost does not resolve DNS on purpose, because the address at connect time can differ and egress restrictions bound where a request lands; brackets and a port are stripped so "[::1]" is caught.
func isPublicHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return false
	}
	if hostOnly, _, err := net.SplitHostPort(h); err == nil {
		h = hostOnly
	}
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")

	if h == "localhost" || strings.Contains(h, "metadata") {
		return false
	}
	if net.ParseIP(h) != nil {
		return false
	}
	return true
}
