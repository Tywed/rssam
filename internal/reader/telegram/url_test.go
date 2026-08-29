package telegram

import "testing"

func TestNormalizeUsername(t *testing.T) {
	if got := NormalizeUsername("  @ChannelName  "); got != "ChannelName" {
		t.Fatalf("got %q", got)
	}
}

func TestParseUsernameFromFeedURL(t *testing.T) {
	cases := map[string]string{
		"https://t.me/s/rssbridge":   "rssbridge",
		"https://t.me/rssbridge":     "rssbridge",
		"https://telegram.me/s/demo": "demo",
		"https://rssbridge.t.me/":    "rssbridge",
		"http://t.me/rssbridge":      "rssbridge",
	}
	for url, want := range cases {
		got, ok := ParseUsernameFromFeedURL(url)
		if !ok || got != want {
			t.Fatalf("%s: got %q ok=%v want %q", url, got, ok, want)
		}
	}
}

func TestDetectFeedURL(t *testing.T) {
	if !DetectFeedURL("https://t.me/s/foo") {
		t.Fatal("expected telegram url")
	}
	if DetectFeedURL("https://example.com/feed.xml") {
		t.Fatal("expected non-telegram url")
	}
}
