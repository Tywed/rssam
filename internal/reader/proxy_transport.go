package reader

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"rssam/internal/ssrf"
)

// proxyHTTPClient returns an http.Client that routes through proxyURL (http/https/socks5).
func proxyHTTPClient(base *http.Client, proxyURL string) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return base, nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if rt, ok := transport.(*http.Transport); ok {
		cp := rt.Clone()
		cp.Proxy = http.ProxyURL(u)
		cp.ForceAttemptHTTP2 = false
		return &http.Client{
			Timeout:       base.Timeout,
			Transport:     cp,
			CheckRedirect: base.CheckRedirect,
		}, nil
	}
	return nil, fmt.Errorf("unsupported base transport for proxy")
}

type proxyClientCache struct {
	mu     sync.Mutex
	proxy  string
	direct map[bool]*http.Client
	via    map[bool]*http.Client
}

func newProxyClientCache(base *http.Client, proxyURL string) *proxyClientCache {
	return &proxyClientCache{
		proxy:  strings.TrimSpace(proxyURL),
		direct: make(map[bool]*http.Client),
		via:    make(map[bool]*http.Client),
	}
}

func (c *proxyClientCache) client(useProxy, tlsInsecure bool, guard *ssrf.Guard, fallback *http.Client, timeout time.Duration) (*http.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !useProxy || c.proxy == "" {
		if cl, ok := c.direct[tlsInsecure]; ok {
			return cl, nil
		}
		var base *http.Client
		if guard != nil {
			base = guard.HTTPClientForFetch(timeout, tlsInsecure)
		} else {
			base = fallback
		}
		c.direct[tlsInsecure] = base
		return base, nil
	}
	if cl, ok := c.via[tlsInsecure]; ok {
		return cl, nil
	}
	var base *http.Client
	if guard != nil {
		base = guard.HTTPClientForFetch(timeout, tlsInsecure)
	} else {
		base = fallback
	}
	cl, err := proxyHTTPClient(base, c.proxy)
	if err != nil {
		return nil, err
	}
	c.via[tlsInsecure] = cl
	return cl, nil
}
