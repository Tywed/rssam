package proxy

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// socks5Stub is a no-auth SOCKS5 CONNECT proxy that counts handshakes.
func socks5Stub(t *testing.T) (addr string, handshakes *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	handshakes = &atomic.Int32{}
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
				var host string
				switch req[3] {
				case 1:
					b := make([]byte, 4)
					_, _ = io.ReadFull(c, b)
					host = net.IP(b).String()
				case 3:
					l := make([]byte, 1)
					_, _ = io.ReadFull(c, l)
					b := make([]byte, int(l[0]))
					_, _ = io.ReadFull(c, b)
					host = string(b)
				default:
					return
				}
				pb := make([]byte, 2)
				_, _ = io.ReadFull(c, pb)
				handshakes.Add(1)
				up, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb)))))
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
	return ln.Addr().String(), handshakes
}

func TestStaticClients_ReusesConnection(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer target.Close()
	addr, handshakes := socks5Stub(t)
	p, err := ParseStaticProxy("socks5://" + addr)
	if err != nil {
		t.Fatal(err)
	}

	var sc StaticClients
	for i := range 3 {
		body, status, err := sc.FetchGET(context.Background(), p, target.URL, "ua", time.Second, 2*time.Second, false)
		if err != nil || status != 200 || string(body) != "hello" {
			t.Fatalf("fetch %d: %v %d %q", i, err, status, body)
		}
	}
	if n := handshakes.Load(); n != 1 {
		t.Fatalf("3 fetches through one proxy made %d SOCKS connections, want 1 (kept alive)", n)
	}
	if len(sc.clients) != 1 {
		t.Fatalf("clients=%d", len(sc.clients))
	}
	// A different timeout or endpoint is another client; the pool stays bounded.
	for i := range staticClientsMax + 1 {
		if _, err := sc.Client(Proxy{Host: "10.0.0.1", Port: 1080 + i}, time.Second, time.Second, false); err != nil {
			t.Fatal(err)
		}
	}
	if len(sc.clients) > staticClientsMax {
		t.Fatalf("pool grew to %d", len(sc.clients))
	}
}
