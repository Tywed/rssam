//go:build integration

package storage

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"rssam/internal/migrations"
)

// isolatedStore migrates a private schema and returns a store bound to it.
// Tests that exercise global queries (job queue, daily reset, admin
// summaries) must not see feeds created by other tests or by the e2e
// package, whose scheduler runs against the same CI database.
func isolatedStore(t *testing.T) *PostgresStore {
	t.Helper()
	base := strings.TrimSpace(testDSN())
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	schema := fmt.Sprintf("it_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := NewPostgresPool(ctx, base, 0)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	dropStaleIsolatedSchemas(t, admin)
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})

	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	pool, err := NewPostgresPool(ctx, base+sep+"search_path="+schema, 0)
	if err != nil {
		t.Fatalf("connect isolated schema: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrations.Apply(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("apply migrations in %s: %v", schema, err)
	}
	return NewPostgresStore(pool)
}

// dropStaleIsolatedSchemas removes it_* schemas left by a killed run: the
// pre-existing index tests count pg_indexes across all schemas.
func dropStaleIsolatedSchemas(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	rows, err := db.Query(ctx, `SELECT nspname FROM pg_namespace WHERE nspname LIKE 'it\_%'`)
	if err != nil {
		t.Fatalf("list schemas: %v", err)
	}
	var stale []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if ns, err := strconv.ParseInt(strings.TrimPrefix(name, "it_"), 10, 64); err == nil && time.Since(time.Unix(0, ns)) > time.Hour {
			stale = append(stale, name)
		}
	}
	rows.Close()
	for _, name := range stale {
		_, _ = db.Exec(ctx, `DROP SCHEMA `+name+` CASCADE`)
	}
}

func newFeedForUser(t *testing.T, store *PostgresStore, userID int64, tag string, interval int) Feed {
	t.Helper()
	feed, err := store.CreateFeed(context.Background(), userID, CreateFeedParams{
		FeedURL:         fmt.Sprintf("https://example.com/%s/%d/%d.xml", tag, userID, time.Now().UnixNano()),
		FeedType:        "rss",
		Title:           tag,
		IntervalMinutes: interval,
	})
	if err != nil {
		t.Fatalf("create feed %s: %v", tag, err)
	}
	return feed
}
