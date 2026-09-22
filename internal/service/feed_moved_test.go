package service

import (
	"context"
	"testing"

	"rssam/internal/reader"
	"rssam/internal/storage"
)

func TestRefreshLoadedFeed_PermanentRedirectUpdatesURL(t *testing.T) {
	feed := storage.Feed{ID: 31, OwnerID: 1, FeedURL: "https://example.com/old.xml", FeedType: "rss", IntervalMinutes: 15}
	h := &stubHandler{res: reader.FetchResponse{NewURL: "https://example.com/new.xml"}}
	r, fs, _, _ := newStatusRefresher(h, feed)
	if _, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
	if fs.meta.NewFeedURL != "https://example.com/new.xml" {
		t.Fatalf("NewFeedURL=%q", fs.meta.NewFeedURL)
	}

	// Same URL, a bridge destination, or a non-RSS feed type: nothing to store.
	cases := []struct {
		name string
		feed storage.Feed
		to   string
	}{
		{"same", feed, feed.FeedURL},
		{"bridge", feed, "https://t.me/s/channel"},
		{"telegram feed", storage.Feed{ID: 32, OwnerID: 1, FeedURL: "https://t.me/s/a", FeedType: "telegram"}, "https://t.me/s/b"},
		{"empty", feed, ""},
	}
	for _, c := range cases {
		if got := movedFeedURL(c.feed, c.to); got != "" {
			t.Errorf("%s: movedFeedURL=%q, want empty", c.name, got)
		}
	}
	if got := movedFeedURL(storage.Feed{FeedURL: "https://a/x", FeedType: "atom"}, "https://a/y"); got != "https://a/y" {
		t.Fatalf("atom feed: %q", got)
	}
}
