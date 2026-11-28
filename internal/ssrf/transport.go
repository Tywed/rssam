package ssrf

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// HTTPClient returns an http.Client that dials only to DNS-resolved IPs validated by the guard.
func (g *Guard) HTTPClient(timeout time.Duration) *http.Client {
	return g.httpClient(timeout, g.tlsInsecureSkipVerify, nil, nil)
}

// HTTPClientForFetch returns a client for fetching a feed URL.
// Per-feed TLSInsecure is combined with the global FETCH_TLS_INSECURE setting.
func (g *Guard) HTTPClientForFetch(timeout time.Duration, feedTLSInsecure bool) *http.Client {
	return g.httpClient(timeout, g.tlsInsecureSkipVerify || feedTLSInsecure, nil, nil)
}

// HTTPClientNoRedirect is HTTPClient for requests that carry credentials
// (webhook deliveries with their headers and signature): a redirect is
// returned as the response instead of being followed to another host.
func (g *Guard) HTTPClientNoRedirect(timeout time.Duration) *http.Client {
	c := *g.HTTPClient(timeout)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// HTTPClientViaProxy returns a client that sends every request through
// proxyURL (http, https or socks5 — FETCH_VIA_PROXY_URL, operator
// configuration). The transport dials the proxy, not the target, so the
// target of every request — and of every redirect, which the transport
// also routes through Proxy — is validated by the guard before the proxy
// is named; the proxy's own address is dialled as operator configuration,
// private networks included.
func (g *Guard) HTTPClientViaProxy(timeout time.Duration, feedTLSInsecure bool, proxyURL string) (*http.Client, error) {
	u, err := url.Parse(strings.TrimSpace(proxyURL))
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid proxy URL %q", proxyURL)
	}
	proxyHost := strings.ToLower(u.Hostname())
	proxy := func(req *http.Request) (*url.URL, error) {
		if _, err := g.resolveHost(req.Context(), req.URL.Hostname()); err != nil {
			return nil, err
		}
		return u, nil
	}
	exempt := func(host, _ string) bool { return strings.ToLower(host) == proxyHost }
	return g.httpClient(timeout, g.tlsInsecureSkipVerify || feedTLSInsecure, proxy, exempt), nil
}

// HTTPClientForEndpoint returns a client that may dial one operator-
// configured host:port on a private network (the Telegram proxy-service)
// and nothing else private. Only this client carries the exemption: a feed
// or webhook URL naming the same host — or the same host on another port —
// goes through the ordinary guard and is refused.
func (g *Guard) HTTPClientForEndpoint(timeout time.Duration, endpoint string) *http.Client {
	host, port, err := net.SplitHostPort(strings.ToLower(strings.TrimSpace(endpoint)))
	if err != nil || host == "" || port == "" {
		return g.HTTPClient(timeout)
	}
	exempt := func(h, p string) bool { return strings.ToLower(h) == host && p == port }
	c := g.httpClient(timeout, g.tlsInsecureSkipVerify, nil, exempt)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// redirectLimit is how many hops a fetch follows: enough for the usual
// http→https→www chain, not for a loop.
const redirectLimit = 5

func (g *Guard) httpClient(timeout time.Duration, tlsInsecureSkipVerify bool, proxy func(*http.Request) (*url.URL, error), exempt func(host, port string) bool) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	cacheable := proxy == nil && exempt == nil
	key := httpClientKey{timeout: timeout, insecure: tlsInsecureSkipVerify}
	if cacheable {
		g.clientsMu.Lock()
		if g.clients == nil {
			g.clients = make(map[httpClientKey]*http.Client)
		}
		if c, ok := g.clients[key]; ok {
			g.clientsMu.Unlock()
			return c
		}
		g.clientsMu.Unlock()
	}
	if proxy == nil {
		proxy = g.proxyFromEnvironment
	}
	transport := &http.Transport{
		Proxy: proxy,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return g.dialContext(ctx, network, addr, exempt)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if tlsInsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in per feed or FETCH_TLS_INSECURE
	}
	c := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= redirectLimit {
				return fmt.Errorf("stopped after %d redirects", redirectLimit)
			}
			_, err := g.resolveHost(req.Context(), req.URL.Hostname())
			return err
		},
	}
	if !cacheable {
		return c
	}
	g.clientsMu.Lock()
	if existing, ok := g.clients[key]; ok {
		g.clientsMu.Unlock()
		return existing
	}
	g.clients[key] = c
	g.clientsMu.Unlock()
	return c
}

// proxyFromEnvironment honours HTTP(S)_PROXY/NO_PROXY like the default
// transport, with one difference: when a proxy applies, the transport dials
// the proxy instead of the target, so dialContext never sees the target
// address. The target is therefore validated here — a proxied request to a
// private address is refused the same way a direct one is. The proxy's own
// address is exempt from the private-network check: it is operator
// configuration, not feed input.
func (g *Guard) proxyFromEnvironment(req *http.Request) (*url.URL, error) {
	proxyURL, err := http.ProxyFromEnvironment(req)
	if err != nil || proxyURL == nil {
		return proxyURL, err
	}
	if _, err := g.resolveHost(req.Context(), req.URL.Hostname()); err != nil {
		return nil, err
	}
	return proxyURL, nil
}

// proxyEndpoints are the host:port endpoints of HTTP(S)_PROXY, read once:
// connections to them are made on the operator's behalf, not the feed's.
// The exemption is for the endpoint, not the host: with
// HTTP_PROXY=http://127.0.0.1:3128 a feed URL naming 127.0.0.1 on another
// port is not a proxy connection — Go sends loopback and NO_PROXY hosts
// directly, so that request would have reached the guard's dialer with the
// host exempt and skipped the private-network check (the class of hole
// Miniflux closed in b6fe593, 2026-11-23).
var proxyEndpoints = func() map[string]struct{} {
	out := map[string]struct{}{}
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		v := os.Getenv(key)
		if v == "" {
			continue
		}
		if !strings.Contains(v, "://") {
			v = "http://" + v
		}
		if ep := proxyEndpoint(v); ep != "" {
			out[ep] = struct{}{}
		}
	}
	return out
}()

// proxyEndpoint reduces a proxy URL to the lower-case host:port the
// transport dials, defaulting the port by scheme as net/http does.
func proxyEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func (g *Guard) dialContext(ctx context.Context, network, addr string, exempt func(host, port string) bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("ssrf dial: %w", err)
	}

	var ips []net.IP
	if _, isProxy := proxyEndpoints[net.JoinHostPort(strings.ToLower(host), port)]; isProxy || (exempt != nil && exempt(host, port)) {
		ips, err = g.lookupIPs(ctx, host)
	} else {
		ips, err = g.resolveHost(ctx, host)
	}
	if err != nil {
		return nil, err
	}

	var d net.Dialer
	var lastErr error
	for _, ip := range ips {
		dialAddr := net.JoinHostPort(ip.String(), port)
		conn, dialErr := d.DialContext(ctx, network, dialAddr)
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("ssrf dial: no addresses to connect")
}
