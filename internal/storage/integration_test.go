//go:build integration

// Integration tests for PostgreSQL storage.
//
// Run (requires PostgreSQL; use a dedicated test database, not production):
//
//	DATABASE_URL='postgres://rssam:rssam@localhost:5432/rssam_test?sslmode=disable' \
//	  go test ./internal/storage/... -tags=integration -count=1 -v
//
// Migrations are applied on startup. Created rows are removed in t.Cleanup.
// Docker/testcontainers are optional; a local PostgreSQL instance is enough.
package storage

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/migrations"
)

func testDSN() string { return strings.TrimSpace(os.Getenv("DATABASE_URL")) }

func testStore(t *testing.T) *PostgresStore {
	t.Helper()
	dsn := testDSN()
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := NewPostgresPool(ctx, dsn, 0)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := migrations.Apply(ctx, pool, slog.Default()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	return NewPostgresStore(pool)
}

func TestIntegration_MigrationsApply(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	var count int
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count == 0 {
		t.Fatal("expected at least one applied migration")
	}
}

func TestIntegration_FeedEntryCRUDAndTenantIsolation(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	hash, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}

	u1, err := store.CreateUser(ctx, CreateUserParams{
		Username:     "int_user_a_" + suffix,
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("create user a: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), u1.ID) })

	u2, err := store.CreateUser(ctx, CreateUserParams{
		Username:     "int_user_b_" + suffix,
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("create user b: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), u2.ID) })

	feedURL := "https://example.com/rss/" + suffix + ".xml"
	feed, err := store.CreateFeed(ctx, u1.ID, CreateFeedParams{
		FeedURL:         feedURL,
		FeedType:        "rss",
		Title:           "Integration feed",
		IntervalMinutes: 60,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.db.Exec(context.Background(), `DELETE FROM feeds WHERE id = $1`, feed.ID)
	})

	got, err := store.GetFeed(ctx, u1.ID, feed.ID)
	if err != nil {
		t.Fatalf("get feed owner: %v", err)
	}
	if got.FeedURL != feedURL {
		t.Fatalf("feed URL=%q", got.FeedURL)
	}

	if _, err := store.GetFeed(ctx, u2.ID, feed.ID); err != ErrNotFound {
		t.Fatalf("other user get feed: err=%v want ErrNotFound", err)
	}

	entryHash := "hash-" + suffix
	n, inserted, err := store.CreateEntries(ctx, feed.ID, []CreateEntryParams{{
		Title:   "Entry one",
		URL:     "https://example.com/e/" + suffix,
		Content: "body",
		Hash:    entryHash,
	}})
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if n != 1 || len(inserted) != 1 {
		t.Fatalf("inserted=%d entries=%d", n, len(inserted))
	}
	entryID := inserted[0].ID
	t.Cleanup(func() {
		_, _ = store.db.Exec(context.Background(), `DELETE FROM entries WHERE id = $1`, entryID)
	})

	entry, err := store.GetEntry(ctx, u1.ID, entryID)
	if err != nil {
		t.Fatalf("get entry owner: %v", err)
	}
	if entry.Title != "Entry one" {
		t.Fatalf("entry title=%q", entry.Title)
	}

	if _, err := store.GetEntry(ctx, u2.ID, entryID); err != ErrNotFound {
		t.Fatalf("other user get entry: err=%v want ErrNotFound", err)
	}

	feeds, total, err := store.ListFeeds(ctx, u1.ID, 10, 0)
	if err != nil {
		t.Fatalf("list feeds: %v", err)
	}
	if total != 1 || len(feeds) != 1 || feeds[0].ID != feed.ID {
		t.Fatalf("list feeds: total=%d len=%d", total, len(feeds))
	}

	feeds2, total2, err := store.ListFeeds(ctx, u2.ID, 10, 0)
	if err != nil {
		t.Fatalf("list feeds other user: %v", err)
	}
	for _, f := range feeds2 {
		if f.ID == feed.ID {
			t.Fatal("other user must not see foreign feed")
		}
	}
	if total2 != len(feeds2) {
		t.Fatalf("total mismatch: total=%d len=%d", total2, len(feeds2))
	}
}
