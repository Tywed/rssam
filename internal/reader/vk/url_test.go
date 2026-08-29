package vk

import "testing"

func TestNormalizeQuery(t *testing.T) {
	if got := NormalizeQuery("  Foo   BAR  "); got != "foo bar" {
		t.Fatalf("got %q", got)
	}
}

func TestParseQueryFromFeedURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
		ok   bool
	}{
		{"https://vk.com/feed?section=search&q=Golang", "golang", true},
		{"https://www.vk.com/feed?section=search&q=hello+world", "hello world", true},
		{"vk-search://Rust%20lang", "rust lang", true},
		{"vk-search://?q=Docker", "docker", true},
		{"https://vk.com/feed?section=news", "", false},
		{"https://example.com/feed.xml", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseQueryFromFeedURL(tc.url)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("ParseQueryFromFeedURL(%q) = (%q, %v), want (%q, %v)", tc.url, got, ok, tc.want, tc.ok)
		}
	}
}
