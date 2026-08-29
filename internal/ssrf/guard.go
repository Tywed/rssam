package ssrf

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Config controls SSRF validation for outbound HTTP fetches.
type Config struct {
	AllowPrivateNetwork bool
	AllowedCIDRs        []string
	BlockedHosts        []string
	// TLSInsecureSkipVerify disables TLS certificate verification for outbound fetches.
	TLSInsecureSkipVerify bool
	// AllowedHosts bypasses private-IP checks for these hostnames (e.g. internal proxy-service).
	AllowedHosts []string
	Resolver     *net.Resolver
	// LookupHost overrides DNS resolution (tests); nil uses Resolver or net.DefaultResolver.
	LookupHost func(ctx context.Context, host string) ([]string, error)
}

// Guard validates URLs before server-side HTTP requests.
type Guard struct {
	allowPrivate          bool
	tlsInsecureSkipVerify bool
	allowedNets           []*net.IPNet
	blockedNets           []*net.IPNet
	blockedHosts          map[string]struct{}
	allowedHosts          map[string]struct{}
	resolver              *net.Resolver
	lookupHost            func(ctx context.Context, host string) ([]string, error)

	clientsMu sync.Mutex
	clients   map[httpClientKey]*http.Client
}

type httpClientKey struct {
	timeout  time.Duration
	insecure bool
}

var defaultBlockedCIDRs = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"100.64.0.0/10",
	"0.0.0.0/8",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
}

func New(cfg Config) (*Guard, error) {
	g := &Guard{
		allowPrivate:          cfg.AllowPrivateNetwork,
		tlsInsecureSkipVerify: cfg.TLSInsecureSkipVerify,
		resolver:              cfg.Resolver,
		lookupHost:            cfg.LookupHost,
	}
	if g.resolver == nil {
		g.resolver = net.DefaultResolver
	}
	for _, cidr := range defaultBlockedCIDRs {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("parse blocked cidr %q: %w", cidr, err)
		}
		g.blockedNets = append(g.blockedNets, n)
	}
	for _, cidr := range cfg.AllowedCIDRs {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("parse allowed cidr %q: %w", cidr, err)
		}
		g.allowedNets = append(g.allowedNets, n)
	}
	g.blockedHosts = make(map[string]struct{})
	for _, h := range cfg.BlockedHosts {
		h = normalizeHost(h)
		if h != "" {
			g.blockedHosts[h] = struct{}{}
		}
	}
	g.allowedHosts = make(map[string]struct{})
	for _, h := range cfg.AllowedHosts {
		h = normalizeHost(h)
		if h != "" {
			g.allowedHosts[h] = struct{}{}
		}
	}
	return g, nil
}

// ValidateURL ensures the URL is safe to fetch (http/https, DNS rebinding checks).
func (g *Guard) ValidateURL(rawURL string) error {
	u, err := g.parseURL(rawURL)
	if err != nil {
		return err
	}
	_, err = g.resolveHost(context.Background(), u.Hostname())
	return err
}

// Validate is an alias for ValidateURL.
func (g *Guard) Validate(rawURL string) error {
	return g.ValidateURL(rawURL)
}

func (g *Guard) parseURL(rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("ssrf: empty url")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("ssrf: invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("ssrf: only http/https schemes are allowed")
	}
	if u.User != nil {
		return nil, fmt.Errorf("ssrf: url must not contain userinfo")
	}
	hostname := u.Hostname()
	if hostname == "" {
		return nil, fmt.Errorf("ssrf: empty hostname")
	}
	if err := g.checkBlockedHost(hostname); err != nil {
		return nil, err
	}
	return u, nil
}

func (g *Guard) checkBlockedHost(hostname string) error {
	host := normalizeHost(hostname)
	if host == "" {
		return fmt.Errorf("ssrf: empty hostname")
	}
	if _, allowed := g.allowedHosts[host]; allowed {
		return nil
	}
	if _, blocked := g.blockedHosts[host]; blocked {
		return fmt.Errorf("ssrf: blocked host: %s", hostname)
	}
	return nil
}

func (g *Guard) resolveHost(ctx context.Context, hostname string) ([]net.IP, error) {
	if err := g.checkBlockedHost(hostname); err != nil {
		return nil, err
	}

	if ip := net.ParseIP(hostname); ip != nil {
		if !g.isAllowedHost(hostname) {
			if err := g.validateIP(ip); err != nil {
				return nil, err
			}
		}
		return []net.IP{ip}, nil
	}

	var addrs []string
	var err error
	if g.lookupHost != nil {
		addrs, err = g.lookupHost(ctx, hostname)
	} else {
		addrs, err = g.resolver.LookupHost(ctx, hostname)
	}
	if err != nil {
		return nil, fmt.Errorf("ssrf: dns lookup failed: %w", err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("ssrf: no addresses for host")
	}

	hostAllowed := g.isAllowedHost(hostname)

	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if !hostAllowed {
			if err := g.validateIP(ip); err != nil {
				return nil, err
			}
		}
		ips = append(ips, ip)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("ssrf: no valid addresses for host")
	}
	return ips, nil
}

func (g *Guard) validateIP(ip net.IP) error {
	ip = ip.To16()
	if ip == nil {
		return fmt.Errorf("ssrf: invalid ip")
	}
	if g.allowPrivate {
		return nil
	}
	for _, n := range g.allowedNets {
		if n.Contains(ip) {
			return nil
		}
	}
	for _, n := range g.blockedNets {
		if n.Contains(ip) {
			return fmt.Errorf("ssrf: blocked ip range: %s", ip.String())
		}
	}
	return nil
}

func (g *Guard) isAllowedHost(hostname string) bool {
	if g == nil {
		return false
	}
	_, ok := g.allowedHosts[normalizeHost(hostname)]
	return ok
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	}
	return strings.TrimSuffix(host, ".")
}
