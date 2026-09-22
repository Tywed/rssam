//go:build integration

package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// A ranked search orders only the SearchRankWindow newest hits, the total
// stops at SearchTotalCap, and the planner settings the search flips are
// gone once it returns.
func TestIntegration_SearchEntriesRankWindowAndTotalCap(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "rankwin_owner")
	feed, _ := newIntegrationFeedWithEntries(t, store, owner.ID, 0)

	// One old entry that would win on relevance sits outside the window;
	// the newest window+50 entries all mention the marker once.
	const marker = "гравастар"
	params := []CreateEntryParams{{
		Title:   "old but very " + marker,
		URL:     "https://example.com/rankwin/old",
		Hash:    "rankwin-old",
		Content: strings.Repeat(marker+" ", 20),
	}}
	for i := range SearchRankWindow + 50 {
		params = append(params, CreateEntryParams{
			Title:   fmt.Sprintf("entry %d", i),
			URL:     fmt.Sprintf("https://example.com/rankwin/%d", i),
			Hash:    fmt.Sprintf("rankwin-%d", i),
			Content: "text " + marker,
		})
	}
	if _, _, err := store.CreateEntries(ctx, feed.ID, params); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `
WITH e AS (
  UPDATE entries SET published_at = now() - interval '1 day' * (CASE WHEN hash = 'rankwin-old' THEN 5000 ELSE split_part(hash, '-', 2)::int END)
  WHERE feed_id = $1
  RETURNING id, published_at
)
UPDATE user_entries ue SET sort_at = e.published_at FROM e WHERE ue.entry_id = e.id`, feed.ID); err != nil {
		t.Fatal(err)
	}

	ranked, total, err := store.SearchEntries(ctx, owner.ID, SearchEntriesFilter{Query: marker, FeedID: &feed.ID, Limit: 5, Rank: true, WithTotal: true})
	if err != nil {
		t.Fatal(err)
	}
	if total != SearchRankWindow+51 {
		t.Fatalf("total = %d, want %d", total, SearchRankWindow+51)
	}
	for _, e := range ranked {
		if e.Hash == "rankwin-old" {
			t.Fatalf("entry outside the rank window made the page: %+v", e)
		}
	}
	if len(ranked) != 5 || ranked[0].Hash != "rankwin-0" {
		t.Fatalf("ranked page: len=%d first=%q, want 5 rows led by the newest of equally ranked hits", len(ranked), ranked[0].Hash)
	}

	// Beyond the window the ranked list ends, unranked paging continues.
	past, _, err := store.SearchEntries(ctx, owner.ID, SearchEntriesFilter{Query: marker, FeedID: &feed.ID, Limit: 5, Offset: SearchRankWindow, Rank: true})
	if err != nil || len(past) != 0 {
		t.Fatalf("ranked page past the window: len=%d err=%v", len(past), err)
	}
	byDate, total, err := store.SearchEntries(ctx, owner.ID, SearchEntriesFilter{Query: marker, FeedID: &feed.ID, Limit: 5, Offset: SearchRankWindow})
	if err != nil || len(byDate) != 5 || total != SearchRankWindow+6 {
		t.Fatalf("date page past the window: len=%d total=%d err=%v", len(byDate), total, err)
	}
	last, _, err := store.SearchEntries(ctx, owner.ID, SearchEntriesFilter{Query: marker, FeedID: &feed.ID, Limit: 5, Offset: SearchRankWindow + 50, Sort: EntrySortNewest})
	if err != nil || len(last) != 1 || last[0].Hash != "rankwin-old" {
		t.Fatalf("last date page: len=%d err=%v", len(last), err)
	}

	var seq, idx string
	if err := store.db.QueryRow(ctx, "SELECT current_setting('enable_seqscan'), current_setting('enable_indexscan')").Scan(&seq, &idx); err != nil {
		t.Fatal(err)
	}
	if seq != "on" || idx != "on" {
		t.Fatalf("planner settings leaked out of the search transaction: seqscan=%s indexscan=%s", seq, idx)
	}
}

// The total is capped: a word with more than SearchTotalCap hits reports
// exactly the cap.
func TestIntegration_SearchEntriesTotalCap(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "totalcap_owner")
	feed, _ := newIntegrationFeedWithEntries(t, store, owner.ID, 0)
	if _, err := store.db.Exec(ctx, `
WITH e AS (
  INSERT INTO entries (feed_id, title, url, hash, content, search_vector)
  SELECT $1, 'cap ' || g, 'https://example.com/cap/' || g, 'cap-' || g, '', to_tsvector('simple', 'капремонт')
  FROM generate_series(1, $3) g
  RETURNING id, feed_id, created_at
)
INSERT INTO user_entries (user_id, entry_id, feed_id, sort_at) SELECT $2, id, feed_id, created_at FROM e`, feed.ID, owner.ID, SearchTotalCap+10); err != nil {
		t.Fatal(err)
	}
	rows, total, err := store.SearchEntries(ctx, owner.ID, SearchEntriesFilter{Query: "капремонт", Limit: 3, WithTotal: true})
	if err != nil || len(rows) != 3 || total != SearchTotalCap {
		t.Fatalf("len=%d total=%d err=%v, want total %d", len(rows), total, err, SearchTotalCap)
	}
}
