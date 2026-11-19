package client

import (
	"net"
	"net/url"
	"strings"
	"sync"
)

// The CLI only attaches credentials to, or performs arbitrary fetches against,
// origins that were explicitly configured. Origins are normalized to
// "scheme://host[:port]" so a key can never be sent to a look-alike host or
// to a different port on an otherwise trusted host.
var (
	allowedOriginsMu sync.RWMutex
	allowedOrigins   = map[string]struct{}{}
)

// SetAllowedOrigins replaces the set of origins the CLI is allowed to contact
// with credentials or as an arbitrary fetch target. Each argument may be a
// full URL or a bare origin; only its scheme, host and port are used. An empty
// set is the safe default and refuses every host.
func SetAllowedOrigins(rawURLs ...string) {
	origins := make(map[string]struct{}, len(rawURLs))
	for _, raw := range rawURLs {
		if origin, ok := normalizeOrigin(raw); ok {
			origins[origin] = struct{}{}
		}
	}
	allowedOriginsMu.Lock()
	allowedOrigins = origins
	allowedOriginsMu.Unlock()
}

// IsAllowedURL reports whether raw is an http(s) URL whose origin is in the
// configured allowlist. It is used to gate arbitrary fetch targets such as
// the cortex ingest --url flag, preventing SSRF to unconfigured hosts.
func IsAllowedURL(raw string) bool {
	origin, ok := normalizeOrigin(raw)
	if !ok {
		return false
	}
	return isAllowedOrigin(origin)
}

func isAllowedOrigin(origin string) bool {
	allowedOriginsMu.RLock()
	defer allowedOriginsMu.RUnlock()
	_, ok := allowedOrigins[origin]
	return ok
}

func normalizeOrigin(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	hostname := strings.ToLower(u.Hostname())
	if hostname == "" {
		return "", false
	}
	host := hostname
	if port := u.Port(); port != "" && !isDefaultPort(scheme, port) {
		host = net.JoinHostPort(hostname, port)
	}
	return scheme + "://" + host, true
}

func isDefaultPort(scheme, port string) bool {
	return (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
}
