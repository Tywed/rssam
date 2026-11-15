package proxy

import "testing"

func TestParseStaticProxy_IPv6AndPortRange(t *testing.T) {
	p, err := ParseStaticProxy("[::1]:1080")
	if err != nil || p.Host != "::1" || p.Port != 1080 {
		t.Fatalf("ipv6: %+v %v", p, err)
	}
	p, err = ParseStaticProxy("u:p@[2001:db8::1]:1080")
	if err != nil || p.Host != "2001:db8::1" || p.Username != "u" {
		t.Fatalf("ipv6 with auth: %+v %v", p, err)
	}
	for _, raw := range []string{"1.2.3.4:65536", "socks5://1.2.3.4:70000", "1.2.3.4:0", "1.2.3.4"} {
		if _, err := ParseStaticProxy(raw); err == nil {
			t.Fatalf("%q: expected error", raw)
		}
	}
}
