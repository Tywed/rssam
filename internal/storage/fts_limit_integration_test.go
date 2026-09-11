//go:build integration

package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"rssam/internal/auth"
)

// A single oversized item must not fail the batch: to_tsvector is fed at
// most ftsIndexedChars characters, and the rest of the content is stored
// but not indexed.
func TestIntegration_CreateEntriesHugeContentIsIndexedTruncated(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	hash, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(ctx, CreateUserParams{Username: "fts_" + suffix, PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), u.ID) })

	var feedID int64
	if err := store.db.QueryRow(ctx, `
INSERT INTO feeds(user_id, feed_url, feed_type, title, interval_minutes, manual_paused)
VALUES ($1, $2, 'rss', 'fts', 60, TRUE) RETURNING id`, u.ID, "https://example.com/fts/"+suffix+".xml").Scan(&feedID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = store.db.Exec(context.Background(), `DELETE FROM feeds WHERE id = $1`, feedID) })

	// Worst case for tsvector size: unique hyphenated tokens, ~3.5 MB of text.
	var b strings.Builder
	for i := 0; b.Len() < 3_500_000; i++ {
		fmt.Fprintf(&b, "x%d-y%d-z%d-w%d-v%d ", i, i, i, i, i)
	}
	huge := b.String()
	marker := "уникальныймаркер" + suffix

	n, inserted, err := store.CreateEntries(ctx, feedID, []CreateEntryParams{
		{Title: "Normal " + marker, URL: "https://example.com/fts/n-" + suffix, Hash: "n-" + suffix, Content: "обычная запись"},
		{Title: "Huge", URL: "https://example.com/fts/h-" + suffix, Hash: "h-" + suffix, Content: huge},
	})
	if err != nil {
		t.Fatalf("create entries: %v", err)
	}
	if n != 2 || len(inserted) != 2 {
		t.Fatalf("inserted %d/%d, want 2", n, len(inserted))
	}

	var storedLen int
	var vecSize int
	if err := store.db.QueryRow(ctx, `
SELECT length(content), pg_column_size(search_vector) FROM entries WHERE feed_id = $1 AND hash = $2`, feedID, "h-"+suffix).Scan(&storedLen, &vecSize); err != nil {
		t.Fatal(err)
	}
	if storedLen != len([]rune(huge)) {
		t.Errorf("content stored truncated: %d chars, want %d", storedLen, len([]rune(huge)))
	}
	if vecSize <= 0 || vecSize >= 1<<20 {
		t.Errorf("tsvector size %d bytes", vecSize)
	}

	rows, _, err := store.SearchEntries(ctx, u.ID, SearchEntriesFilter{Query: marker, Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(rows) != 1 || rows[0].Hash != "n-"+suffix {
		t.Errorf("search for the normal entry returned %d rows", len(rows))
	}
}
