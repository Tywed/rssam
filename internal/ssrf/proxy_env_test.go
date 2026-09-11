package ssrf

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// With HTTP_PROXY set the transport dials the proxy, not the target, so the
// target must be checked before the proxy is chosen. Run in a child process
// because the proxy environment is read once at init.
func TestHTTPClient_ProxyDoesNotBypassPrivateNetworkCheck(t *testing.T) {
	if os.Getenv("SSRF_PROXY_CHILD") == "" {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		// A "proxy" that answers 200 to anything: if a request reaches it, the
		// guard let a private target through.
		var reached []string
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				buf := make([]byte, 4096)
				n, _ := c.Read(buf)
				reached = append(reached, string(buf[:n]))
				_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
				c.Close()
			}
		}()

		cmd := exec.Command(os.Args[0], "-test.run=^TestHTTPClient_ProxyDoesNotBypassPrivateNetworkCheck$", "-test.v")
		cmd.Env = append(os.Environ(),
			"SSRF_PROXY_CHILD=1",
			"HTTP_PROXY=http://"+ln.Addr().String(),
			"HTTPS_PROXY=http://"+ln.Addr().String(),
			"NO_PROXY=",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child failed: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "PASS") {
			t.Fatalf("child output:\n%s", out)
		}
		for _, r := range reached {
			if strings.Contains(r, "169.254.169.254") || strings.Contains(r, "10.0.0.7") {
				t.Fatalf("private target reached the proxy: %q", r)
			}
		}
		if len(reached) == 0 {
			t.Fatal("public target should have been sent through the proxy")
		}
		return
	}

	// Child: proxy configured, DNS stubbed.
	lookup := func(_ context.Context, host string) ([]string, error) {
		switch host {
		case "public.example":
			return []string{"93.184.216.34"}, nil
		case "internal.example":
			return []string{"10.0.0.7"}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}
	guard, err := New(Config{LookupHost: lookup})
	if err != nil {
		t.Fatal(err)
	}
	client := guard.HTTPClient(3 * time.Second)

	for _, target := range []string{"http://169.254.169.254/latest/meta-data/", "http://internal.example/"} {
		resp, err := client.Get(target)
		if err == nil {
			resp.Body.Close()
			t.Fatalf("%s: expected ssrf error through proxy", target)
		}
		if !strings.Contains(err.Error(), "ssrf") {
			t.Fatalf("%s: err=%v", target, err)
		}
	}
	resp, err := client.Get("http://public.example/feed.xml")
	if err != nil {
		t.Fatalf("public target via proxy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
