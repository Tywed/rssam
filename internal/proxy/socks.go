package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// FetchGET performs an HTTP GET through SOCKS5 (no direct connection to target host).
func FetchGET(
	ctx context.Context,
	p Proxy,
	targetURL string,
	userAgent string,
	connectTimeout time.Duration,
	requestTimeout time.Duration,
	tlsInsecureSkipVerify bool,
) ([]byte, int, error) {
	if connectTimeout <= 0 {
		connectTimeout = 10 * time.Second
	}
	if requestTimeout <= 0 {
		requestTimeout = 25 * time.Second
	}
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil, 0, fmt.Errorf("proxy: target URL is empty")
	}

	transport, err := SOCKS5Transport(p, connectTimeout, tlsInsecureSkipVerify)
	if err != nil {
		return nil, 0, err
	}
	client := &http.Client{
		Timeout:   requestTimeout,
		Transport: transport,
	}
	return doSOCKSGet(ctx, client, targetURL, userAgent)
}

func doSOCKSGet(ctx context.Context, client *http.Client, targetURL, userAgent string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("proxy: create target request: %w", err)
	}
	if ua := strings.TrimSpace(userAgent); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("proxy: socks fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("proxy: read target body: %w", err)
	}
	return body, resp.StatusCode, nil
}

// SOCKS5Transport builds an http.Transport that dials via SOCKS5.
func SOCKS5Transport(p Proxy, connectTimeout time.Duration, tlsInsecureSkipVerify bool) (*http.Transport, error) {
	addr := net.JoinHostPort(strings.TrimSpace(p.Host), fmt.Sprintf("%d", p.Port))
	var auth *proxy.Auth
	if strings.TrimSpace(p.Username) != "" {
		auth = &proxy.Auth{User: p.Username, Password: p.Password}
	}
	base, err := proxy.SOCKS5("tcp", addr, auth, &net.Dialer{Timeout: connectTimeout})
	if err != nil {
		return nil, fmt.Errorf("proxy: socks5 dialer: %w", err)
	}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if cd, ok := base.(proxy.ContextDialer); ok {
				return cd.DialContext(ctx, network, address)
			}
			return dialWithContext(ctx, func() (net.Conn, error) {
				return base.Dial(network, address)
			})
		},
		TLSClientConfig:       tlsClientConfig(tlsInsecureSkipVerify),
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}, nil
}

func dialWithContext(ctx context.Context, dial func() (net.Conn, error)) (net.Conn, error) {
	type result struct {
		c   net.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := dial()
		ch <- result{c, err}
	}()
	select {
	case <-ctx.Done():
		go func() {
			r := <-ch
			if r.c != nil {
				_ = r.c.Close()
			}
		}()
		return nil, ctx.Err()
	case r := <-ch:
		return r.c, r.err
	}
}

func tlsClientConfig(insecure bool) *tls.Config {
	if !insecure {
		return nil
	}
	return &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in via FETCH_TLS_INSECURE
}
