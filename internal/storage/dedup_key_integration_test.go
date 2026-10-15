//go:build integration

package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/migrations"
	"rssam/internal/model"
)

// The dedup key is the first 16 bytes of a hex sha256; any other string is
// sha256-hashed first. Both paths must round-trip through every writer
// (RecordFeedEntryDedup, collapse) and the reader (FilterKnownEntryHashes,
// CreateEntries' NOT EXISTS).
func TestIntegration_DedupKeyHexAndOpaqueHashes(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	pw, _ := auth.HashPassword("integration-test")
	u, err := store.CreateUser(ctx, CreateUserParams{Username: "dk", PasswordHash: pw})
	if err != nil {
		t.Fatal(err)
	}
	f, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: "https://dk.example/rss", FeedType: "rss", Title: "k", IntervalMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	hexHash := model.DedupHashFromURL("https://dk.example/1")
	opaque := "sender-id-42" // an inbound feed stores the sender's Hash verbatim
	if _, err := store.RecordFeedEntryDedup(ctx, f.ID, []FeedEntryDedupParams{{Hash: hexHash}, {Hash: opaque}}); err != nil {
		t.Fatal(err)
	}
	var hexKey, opaqueKey []byte
	if err := store.db.QueryRow(ctx, `SELECT rssam_dedup_key($1), rssam_dedup_key($2)`, hexHash, opaque).Scan(&hexKey, &opaqueKey); err != nil {
		t.Fatal(err)
	}
	raw, _ := hex.DecodeString(hexHash)
	if string(hexKey) != string(raw[:16]) {
		t.Fatalf("hex key = %x, want first 16 bytes of %s", hexKey, hexHash)
	}
	sum := sha256.Sum256([]byte(opaque))
	if string(opaqueKey) != string(sum[:16]) {
		t.Fatalf("opaque key = %x, want sha256 prefix", opaqueKey)
	}
	known, err := store.FilterKnownEntryHashes(ctx, f.ID, []string{hexHash, opaque, "fresh"})
	if err != nil || len(known) != 2 {
		t.Fatalf("known = %v err=%v", known, err)
	}
	if _, ok := known[hexHash]; !ok {
		t.Fatal("hex hash not recognised")
	}
	if _, ok := known[opaque]; !ok {
		t.Fatal("opaque hash not recognised")
	}
	n, _, err := store.CreateEntries(ctx, f.ID, []CreateEntryParams{
		{Title: "a", URL: "https://dk.example/1", Content: "c", Hash: hexHash},
		{Title: "b", URL: "https://dk.example/2", Content: "c", Hash: opaque},
		{Title: "c", URL: "https://dk.example/3", Content: "c", Hash: "fresh"},
	})
	if err != nil || n != 1 {
		t.Fatalf("CreateEntries inserted %d, want 1 (two hashes are in dedup): %v", n, err)
	}
	// Collapsing the fresh entry must put its key in and keep it out next time.
	if _, err := store.CollapseEntriesToHashes(ctx, CollapseEntriesParams{FeedID: &f.ID}); err != nil {
		t.Fatal(err)
	}
	known, err = store.FilterKnownEntryHashes(ctx, f.ID, []string{"fresh"})
	if err != nil || len(known) != 1 {
		t.Fatalf("after collapse known = %v err=%v", known, err)
	}
}

// Migration 0062 on pre-0062 rows: hex hashes keep their identity, opaque
// ones are hashed, rows whose keys coincide keep the earliest first_seen_at,
// and empty hashes are dropped.
func TestIntegration_Migration0062DedupRewrite(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	hexA := model.DedupHashFromString("a")
	for _, q := range []string{
		`DROP TABLE feed_entry_dedup`,
		`CREATE TABLE feed_entry_dedup (feed_id BIGINT NOT NULL REFERENCES feeds(id) ON DELETE CASCADE, hash TEXT NOT NULL, url TEXT NOT NULL DEFAULT '', first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY (feed_id, hash))`,
		`INSERT INTO users(id, username, password_hash, role) VALUES (21, 'm62', 'h', 'admin')`,
		`INSERT INTO feeds(id, owner_id, feed_url, feed_type, title, interval_minutes) VALUES (1, 21, 'https://x/a.xml', 'rss', 'A', 60)`,
		`INSERT INTO feed_entry_dedup(feed_id, hash, url, first_seen_at) VALUES
		   (1, '` + hexA + `', 'https://x/1', '2026-01-02'),
		   (1, '` + hexA[:32] + `0000000000000000000000000000000f', 'https://x/1b', '2026-01-01'),
		   (1, 'opaque', '', '2026-02-01'),
		   (1, '', '', '2026-03-01')`,
	} {
		if _, err := store.db.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	body, err := migrations.Source("0062_dedup_key_bytea.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, body); err != nil {
		t.Fatal(err)
	}
	var n int
	var first time.Time
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM feed_entry_dedup`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows = %d err=%v, want 2 (two hex hashes share a 16-byte prefix, empty dropped)", n, err)
	}
	if err := store.db.QueryRow(ctx, `SELECT first_seen_at FROM feed_entry_dedup WHERE hash = rssam_dedup_key($1)`, hexA).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if first.UTC().Format("2006-01-02") != "2026-01-01" {
		t.Fatalf("merged key keeps the earliest first_seen_at, got %s", first)
	}
	known, err := store.FilterKnownEntryHashes(ctx, 1, []string{hexA, "opaque", ""})
	if err != nil || len(known) != 2 {
		t.Fatalf("known after migration = %v err=%v", known, err)
	}
	var idx int
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'feed_entry_dedup' AND indexname IN ('feed_entry_dedup_pkey', 'feed_entry_dedup_feed_id_first_seen_idx')`).Scan(&idx); err != nil || idx != 2 {
		t.Fatalf("indexes = %d err=%v", idx, err)
	}
}
