package reader

import (
	"testing"

	maxbridge "rssam/internal/reader/max"
)

func TestParseMaxChannelFromFeedURL(t *testing.T) {
	cases := []struct {
		url     string
		channel string
		ok      bool
	}{
		{"https://max.ru/rosgvard_krd", "rosgvard_krd", true},
		{"https://www.max.ru/channel/foo", "foo", true},
		{"https://max.ru/c/bar", "bar", true},
		{"https://example.com/feed.xml", "", false},
		{"https://max.ru/", "", false},
	}
	for _, tc := range cases {
		ch, ok := maxbridge.ParseChannelFromFeedURL(tc.url)
		if ok != tc.ok || ch != tc.channel {
			t.Fatalf("ParseChannelFromFeedURL(%q) = (%q, %v), want (%q, %v)", tc.url, ch, ok, tc.channel, tc.ok)
		}
	}
}

func TestDetectFeedTypeFromURL_Max(t *testing.T) {
	if got := DetectFeedTypeFromURL("https://max.ru/news"); got != FeedTypeMax {
		t.Fatalf("got %q", got)
	}
}
