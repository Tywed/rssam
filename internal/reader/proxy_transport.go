package reader

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"rssam/internal/ssrf"
)

type proxyClientCache struct {
	mu     sync.Mutex
	proxy  string
	direct map[bool]*http.Client
	via    map[bool]*http.Client
}

func newProxyClientCache(proxyURL string) *proxyClientCache {
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
		base := fallback
		if guard != nil {
			base = guard.HTTPClientForFetch(timeout, tlsInsecure)
		}
		c.direct[tlsInsecure] = base
		return base, nil
	}
	if cl, ok := c.via[tlsInsecure]; ok {
		return cl, nil
	}
	if guard == nil {
		return nil, fmt.Errorf("fetch via proxy requires the ssrf guard")
	}
	cl, err := guard.HTTPClientViaProxy(timeout, tlsInsecure, c.proxy)
	if err != nil {
		return nil, err
	}
	c.via[tlsInsecure] = cl
	return cl, nil
}
