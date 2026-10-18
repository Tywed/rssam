package service

import (
	"context"
	"strings"
	"testing"

	"rssam/internal/bridge/v1"
	"rssam/internal/filter"
	"rssam/internal/reader"
	"rssam/internal/storage"
)

const dirtyHTML = `<p>text</p><script>alert(1)</script><img src="https://x/y.png" onerror="alert(2)">`

func assertClean(t *testing.T, where, c string) {
	t.Helper()
	if strings.Contains(c, "<script") || strings.Contains(c, "onerror") || !strings.Contains(c, "<p>text</p>") {
		t.Fatalf("%s: content not sanitized: %q", where, c)
	}
}

// Handlers return content as fetched; everything a full feed stores has
// been through SanitizeHTML, and a feed with keep/blocked rules has its
// rules matched against the sanitized text.
func TestRefreshLoadedFeed_SanitizesStoredContent(t *testing.T) {
	items := []bridge.Entry{
		{Title: "a", URL: "https://e/a", Hash: "ha", Content: dirtyHTML},
		{Title: "b", URL: "https://e/b", Hash: "hb", Content: `<p>text</p><script>keepword</script>`},
	}
	feed := storage.Feed{ID: 5, OwnerID: 1, FeedURL: "https://example.com/f.xml", FeedType: "rss", IntervalMinutes: 10}
	h := &stubHandler{res: reader.FetchResponse{Entries: items}}
	r, _, _, _ := newStatusRefresher(h, feed)
	entries := r.Entries.(*memEntryCreate)

	if n, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil || n != 2 {
		t.Fatalf("poll: n=%d err=%v", n, err)
	}
	for _, e := range entries.created {
		assertClean(t, "full store", e.Content)
	}
	// A keep rule that only matches inside a <script> block sees the
	// sanitized text and therefore keeps nothing.
	entries.created = nil
	feed.KeepRules = "keepword"
	feed.ItemsHash = ""
	if n, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil || n != 0 || len(entries.created) != 0 {
		t.Fatalf("keep rule on stripped markup: n=%d created=%d err=%v", n, len(entries.created), err)
	}
	feed.KeepRules = "text"
	if n, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil || n != 2 {
		t.Fatalf("keep rule on visible text: n=%d err=%v", n, err)
	}
}

// Filtered/relay feeds sanitize after dedup: only fresh items pay for it,
// and both the stored entry and the relayed one are clean.
func TestProcessEntriesFiltered_SanitizesFreshOnly(t *testing.T) {
	dedup := &memDedupStore{known: map[string]struct{}{"old": {}}}
	entries := &memEntryCreate{}
	filters := &memFilterList{filters: []storage.Filter{{
		ID: 1, UserID: 1, Enabled: true, FeedScope: storage.FilterFeedScopeAll,
		Rules: []storage.FilterRule{{Field: "content", Pattern: "text", Op: "regex"}},
	}}}
	r := &FeedRefresher{Dedup: dedup, Entries: entries, Filters: filters, Engine: filter.New(filter.Config{})}
	feed := storage.Feed{ID: 1, OwnerID: 1, StoreMode: storage.StoreModeFiltered}
	subs := []storage.Subscription{{UserID: 1, FeedID: feed.ID}}

	in := []storage.CreateEntryParams{
		{Title: "known", Hash: "old", URL: "https://e/old", Content: dirtyHTML},
		{Title: "new", Hash: "new", URL: "https://e/new", Content: dirtyHTML},
	}
	inserted, created, fresh, _, err := r.processEntriesFiltered(context.Background(), feed, subs, in, false, &storage.UpdateFeedRefreshMetaParams{}, true)
	if err != nil || inserted != 1 || fresh != 1 || len(created) != 1 {
		t.Fatalf("inserted=%d fresh=%d created=%d err=%v", inserted, fresh, len(created), err)
	}
	assertClean(t, "filtered store", entries.created[0].Content)
	assertClean(t, "filtered result", created[0].Content)
	if in[0].Content != dirtyHTML || in[1].Content != dirtyHTML {
		t.Fatal("the caller's slice must stay as fetched")
	}

	// A filter that matches a word only present inside <script> sees the
	// sanitized text (a fresh refresher: the filter list is cached per user).
	dedup.known = map[string]struct{}{}
	entries.created = nil
	filters.filters[0].Rules[0].Pattern = "alert"
	r = &FeedRefresher{Dedup: dedup, Entries: entries, Filters: filters, Engine: filter.New(filter.Config{})}
	inserted, _, _, _, err = r.processEntriesFiltered(context.Background(), feed, subs, in[1:], false, &storage.UpdateFeedRefreshMetaParams{}, true)
	if err != nil || inserted != 0 {
		t.Fatalf("filter on stripped markup: inserted=%d err=%v", inserted, err)
	}
}
