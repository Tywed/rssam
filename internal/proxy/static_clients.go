package proxy

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// StaticClients keeps one HTTP client per static SOCKS endpoint so that
// polls through the same proxy reuse its TCP/TLS connections instead of
// opening a new transport (and a new SOCKS handshake, TCP and TLS
// connection) for every page. The set is small — one entry per distinct
// endpoint and timeout — and is emptied, idle connections closed, when it
// grows past staticClientsMax.
type StaticClients struct {
	mu      sync.Mutex
	clients map[staticClientKey]*http.Client
}

type staticClientKey struct {
	Proxy
	connectTimeout time.Duration
	requestTimeout time.Duration
	insecure       bool
}

const staticClientsMax = 64

// Client returns the shared client for p, building it on first use.
func (s *StaticClients) Client(p Proxy, connectTimeout, requestTimeout time.Duration, tlsInsecure bool) (*http.Client, error) {
	key := staticClientKey{Proxy: p, connectTimeout: connectTimeout, requestTimeout: requestTimeout, insecure: tlsInsecure}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.clients[key]; ok {
		return c, nil
	}
	transport, err := SOCKS5Transport(p, connectTimeout, tlsInsecure)
	if err != nil {
		return nil, err
	}
	if len(s.clients) >= staticClientsMax {
		for k, c := range s.clients {
			c.CloseIdleConnections()
			delete(s.clients, k)
		}
	}
	if s.clients == nil {
		s.clients = make(map[staticClientKey]*http.Client)
	}
	c := &http.Client{Timeout: requestTimeout, Transport: transport}
	s.clients[key] = c
	return c, nil
}

// FetchGET is FetchGET through the shared client of p.
func (s *StaticClients) FetchGET(ctx context.Context, p Proxy, targetURL, userAgent string, connectTimeout, requestTimeout time.Duration, tlsInsecure bool) ([]byte, int, error) {
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
	c, err := s.Client(p, connectTimeout, requestTimeout, tlsInsecure)
	if err != nil {
		return nil, 0, err
	}
	return doSOCKSGet(ctx, c, targetURL, userAgent)
}
