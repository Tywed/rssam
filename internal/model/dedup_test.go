package model

import (
	"net/url"
	"strings"
	"testing"
)

func TestNormalizeURL_StripsTrackingParams(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://ex.com/a?utm_source=tg&utm_medium=post", "https://ex.com/a"},
		{"https://ex.com/a?id=1&utm_source=tg", "https://ex.com/a?id=1"},
		{"https://ex.com/a?utm_source=tg&id=1", "https://ex.com/a?id=1"},
		{"https://ex.com/a?yclid=123&id=1&fbclid=abc", "https://ex.com/a?id=1"},
		{"https://ex.com/a?UTM_Source=x", "https://ex.com/a"},
		{"https://ex.com/a?_openstat=x;y;z", "https://ex.com/a"},
		{"https://ex.com/a?mtm_campaign=x&pk_kwd=y&itm_source=z", "https://ex.com/a"},
		{"https://ex.com/a?ysclid=abc#frag", "https://ex.com/a"},
		{"https://ex.com/a?id=1&&utm_source=x", "https://ex.com/a?id=1"},
		{"HTTPS://EX.com/a?utm_source=x", "https://ex.com/a"},
		{"https://ex.com?utm_source=x", "https://ex.com/"},
		// order and raw encoding of non-tracking params are preserved
		{"https://ex.com/a?b=2&a=%D0%BF%D1%80&utm_term=t", "https://ex.com/a?b=2&a=%D0%BF%D1%80"},
		{"https://ex.com/a?q=a+b&utm_source=x", "https://ex.com/a?q=a+b"},
	}
	for _, c := range cases {
		if got := NormalizeURL(c.in); got != c.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// URLs without trackers must come out byte-for-byte as before 0.1.11, or the
// hashes of already stored entries would no longer match new items.
func TestNormalizeURL_UnchangedWithoutTrackers(t *testing.T) {
	cases := []string{
		"https://ex.com/a?id=1&page=2",
		"https://ex.com/a?utm=notaprefix",
		"https://ex.com/a?ref=site",
		"https://ex.com/search?q=utm_source",
		"https://ex.com/a?a=1&&b=2",
		"https://ex.com/a?",
		"https://ex.com/utm_source/1",
		"https://ex.com/a?x=%2F%3F",
		"https://ex.com/",
		"tg://resolve?domain=x&utm_source=y",
		"not a url",
		"",
	}
	for _, in := range cases {
		want := legacyNormalizeURL(in)
		if got := NormalizeURL(in); got != want {
			t.Errorf("NormalizeURL(%q) = %q, legacy %q", in, got, want)
		}
	}
	if got := NormalizeURL("https://ex.com/a?gclid"); got != "https://ex.com/a" {
		t.Errorf("valueless tracker: got %q", got)
	}
}

func TestDedupHashFromURL_TrackerVariantsCollide(t *testing.T) {
	a := DedupHashFromURL("https://ex.com/news/1?utm_source=telegram&utm_campaign=morning")
	b := DedupHashFromURL("https://ex.com/news/1?utm_source=vk")
	c := DedupHashFromURL("https://ex.com/news/1")
	if a != b || b != c {
		t.Fatalf("hashes differ: %s %s %s", a, b, c)
	}
	if d := DedupHashFromURL("https://ex.com/news/2"); d == c {
		t.Fatal("different articles must not collide")
	}
}

func TestStripTrackingParams(t *testing.T) {
	if q, ok := StripTrackingParams("a=1&utm_source=x&b=2"); !ok || q != "a=1&b=2" {
		t.Fatalf("got %q %v", q, ok)
	}
	if q, ok := StripTrackingParams("utm_source=x"); !ok || q != "" {
		t.Fatalf("got %q %v", q, ok)
	}
	if q, ok := StripTrackingParams("a=1&b=2"); ok || q != "a=1&b=2" {
		t.Fatalf("got %q %v", q, ok)
	}
}

func BenchmarkNormalizeURL(b *testing.B) {
	for b.Loop() {
		NormalizeURL("https://example.com/news/2026/09/11/some-long-slug-title?id=12345&utm_source=telegram&utm_medium=social&utm_campaign=daily")
	}
}

// legacyNormalizeURL is NormalizeURL as shipped through 0.1.10; kept only to
// pin that tracker-free URLs normalise identically.
func legacyNormalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	u.Fragment = ""
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String()
}
