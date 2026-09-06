//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"rssam/internal/auth"
)

// Sessions were never purged: the table only ever shrank via per-user
// deletes, so every login (30-day TTL) stayed forever.
func TestIntegration_DeleteExpiredSessions(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	hash, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(ctx, CreateUserParams{Username: "sess_" + suffix, PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), u.ID) })

	now := time.Now().UTC()
	if _, err := store.CreateSession(ctx, u.ID, "expired-"+suffix, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSession(ctx, u.ID, "long-expired-"+suffix, now.Add(-40*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSession(ctx, u.ID, "live-"+suffix, now.Add(DefaultSessionTTL)); err != nil {
		t.Fatal(err)
	}

	res, err := store.RunRetentionCleanup(ctx, RetentionCleanupOpts{
		RemovedEntriesBefore: now.Add(-365 * 24 * time.Hour),
		WebhookLogsBefore:    now.Add(-365 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if res.ExpiredSessions < 2 {
		t.Fatalf("expected at least 2 expired sessions removed, got %d", res.ExpiredSessions)
	}
	if _, err := store.LookupSession(ctx, "live-"+suffix); err != nil {
		t.Fatalf("live session must survive: %v", err)
	}
	var n int
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1`, u.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 session row left, got %d", n)
	}
	// Index from migration 0030 exists.
	var idx int
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE tablename='sessions' AND indexname='sessions_expires_at_idx'`).Scan(&idx); err != nil {
		t.Fatal(err)
	}
	if idx != 1 {
		t.Fatal("sessions_expires_at_idx missing")
	}
}

// Sliding expiry is written back at most once per SessionTouchInterval.
func TestIntegration_TouchSessionRefreshesExpiry(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	hash, _ := auth.HashPassword("integration-test")
	u, err := store.CreateUser(ctx, CreateUserParams{Username: "touch_" + suffix, PasswordHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), u.ID) })

	now := time.Now().UTC()
	old := now.Add(DefaultSessionTTL - 2*time.Hour) // last touched 2h ago
	if _, err := store.CreateSession(ctx, u.ID, "t-"+suffix, old); err != nil {
		t.Fatal(err)
	}
	sess, _ := store.LookupSession(ctx, "t-"+suffix)
	if !SessionNeedsTouch(sess.ExpiresAt, now) {
		t.Fatal("2h-old session must need a touch")
	}
	if err := store.TouchSession(ctx, "t-"+suffix, now.Add(DefaultSessionTTL)); err != nil {
		t.Fatal(err)
	}
	sess, _ = store.LookupSession(ctx, "t-"+suffix)
	if SessionNeedsTouch(sess.ExpiresAt, now) {
		t.Fatal("freshly touched session must not need another touch")
	}
}
