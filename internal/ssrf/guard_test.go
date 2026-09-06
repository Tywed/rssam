package ssrf

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGuard_ValidateURL(t *testing.T) {
	g, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{name: "localhost hostname", url: "http://localhost/article", wantErr: "blocked ip range"},
		{name: "loopback ipv4", url: "http://127.0.0.1/article", wantErr: "blocked ip range"},
		{name: "private 10.x", url: "http://10.0.0.1/feed.xml", wantErr: "blocked ip range"},
		{name: "link-local metadata", url: "http://169.254.169.254/latest/meta-data/", wantErr: "blocked ip range"},
		{name: "private 192.168", url: "http://192.168.1.10/", wantErr: "blocked ip range"},
		{name: "file scheme", url: "file:///etc/passwd", wantErr: "only http/https"},
		{name: "gopher scheme", url: "gopher://example.com", wantErr: "only http/https"},
		{name: "userinfo", url: "http://user:pass@example.com/", wantErr: "userinfo"},
		{name: "empty url", url: "   ", wantErr: "empty url"},
		{name: "public ipv4 ok", url: "https://8.8.8.8/dns-query", wantErr: ""},
		{name: "public ipv6 ok", url: "https://[2001:4860:4860::8888]/", wantErr: ""},
		{name: "multicast ipv4", url: "http://224.0.0.1/", wantErr: "blocked ip range"},
		{name: "ssdp multicast", url: "http://239.255.255.250:1900/", wantErr: "blocked ip range"},
		{name: "reserved ipv4", url: "http://240.0.0.1/", wantErr: "blocked ip range"},
		{name: "broadcast", url: "http://255.255.255.255/", wantErr: "blocked ip range"},
		{name: "test-net", url: "http://192.0.2.1/", wantErr: "blocked ip range"},
		{name: "benchmark range", url: "http://198.18.0.1/", wantErr: "blocked ip range"},
		{name: "unspecified ipv6", url: "http://[::]/", wantErr: "blocked ip range"},
		{name: "multicast ipv6", url: "http://[ff02::1]/", wantErr: "blocked ip range"},
		{name: "documentation ipv6", url: "http://[2001:db8::1]/", wantErr: "blocked ip range"},
		{name: "ipv4-mapped loopback", url: "http://[::ffff:127.0.0.1]/", wantErr: "blocked ip range"},
		{name: "nat64 loopback", url: "http://[64:ff9b::7f00:1]/", wantErr: "blocked ip range"},
		{name: "nat64 metadata", url: "http://[64:ff9b::a9fe:a9fe]/", wantErr: "blocked ip range"},
		{name: "nat64 public ok", url: "http://[64:ff9b::808:808]/", wantErr: ""},
		{name: "6to4 loopback", url: "http://[2002:7f00:1::1]/", wantErr: "blocked ip range"},
		{name: "6to4 private", url: "http://[2002:c0a8:101::1]/", wantErr: "blocked ip range"},
		{name: "6to4 public ok", url: "http://[2002:808:808::1]/", wantErr: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := g.ValidateURL(tc.url)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected allow, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestGuard_AllowedCIDRException(t *testing.T) {
	g, err := New(Config{AllowedCIDRs: []string{"10.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.ValidateURL("http://10.1.2.3/feed.xml"); err != nil {
		t.Fatalf("expected allow via CIDR exception: %v", err)
	}
}

func TestGuard_AllowPrivateNetwork(t *testing.T) {
	g, err := New(Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.ValidateURL("http://127.0.0.1/article"); err != nil {
		t.Fatalf("expected allow: %v", err)
	}
}

func TestGuard_BlockedHosts(t *testing.T) {
	g, err := New(Config{
		BlockedHosts: []string{"metadata.internal", "LOCALHOST"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.ValidateURL("http://metadata.internal/x"); err == nil {
		t.Fatal("expected blocked host")
	} else if !strings.Contains(err.Error(), "blocked host") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGuard_DNSRebindingAtDial(t *testing.T) {
	g, err := New(Config{
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, address)
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	client := g.HTTPClient(2 * time.Second)
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strings.TrimPrefix(ln.Addr().String(), "127.0.0.1:"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("expected dial block for loopback")
	}
	if !strings.Contains(err.Error(), "blocked ip range") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Ensure custom resolver is used for hostname checks.
func TestGuard_PublicHostnameWithLookup(t *testing.T) {
	g, err := New(Config{
		LookupHost: func(ctx context.Context, host string) ([]string, error) {
			if host == "example.com" {
				return []string{"93.184.216.34"}, nil
			}
			return nil, errors.New("not found")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.ValidateURL("https://example.com/feed.xml"); err != nil {
		t.Fatalf("expected allow: %v", err)
	}
}

func TestGuard_CustomLookupHost(t *testing.T) {
	var called bool
	g, err := New(Config{
		LookupHost: func(ctx context.Context, host string) ([]string, error) {
			called = true
			if host == "evil.example" {
				return []string{"10.0.0.5"}, nil
			}
			return nil, errors.New("not found")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = g.ValidateURL("http://evil.example/")
	if !called {
		t.Fatal("expected lookup")
	}
	if err == nil || !strings.Contains(err.Error(), "blocked ip range") {
		t.Fatalf("expected private block, got %v", err)
	}
}
