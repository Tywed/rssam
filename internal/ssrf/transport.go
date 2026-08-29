package ssrf

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"
)

// HTTPClient returns an http.Client that dials only to DNS-resolved IPs validated by the guard.
func (g *Guard) HTTPClient(timeout time.Duration) *http.Client {
	return g.httpClient(timeout, g.tlsInsecureSkipVerify)
}

// HTTPClientForFetch returns a client for fetching a feed URL.
// Per-feed TLSInsecure is combined with the global FETCH_TLS_INSECURE setting.
func (g *Guard) HTTPClientForFetch(timeout time.Duration, feedTLSInsecure bool) *http.Client {
	return g.httpClient(timeout, g.tlsInsecureSkipVerify || feedTLSInsecure)
}

func (g *Guard) httpClient(timeout time.Duration, tlsInsecureSkipVerify bool) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	key := httpClientKey{timeout: timeout, insecure: tlsInsecureSkipVerify}
	g.clientsMu.Lock()
	if g.clients == nil {
		g.clients = make(map[httpClientKey]*http.Client)
	}
	if c, ok := g.clients[key]; ok {
		g.clientsMu.Unlock()
		return c
	}
	g.clientsMu.Unlock()

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return g.dialContext(ctx, network, addr)
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

func (g *Guard) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("ssrf dial: %w", err)
	}

	ips, err := g.resolveHost(ctx, host)
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
