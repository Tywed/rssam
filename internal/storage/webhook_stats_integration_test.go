//go:build integration

package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

// claimOwnWebhookLogs claims due logs and returns only those of webhookID.
func claimOwnWebhookLogs(t *testing.T, store *PostgresStore, webhookID int64, want int) []WebhookLog {
	t.Helper()
	claimed, err := store.ClaimDueWebhookLogs(context.Background(), 1000)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	var mine []WebhookLog
	for _, l := range claimed {
		if l.WebhookID == webhookID {
			mine = append(mine, l)
		}
	}
	if len(mine) != want {
		t.Fatalf("claimed %d of our logs, want %d", len(mine), want)
	}
	return mine
}

// TestIntegration_WebhookStatsPersistAcrossLogRetention covers the persistent
// per-webhook counters (migration 0031):
//
//   - MarkWebhookLogSent / MarkWebhookLogFailed maintain sent_total,
//     failed_total, last_sent_at, last_failed_at, last_error;
//   - transient failures (retry scheduled) update last_error but do not count
//     as failed deliveries;
//   - the counters survive RunRetentionCleanup purging webhook_logs;
//   - ResetWebhookStats clears them (tenant-scoped) and leaves the logs alone.
func TestIntegration_WebhookStatsPersistAcrossLogRetention(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "whstats")
	other := newIntegrationUser(t, store, "whstats_other")
	_, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 4)

	wh, err := store.CreateWebhook(ctx, CreateWebhookParams{
		UserID: owner.ID, Name: "stats", URL: "https://example.com/stats", Method: "POST",
		Headers: []byte(`{}`), Enabled: true, Kind: WebhookKindHTTP,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}

	row, err := store.GetAdminWebhook(ctx, owner.ID, wh.ID)
	if err != nil {
		t.Fatalf("get admin webhook: %v", err)
	}
	if row.SentCount != 0 || row.FailedCount != 0 || row.LastSentAt != nil || row.LastFailedAt != nil || row.LastError != "" {
		t.Fatalf("fresh webhook has non-zero stats: %+v", row)
	}

	// Enqueue 4 deliveries: 2 sent, 1 transient failure, 1 dead.
	for _, e := range entries {
		if err := store.EnqueueWebhookLogs(ctx, []int64{wh.ID}, e.ID); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	mine := claimOwnWebhookLogs(t, store, wh.ID, 4)

	if err := store.MarkWebhookLogSent(ctx, mine[0].ID, 1, 200, "ok"); err != nil {
		t.Fatalf("mark sent 1: %v", err)
	}
	if err := store.MarkWebhookLogSent(ctx, mine[1].ID, 1, 204, ""); err != nil {
		t.Fatalf("mark sent 2: %v", err)
	}
	code503 := 503
	future := time.Now().Add(time.Hour)
	if err := store.MarkWebhookLogFailed(ctx, mine[2].ID, &code503, "non-2xx: 503", "", 1, &future, false); err != nil {
		t.Fatalf("mark failed transient: %v", err)
	}

	row, err = store.GetAdminWebhook(ctx, owner.ID, wh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.SentCount != 2 || row.FailedCount != 0 {
		t.Fatalf("after 2 sent + 1 transient: sent=%d failed=%d", row.SentCount, row.FailedCount)
	}
	if row.LastSentAt == nil || row.LastFailedAt != nil {
		t.Fatalf("transient failure must not set last_failed_at: %+v", row)
	}
	if row.LastError != "non-2xx: 503" || row.LastErrorAt == nil {
		t.Fatalf("transient failure must record last_error: %q at %v", row.LastError, row.LastErrorAt)
	}
	if row.RetryingCount != 1 {
		t.Fatalf("retrying=%d want 1", row.RetryingCount)
	}

	code400 := 400
	if err := store.MarkWebhookLogFailed(ctx, mine[3].ID, &code400, "non-2xx: 400", "bad", 1, nil, true); err != nil {
		t.Fatalf("mark dead: %v", err)
	}
	row, err = store.GetAdminWebhook(ctx, owner.ID, wh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.SentCount != 2 || row.FailedCount != 1 {
		t.Fatalf("after dead: sent=%d failed=%d", row.SentCount, row.FailedCount)
	}
	if row.LastFailedAt == nil || !row.LastFailedAt.After(*row.LastSentAt) {
		t.Fatalf("last_failed_at must be after last_sent_at: failed=%v sent=%v", row.LastFailedAt, row.LastSentAt)
	}
	if row.LastError != "non-2xx: 400" {
		t.Fatalf("last_error=%q", row.LastError)
	}

	sum, err := store.AdminWebhookSummary(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sum.FailedTotal != 1 || sum.Sent24h != 2 {
		t.Fatalf("summary: %+v", sum)
	}

	// A later success moves last_sent_at past last_failed_at (the UI derives
	// "ok" from exactly this ordering).
	if err := store.RetryWebhookLogNow(ctx, owner.ID, mine[3].ID); err != nil {
		t.Fatalf("retry dead: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := store.MarkWebhookLogSent(ctx, mine[3].ID, 2, 200, "ok"); err != nil {
		t.Fatalf("mark sent after retry: %v", err)
	}
	row, err = store.GetAdminWebhook(ctx, owner.ID, wh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.SentCount != 3 || row.FailedCount != 1 {
		t.Fatalf("after retry success: sent=%d failed=%d", row.SentCount, row.FailedCount)
	}
	if !row.LastSentAt.After(*row.LastFailedAt) {
		t.Fatalf("last_sent_at must now be after last_failed_at: sent=%v failed=%v", row.LastSentAt, row.LastFailedAt)
	}

	// Retention wipes every log of this webhook; the counters must not move.
	if _, err := store.db.Exec(ctx, `UPDATE webhook_logs SET created_at = now() - interval '400 days' WHERE webhook_id = $1`, wh.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.RunRetentionCleanup(ctx, RetentionCleanupOpts{
		RemovedEntriesBefore: RetentionCutoff(now, 3650),
		WebhookLogsBefore:    RetentionCutoff(now, 1),
	}); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, total, err := store.ListWebhookLogs(ctx, wh.ID, 10, 0); err != nil || total != 0 {
		t.Fatalf("logs after retention: total=%d err=%v", total, err)
	}
	row, err = store.GetAdminWebhook(ctx, owner.ID, wh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.SentCount != 3 || row.FailedCount != 1 || row.LastSentAt == nil || row.LastFailedAt == nil || row.LastError == "" {
		t.Fatalf("stats lost after retention: %+v", row)
	}
	if row.Sent24h != 0 || row.RetryingCount != 0 || row.QueueDueCount != 0 {
		t.Fatalf("queue counters must follow the logs: %+v", row)
	}

	// Reset is tenant-scoped and explicit.
	if err := store.ResetWebhookStats(ctx, other.ID, wh.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign reset: err=%v want ErrNotFound", err)
	}
	if err := store.ResetWebhookStats(ctx, owner.ID, wh.ID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	row, err = store.GetAdminWebhook(ctx, owner.ID, wh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.SentCount != 0 || row.FailedCount != 0 || row.LastSentAt != nil || row.LastFailedAt != nil || row.LastError != "" || row.LastErrorAt != nil {
		t.Fatalf("stats after reset: %+v", row)
	}
	if row.StatsResetAt == nil {
		t.Fatal("stats_reset_at must be set after reset")
	}

	// Counters resume from zero after a reset.
	if err := store.EnqueueWebhookLogs(ctx, []int64{wh.ID}, entries[0].ID); err != nil {
		t.Fatal(err)
	}
	again := claimOwnWebhookLogs(t, store, wh.ID, 1)
	if err := store.MarkWebhookLogSent(ctx, again[0].ID, 1, 200, ""); err != nil {
		t.Fatal(err)
	}
	row, err = store.GetAdminWebhook(ctx, owner.ID, wh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.SentCount != 1 || row.FailedCount != 0 {
		t.Fatalf("after reset + 1 sent: sent=%d failed=%d", row.SentCount, row.FailedCount)
	}

	if err := store.DeleteWebhook(ctx, owner.ID, wh.ID); err != nil {
		t.Fatalf("delete webhook: %v", err)
	}
}

// TestIntegration_WebhookStatsDisabledParkDoesNotCount: a delivery parked
// because the webhook is paused ("webhook is disabled", retry scheduled) must
// not pollute the webhook's error counters.
func TestIntegration_WebhookStatsDisabledParkDoesNotCount(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "whpark")
	_, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 1)

	wh, err := store.CreateWebhook(ctx, CreateWebhookParams{
		UserID: owner.ID, Name: "park", URL: "https://example.com/park", Method: "POST",
		Headers: []byte(`{}`), Enabled: true, Kind: WebhookKindHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnqueueWebhookLogs(ctx, []int64{wh.ID}, entries[0].ID); err != nil {
		t.Fatal(err)
	}
	mine := claimOwnWebhookLogs(t, store, wh.ID, 1)
	if err := store.SetWebhookEnabled(ctx, owner.ID, wh.ID, false); err != nil {
		t.Fatal(err)
	}
	next := time.Now().Add(time.Minute)
	if err := store.MarkWebhookLogFailed(ctx, mine[0].ID, nil, "webhook is disabled", "", 0, &next, false); err != nil {
		t.Fatal(err)
	}
	row, err := store.GetAdminWebhook(ctx, owner.ID, wh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.FailedCount != 0 || row.LastError != "" || row.LastErrorAt != nil || row.LastFailedAt != nil {
		t.Fatalf("parked delivery must not touch stats: %+v", row)
	}
	if err := store.DeleteWebhook(ctx, owner.ID, wh.ID); err != nil {
		t.Fatal(err)
	}
}
