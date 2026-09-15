//go:build integration

package storage

import (
	"context"
	"testing"
)

// WithoutBody must change nothing but the two body fields: same rows, same
// order, same total, for both the list and the search path.
func TestIntegration_ListEntriesWithoutBody(t *testing.T) {
	store := isolatedStore(t)
	store.ftsLanguage = "russian"
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "nobody")
	feed := newFeedForUser(t, store, owner.ID, "nobody", 60)

	params := []CreateEntryParams{
		{Title: "Первая новость", Content: "<p>тело первой</p>", URL: "https://example.com/nb/1", Hash: "nb-1"},
		{Title: "Вторая новость", Content: "<p>тело второй</p>", URL: "https://example.com/nb/2", Hash: "nb-2"},
	}
	if _, _, err := store.CreateEntries(ctx, feed.ID, params); err != nil {
		t.Fatal(err)
	}

	full, totalFull, err := store.ListEntries(ctx, owner.ID, ListEntriesFilter{FeedID: &feed.ID, Limit: 10, WithTotal: true})
	if err != nil {
		t.Fatal(err)
	}
	slim, totalSlim, err := store.ListEntries(ctx, owner.ID, ListEntriesFilter{FeedID: &feed.ID, Limit: 10, WithTotal: true, WithoutBody: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 2 || len(slim) != 2 || totalFull != totalSlim {
		t.Fatalf("rows full=%d slim=%d totals %d/%d", len(full), len(slim), totalFull, totalSlim)
	}
	for i := range full {
		if full[i].Content == "" {
			t.Fatalf("full row %d lost its content", i)
		}
		if slim[i].Content != "" || slim[i].OriginalContent != "" {
			t.Fatalf("slim row %d carries a body", i)
		}
		f, s := full[i], slim[i]
		f.Content, f.OriginalContent = "", ""
		if f != s {
			t.Fatalf("row %d differs beyond the body:\n full=%+v\n slim=%+v", i, f, s)
		}
	}

	hits, _, err := store.SearchEntries(ctx, owner.ID, SearchEntriesFilter{Query: "новость", FeedID: &feed.ID, Limit: 10, WithoutBody: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Content != "" || hits[0].Title == "" {
		t.Fatalf("search without body: %d hits, first=%+v", len(hits), hits[0])
	}
}
