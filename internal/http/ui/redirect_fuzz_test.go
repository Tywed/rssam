package ui

import (
	"net/url"
	"strings"
	"testing"
)

// FuzzLocalUIPath checks the security invariant of localUIPath: whatever the
// input, the result is either the fallback or a same-origin path under /ui/
// that survives a parse round-trip without a scheme or host.
func FuzzLocalUIPath(f *testing.F) {
	for _, s := range []string{
		"", "/ui/unread", "/ui/feeds?page=2#x", "//evil.example/ui/x", "https://evil.example/ui/x",
		"/ui/../admin", "/ui/%2e%2e/admin", "/admin", "javascript:alert(1)", "/ui/\\evil.example",
		"/ui//evil.example/", " /ui/x ", "/ui/x\r\nSet-Cookie: a=b", "%", "/ui/%zz",
		"\\\\evil.example\\ui", "/ui/../../ui/feeds", "/ui/./feeds", "/ui/feeds/../../etc",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		const fallback = "/ui/unread"
		got := localUIPath(raw, fallback)
		if got == fallback {
			return
		}
		if !strings.HasPrefix(got, "/ui/") {
			t.Fatalf("%q -> %q: not under /ui/", raw, got)
		}
		if strings.HasPrefix(got, "//") {
			t.Fatalf("%q -> %q: protocol-relative", raw, got)
		}
		if strings.ContainsAny(got, "\r\n") {
			t.Fatalf("%q -> %q: contains CR/LF", raw, got)
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("%q -> %q: unparsable: %v", raw, got, err)
		}
		if u.Scheme != "" || u.Host != "" || u.User != nil {
			t.Fatalf("%q -> %q: parsed with scheme/host (%q %q)", raw, got, u.Scheme, u.Host)
		}
		if !strings.HasPrefix(u.Path, "/ui/") {
			t.Fatalf("%q -> %q: decoded path %q escapes /ui/", raw, got, u.Path)
		}
		// No dot segment may survive: a browser would resolve it away from /ui/.
		for _, seg := range strings.Split(u.Path, "/") {
			if seg == ".." || seg == "." {
				t.Fatalf("%q -> %q: dot segment survived", raw, got)
			}
		}
	})
}
