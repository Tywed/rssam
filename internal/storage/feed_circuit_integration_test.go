//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"rssam/internal/auth"
)

func TestIntegration_FeedPollOutcomeIsOneRowVersion(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	hash, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(ctx, CreateUserParams{Username: "circuit_" + suffix, PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), u.ID) })

	var feedID int64
	if err := store.db.QueryRow(ctx, `
INSERT INTO feeds(user_id, feed_url, feed_type, title, interval_minutes, manual_paused)
VALUES ($1, $2, 'rss', 'circuit', 60, TRUE) RETURNING id`, u.ID, "https://example.com/circuit/"+suffix+".xml").Scan(&feedID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = store.db.Exec(context.Background(), `DELETE FROM feeds WHERE id = $1`, feedID) })

	rowVersion := func() string {
		var x string
		if err := store.db.QueryRow(ctx, `SELECT xmin::text FROM feeds WHERE id = $1`, feedID).Scan(&x); err != nil {
			t.Fatal(err)
		}
		return x
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	failNext := now.Add(2 * time.Hour)
	v0 := rowVersion()
	for i := 1; i <= 2; i++ {
		if err := store.RecordFeedPollFailure(ctx, RecordFeedPollFailureParams{
			ID: feedID, Error: "boom", Threshold: 2, CheckedAt: now, NextCheckAt: failNext, BridgeState: []byte(`{"max":{"last_end_time_ms":5}}`),
		}); err != nil {
			t.Fatalf("record failure %d: %v", i, err)
		}
	}
	f, err := store.GetFeedByID(ctx, feedID)
	if err != nil {
		t.Fatal(err)
	}
	if f.ParsingErrorCount != 2 || !f.PollPaused || f.LastError != "boom" || f.LastCheckedAt == nil || !f.LastCheckedAt.Equal(now) ||
		f.NextCheckAt == nil || !f.NextCheckAt.Equal(failNext) || string(f.BridgeState) != `{"max": {"last_end_time_ms": 5}}` {
		t.Fatalf("after failures: %+v bridge=%s", f, f.BridgeState)
	}
	if v := rowVersion(); v == v0 {
		t.Fatal("failure must write the row")
	}

	okNext := now.Add(time.Hour)
	v1 := rowVersion()
	if err := store.UpdateFeedRefreshMeta(ctx, UpdateFeedRefreshMetaParams{
		ID: feedID, ETag: "e1", LastModified: "lm", LastCheckedAt: now, LastError: "", NextCheckAt: &okNext, BridgeState: []byte(`{"max":{"last_end_time_ms":6}}`),
	}); err != nil {
		t.Fatalf("refresh meta: %v", err)
	}
	f, err = store.GetFeedByID(ctx, feedID)
	if err != nil {
		t.Fatal(err)
	}
	if f.ParsingErrorCount != 0 || f.PollPaused || f.LastError != "" || f.ETag != "e1" || f.LastModified != "lm" ||
		f.NextCheckAt == nil || !f.NextCheckAt.Equal(okNext) || string(f.BridgeState) != `{"max": {"last_end_time_ms": 6}}` {
		t.Fatalf("after success: %+v bridge=%s", f, f.BridgeState)
	}
	if v := rowVersion(); v == v1 {
		t.Fatal("success must write the row")
	}

	// Without NextCheckAt the schedule is left alone.
	if err := store.UpdateFeedRefreshMeta(ctx, UpdateFeedRefreshMetaParams{ID: feedID, LastCheckedAt: now, LastError: "x"}); err != nil {
		t.Fatal(err)
	}
	f, err = store.GetFeedByID(ctx, feedID)
	if err != nil {
		t.Fatal(err)
	}
	if f.NextCheckAt == nil || !f.NextCheckAt.Equal(okNext) {
		t.Fatalf("next_check_at changed without request: %v", f.NextCheckAt)
	}
}
