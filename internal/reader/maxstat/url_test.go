package maxstat

import "testing"

func TestParseQueryFromFeedURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
		ok   bool
	}{
		{"https://maxstat.ru/posts?search=krasnodar", "krasnodar", true},
		{"https://www.maxstat.ru/posts?search=hello+world", "hello world", true},
		{"maxstat-search://Rust%20lang", "rust lang", true},
		{"maxstat-search://?search=Docker", "docker", true},
		{"https://max.ru/test", "", false},
	}
	for _, tc := range tests {
		got, ok := ParseQueryFromFeedURL(tc.url)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("ParseQueryFromFeedURL(%q) = (%q, %v), want (%q, %v)", tc.url, got, ok, tc.want, tc.ok)
		}
	}
}

func TestDetectFeedURL(t *testing.T) {
	if !DetectFeedURL("https://maxstat.ru/posts?search=test") {
		t.Fatal("expected true")
	}
	if DetectFeedURL("https://max.ru/channel") {
		t.Fatal("expected false")
	}
}
