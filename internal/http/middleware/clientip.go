package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
)

// trustedProxies holds the CIDR list of proxies whose X-Forwarded-For /
// X-Real-IP headers may be believed. Any peer outside this list is treated as
// the real client and its forwarding headers are ignored — otherwise a remote
// attacker can rotate X-Forwarded-For and bypass per-IP rate limiting.
var trustedProxies atomic.Pointer[[]netip.Prefix]

// DefaultTrustedProxies covers a reverse proxy running on the same host,
// which is the layout install.sh / docker-compose produce.
var DefaultTrustedProxies = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
}

func init() {
	p := append([]netip.Prefix(nil), DefaultTrustedProxies...)
	trustedProxies.Store(&p)
}

// SetTrustedProxies replaces the trusted proxy list. Entries may be single IPs
// or CIDRs. An empty list disables proxy headers entirely.
func SetTrustedProxies(entries []string) error {
	out := make([]netip.Prefix, 0, len(entries))
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if strings.Contains(e, "/") {
			p, err := netip.ParsePrefix(e)
			if err != nil {
				return err
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(e)
		if err != nil {
			return err
		}
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	trustedProxies.Store(&out)
	return nil
}

// TrustedProxies returns the active list (for logging/diagnostics).
func TrustedProxies() []netip.Prefix {
	if p := trustedProxies.Load(); p != nil {
		return *p
	}
	return nil
}

func isTrustedProxy(addr netip.Addr) bool {
	p := trustedProxies.Load()
	if p == nil {
		return false
	}
	addr = addr.Unmap()
	for _, pre := range *p {
		if pre.Contains(addr) {
			return true
		}
	}
	return false
}

func remoteAddr(r *http.Request) (string, netip.Addr, bool) {
	raw := strings.TrimSpace(r.RemoteAddr)
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		host = raw
	}
	a, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return host, netip.Addr{}, false
	}
	return host, a, true
}

// ClientIP returns the client IP for rate limiting and access logs.
//
// Forwarding headers are only honoured when the direct peer is a trusted
// proxy. X-Forwarded-For is walked right-to-left: every trusted hop is
// skipped and the first untrusted address is the client, so an attacker
// cannot prepend arbitrary values.
func ClientIP(r *http.Request) string {
	host, peer, ok := remoteAddr(r)
	if !ok || !isTrustedProxy(peer) {
		return host
	}

	if xff := r.Header.Get("X-Forwarded-For"); strings.TrimSpace(xff) != "" {
		parts := strings.Split(xff, ",")
		for _, part := range slices.Backward(parts) {
			cand := strings.TrimSpace(part)
			a, err := netip.ParseAddr(strings.Trim(cand, "[]"))
			if err != nil {
				break // malformed hop: stop trusting the chain
			}
			if isTrustedProxy(a) {
				continue
			}
			return a.Unmap().String()
		}
		// Every hop was a trusted proxy (or chain malformed): fall through.
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		if a, err := netip.ParseAddr(strings.Trim(xri, "[]")); err == nil {
			return a.Unmap().String()
		}
	}
	return host
}

// RequestHost returns the host the client addressed. Behind a trusted reverse
// proxy that rewrites Host to the upstream address (nginx proxy_pass default),
// X-Forwarded-Host carries the original value; it is only honoured when the
// direct peer is a trusted proxy, like the other forwarding headers.
func RequestHost(r *http.Request) string {
	if _, peer, ok := remoteAddr(r); ok && isTrustedProxy(peer) {
		if xfh := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); xfh != "" {
			// Multiple proxies append comma-separated values; the first is the client-facing one.
			if i := strings.IndexByte(xfh, ','); i >= 0 {
				xfh = strings.TrimSpace(xfh[:i])
			}
			return xfh
		}
	}
	return r.Host
}
