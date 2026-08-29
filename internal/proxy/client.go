package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Proxy is a SOCKS endpoint returned by proxy-service.
type Proxy struct {
	ID       string
	Host     string
	Port     int
	Username string
	Password string
}

// Client talks to an external proxy-service (GET /proxy, POST /proxy/bad).
// A successful endpoint is reused until ReportBadProxy for that id.
const badProxyCooldown = 2 * time.Second

type Client struct {
	HTTP         *http.Client
	ServiceToken string

	mu            sync.Mutex
	cached        *Proxy
	socks         *http.Client
	socksInsecure bool
	socksTimeout  time.Duration
	lastBadID     string
	lastBadAt     time.Time
}

// NewClient returns a client with sane defaults when httpClient is nil.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{HTTP: httpClient}
}

type proxyResponse struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// WorkingProxy returns the cached SOCKS endpoint, or GET /proxy once on a miss.
// Concurrent callers share one in-flight allocation (lock held for the HTTP round-trip).
func (c *Client) WorkingProxy(ctx context.Context, serviceURL, targetURL string) (Proxy, error) {
	if c == nil || c.HTTP == nil {
		return Proxy{}, fmt.Errorf("proxy: client is not configured")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached != nil {
		return *c.cached, nil
	}
	p, err := c.fetchProxy(ctx, serviceURL, targetURL)
	if err != nil {
		return Proxy{}, err
	}
	cp := p
	c.cached = &cp
	return p, nil
}

// FetchGET performs GET via the cached SOCKS client for this working proxy.
func (c *Client) FetchGET(
	ctx context.Context,
	p Proxy,
	targetURL string,
	userAgent string,
	connectTimeout time.Duration,
	requestTimeout time.Duration,
	tlsInsecureSkipVerify bool,
) ([]byte, int, error) {
	if c == nil {
		return FetchGET(ctx, p, targetURL, userAgent, connectTimeout, requestTimeout, tlsInsecureSkipVerify)
	}
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil, 0, fmt.Errorf("proxy: target URL is empty")
	}
	if connectTimeout <= 0 {
		connectTimeout = 10 * time.Second
	}
	if requestTimeout <= 0 {
		requestTimeout = 25 * time.Second
	}
	client, err := c.socksClient(p, connectTimeout, requestTimeout, tlsInsecureSkipVerify)
	if err != nil {
		return nil, 0, err
	}
	return doSOCKSGet(ctx, client, targetURL, userAgent)
}

func (c *Client) socksClient(p Proxy, connectTimeout, requestTimeout time.Duration, tlsInsecure bool) (*http.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.socks != nil && c.cached != nil && c.cached.ID == p.ID && c.socksInsecure == tlsInsecure && c.socksTimeout == requestTimeout {
		return c.socks, nil
	}
	transport, err := SOCKS5Transport(p, connectTimeout, tlsInsecure)
	if err != nil {
		return nil, err
	}
	cl := &http.Client{Timeout: requestTimeout, Transport: transport}
	if c.cached != nil && c.cached.ID == p.ID {
		c.socks = cl
		c.socksInsecure = tlsInsecure
		c.socksTimeout = requestTimeout
	}
	return cl, nil
}

func (c *Client) dropWorkingLocked(proxyID string) {
	if c.cached != nil && (proxyID == "" || c.cached.ID == proxyID) {
		c.cached = nil
	}
	if c.socks != nil {
		c.socks.CloseIdleConnections()
		c.socks = nil
	}
}

// GetProxy requests a SOCKS proxy from serviceURL. targetURL is optional (?target=).
// Does not touch the working-proxy cache; prefer WorkingProxy for Telegram polls.
func (c *Client) GetProxy(ctx context.Context, serviceURL, targetURL string) (Proxy, error) {
	if c == nil || c.HTTP == nil {
		return Proxy{}, fmt.Errorf("proxy: client is not configured")
	}
	return c.fetchProxy(ctx, serviceURL, targetURL)
}

func (c *Client) fetchProxy(ctx context.Context, serviceURL, targetURL string) (Proxy, error) {
	if c == nil || c.HTTP == nil {
		return Proxy{}, fmt.Errorf("proxy: client is not configured")
	}
	base, err := normalizeServiceURL(serviceURL)
	if err != nil {
		return Proxy{}, err
	}
	u := base + "/proxy"
	if t := strings.TrimSpace(targetURL); t != "" {
		q := url.Values{}
		q.Set("target", t)
		u += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Proxy{}, fmt.Errorf("proxy: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if tok := strings.TrimSpace(c.ServiceToken); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Proxy{}, fmt.Errorf("proxy: get proxy: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Proxy{}, fmt.Errorf("proxy: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Proxy{}, fmt.Errorf("proxy: service returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var data proxyResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return Proxy{}, fmt.Errorf("proxy: invalid JSON: %w", err)
	}
	if strings.TrimSpace(data.Host) == "" || data.Port <= 0 || strings.TrimSpace(data.ID) == "" {
		return Proxy{}, fmt.Errorf("proxy: incomplete proxy response")
	}
	return Proxy{
		ID:       data.ID,
		Host:     data.Host,
		Port:     data.Port,
		Username: data.Username,
		Password: data.Password,
	}, nil
}

type badProxyRequest struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// ReportBadProxy notifies proxy-service that a proxy failed and drops it from the cache.
func (c *Client) ReportBadProxy(ctx context.Context, serviceURL, proxyID, reason string) error {
	if c == nil || c.HTTP == nil {
		return nil
	}
	proxyID = strings.TrimSpace(proxyID)
	if proxyID == "" {
		return nil
	}
	c.mu.Lock()
	c.dropWorkingLocked(proxyID)
	if c.lastBadID == proxyID && !c.lastBadAt.IsZero() && time.Since(c.lastBadAt) < badProxyCooldown {
		c.mu.Unlock()
		return nil
	}
	c.lastBadID = proxyID
	c.lastBadAt = time.Now()
	c.mu.Unlock()
	base, err := normalizeServiceURL(serviceURL)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(badProxyRequest{ID: proxyID, Reason: strings.TrimSpace(reason)})
	if err != nil {
		return fmt.Errorf("proxy: encode bad request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/proxy/bad", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("proxy: create bad request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if tok := strings.TrimSpace(c.ServiceToken); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("proxy: report bad: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("proxy: report bad status %d", resp.StatusCode)
	}
	return nil
}

func normalizeServiceURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", fmt.Errorf("proxy: service URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("proxy: invalid service URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("proxy: service URL must be http or https")
	}
	if u.Host == "" {
		return "", fmt.Errorf("proxy: service URL has no host")
	}
	if u.User != nil {
		return "", fmt.Errorf("proxy: service URL must not contain userinfo")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ServiceHost returns the hostname of a proxy-service URL (for SSRF allowlist).
func ServiceHost(serviceURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(serviceURL))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("proxy: invalid service URL host")
	}
	return strings.ToLower(u.Hostname()), nil
}
