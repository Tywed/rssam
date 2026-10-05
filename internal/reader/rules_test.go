package reader

import (
	"testing"

	"rssam/internal/bridge/v1"
)

func TestApplyURLRewriteRules(t *testing.T) {
	entries := []bridge.Entry{
		{URL: "https://news.example.com/r/123"},
	}
	got := ApplyURLRewriteRules(entries, "https://news\\.example\\.com/r/(\\d+)\nhttps://news.example.com/article/$1")
	if got[0].URL != "https://news.example.com/article/123" {
		t.Fatalf("got %q", got[0].URL)
	}
}

func TestApplyFeedRules_Blocked(t *testing.T) {
	entries := []bridge.Entry{
		{Title: "Normal", URL: "https://a.test/1"},
		{Title: "SPAM promo", URL: "https://a.test/2"},
	}
	got := ApplyFeedRules(entries, "(?i)spam|promo", "")
	if len(got) != 1 || got[0].Title != "Normal" {
		t.Fatalf("got %#v", got)
	}
}
