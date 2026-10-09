package service

import (
	"context"
	"testing"

	"rssam/internal/bridge/v1"
	"rssam/internal/reader"
	"rssam/internal/storage"
)

func TestItemSetHash(t *testing.T) {
	a := []bridge.Entry{{Hash: "x"}, {Hash: "y", Content: "one"}}
	b := []bridge.Entry{{Hash: "y", Content: "two"}, {Hash: "x"}}
	if itemSetHash(a) != itemSetHash(b) {
		t.Fatal("order and content must not change the hash")
	}
	if itemSetHash(a) == itemSetHash(a[:1]) {
		t.Fatal("different sets must differ")
	}
	if itemSetHash(nil) != "" {
		t.Fatal("empty set has no hash")
	}
}

func TestRefreshLoadedFeed_SameItemSetSkipsInsert(t *testing.T) {
	items := []bridge.Entry{{Title: "a", URL: "https://e/a", Hash: "ha"}, {Title: "b", URL: "https://e/b", Hash: "hb"}}
	feed := storage.Feed{ID: 5, OwnerID: 1, FeedURL: "https://example.com/f.xml", FeedType: "rss", IntervalMinutes: 10}
	h := &stubHandler{res: reader.FetchResponse{Entries: items}}
	r, fs, _, _ := newStatusRefresher(h, feed)
	entries := r.Entries.(*memEntryCreate)

	// First poll: unknown set, inserted and the hash stored.
	if n, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil || n != 2 {
		t.Fatalf("first poll: n=%d err=%v", n, err)
	}
	if fs.meta.ItemsHash == nil || *fs.meta.ItemsHash != itemSetHash(items) {
		t.Fatalf("items hash not stored: %+v", fs.meta.ItemsHash)
	}
	feed.ItemsHash = *fs.meta.ItemsHash

	// Same set again: storage is not asked at all and the hash is kept.
	before := len(entries.created)
	if n, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil || n != 0 {
		t.Fatalf("second poll: n=%d err=%v", n, err)
	}
	if len(entries.created) != before {
		t.Fatal("CreateEntries must not run for an unchanged item set")
	}
	if fs.meta.ItemsHash != nil {
		t.Fatalf("unchanged set must keep items_hash, got %q", *fs.meta.ItemsHash)
	}

	// Manual refresh ignores the shortcut.
	fs.feed = feed
	if _, err := r.RefreshFeedManual(context.Background(), feed.ID); err != nil {
		t.Fatal(err)
	}
	if len(entries.created) != before+2 {
		t.Fatal("manual refresh must re-run CreateEntries")
	}

	// A new item changes the set: insert runs, new hash stored.
	h.res.Entries = append(items, bridge.Entry{Title: "c", URL: "https://e/c", Hash: "hc"})
	if _, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
	if fs.meta.ItemsHash == nil || *fs.meta.ItemsHash == feed.ItemsHash {
		t.Fatal("changed set must store a new hash")
	}
}
