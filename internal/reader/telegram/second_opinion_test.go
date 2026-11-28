package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"rssam/internal/bridge/v1"
	"rssam/internal/proxy"
)

// fixedSOCKS5 is a no-auth SOCKS5 proxy that ignores the requested host and
// tunnels every CONNECT to upstream — so two "proxies" can show two
// different t.me pages for the same URL.
func fixedSOCKS5(t *testing.T, upstream string) (addr string, connects *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	connects = &atomic.Int32{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				hdr := make([]byte, 2)
				if _, err := io.ReadFull(c, hdr); err != nil {
					return
				}
				if _, err := io.ReadFull(c, make([]byte, int(hdr[1]))); err != nil {
					return
				}
				_, _ = c.Write([]byte{5, 0})
				req := make([]byte, 4)
				if _, err := io.ReadFull(c, req); err != nil {
					return
				}
				switch req[3] {
				case 1:
					_, _ = io.ReadFull(c, make([]byte, 4))
				case 3:
					l := make([]byte, 1)
					_, _ = io.ReadFull(c, l)
					_, _ = io.ReadFull(c, make([]byte, int(l[0])))
				default:
					return
				}
				_, _ = io.ReadFull(c, make([]byte, 2))
				connects.Add(1)
				up, err := net.Dial("tcp", upstream)
				if err != nil {
					_, _ = c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				defer up.Close()
				_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				go func() { _, _ = io.Copy(up, c) }()
				_, _ = io.Copy(c, up)
			}()
		}
	}()
	return ln.Addr().String(), connects
}

func hostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(p)
	return h, n
}

// The stub page through one proxy is checked through another: when the
// second one sees the channel, the poll succeeds, the first proxy is
// reported bad and both outcomes reach the observer.
func TestFetch_StubPageGetsASecondOpinion(t *testing.T) {
	stubPage := `<html><body><div class="tgme_page">open in telegram</div></body></html>`
	stubSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, stubPage) }))
	defer stubSrv.Close()
	chanSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tgPage([]int{7}, "")) }))
	defer chanSrv.Close()

	badAddr, badConnects := fixedSOCKS5(t, strings.TrimPrefix(stubSrv.URL, "https://"))
	goodAddr, goodConnects := fixedSOCKS5(t, strings.TrimPrefix(chanSrv.URL, "https://"))
	badHost, badPort := hostPort(t, badAddr)
	goodHost, goodPort := hostPort(t, goodAddr)

	// The service hands out the bad proxy first (it becomes the working
	// one), then the good one; /proxy/bad records who was reported.
	var mu sync.Mutex
	var reported []string
	var served atomic.Int32
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/proxy/bad" {
			var body struct {
				ID string `json:"id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			reported = append(reported, body.ID)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if served.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "bad", "host": badHost, "port": badPort})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "good", "host": goodHost, "port": goodPort})
	}))
	defer svc.Close()

	pc := proxy.NewClient(svc.Client())
	var seen []string
	pc.Observe = func(id, result string) {
		mu.Lock()
		seen = append(seen, id+":"+result)
		mu.Unlock()
	}
	h := NewHandler(pc, nil, Config{
		UseProxy:              true,
		ProxyServiceURL:       svc.URL,
		ProxyConnectTimeout:   2 * time.Second,
		ProxyRequestTimeout:   5 * time.Second,
		TLSInsecureSkipVerify: true,
	})

	res, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://t.me/chan"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("entries: %d, want the channel page seen through the second proxy", len(res.Entries))
	}
	if badConnects.Load() != 1 || goodConnects.Load() != 1 {
		t.Fatalf("connects bad=%d good=%d, want one each", badConnects.Load(), goodConnects.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 1 || reported[0] != "bad" {
		t.Fatalf("reported bad: %v, want [bad]", reported)
	}
	want := []string{"bad:ok", "bad:stub", "good:stub_retry_ok"}
	if strings.Join(seen, " ") != strings.Join(want, " ") {
		t.Fatalf("observer: %v, want %v", seen, want)
	}
}

// When the service has no other endpoint, the stub stands: the error keeps
// its text and the path it came through, and the proxy is not reported.
func TestFetch_StubPageWithoutAnotherProxyIsUnavailable(t *testing.T) {
	stubPage := `<html><body><div class="tgme_page">open in telegram</div></body></html>`
	stubSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, stubPage) }))
	defer stubSrv.Close()
	addr, connects := fixedSOCKS5(t, strings.TrimPrefix(stubSrv.URL, "https://"))
	host, port := hostPort(t, addr)

	var bad atomic.Int32
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/proxy/bad" {
			bad.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "only", "host": host, "port": port})
	}))
	defer svc.Close()

	pc := proxy.NewClient(svc.Client())
	h := NewHandler(pc, nil, Config{UseProxy: true, ProxyServiceURL: svc.URL, ProxyConnectTimeout: 2 * time.Second, ProxyRequestTimeout: 5 * time.Second, TLSInsecureSkipVerify: true})
	_, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://t.me/chan"})
	var cu *ChannelUnavailableError
	if !errors.As(err, &cu) || !errors.Is(err, ErrChannelUnavailable) || !strings.HasPrefix(cu.Via, "only ") {
		t.Fatalf("want ChannelUnavailableError via 'only', got %v", err)
	}
	if connects.Load() != 1 || bad.Load() != 0 {
		t.Fatalf("connects=%d reported=%d, want 1 and 0", connects.Load(), bad.Load())
	}
}
