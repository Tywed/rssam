//go:build integration

package storage

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestIntegration_WebhookDigestQueue: digest rows are parked until the end of
// the window, then claimed with digest_minutes set; peers are claimed by
// webhook; the batch marks touch the counters once per message.
func TestIntegration_WebhookDigestQueue(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "digest")
	_, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 3)

	wh, err := store.CreateWebhook(ctx, CreateWebhookParams{
		UserID: owner.ID, Name: "digest", URL: "https://example.com/d", Method: "POST",
		Headers: []byte(`{}`), Enabled: true, Kind: WebhookKindHTTP, DigestMinutes: 60,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	if wh.DigestMinutes != 60 {
		t.Fatalf("digest_minutes=%d", wh.DigestMinutes)
	}
	if _, err := store.CreateWebhook(ctx, CreateWebhookParams{
		UserID: owner.ID, Name: "bad", URL: "https://example.com/x", Kind: WebhookKindHTTP, DigestMinutes: 1441,
	}); err == nil || !strings.Contains(err.Error(), "digest_minutes") {
		t.Fatalf("expected digest_minutes validation error, got %v", err)
	}

	for _, e := range entries {
		if err := store.EnqueueWebhookLogs(ctx, []int64{wh.ID}, e.ID); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	var due time.Time
	if err := store.db.QueryRow(ctx, `SELECT min(next_retry_at) FROM webhook_logs WHERE webhook_id = $1`, wh.ID).Scan(&due); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	origin := DigestWindowOrigin(now)
	wantDue := origin.Add((now.Sub(origin)/time.Hour + 1) * time.Hour)
	if d := due.Sub(wantDue); d < -time.Second || d > time.Second {
		t.Fatalf("due=%s want end of hour %s", due, wantDue)
	}
	if claimed, err := store.ClaimDueWebhookLogs(ctx, 100); err != nil || len(claimed) != 0 {
		t.Fatalf("parked digest rows must not be claimed: %v %d", err, len(claimed))
	}

	// Window over: rows are due, carry digest_minutes, and peers come along.
	if _, err := store.db.Exec(ctx, `UPDATE webhook_logs SET next_retry_at = now() - interval '1 second' WHERE webhook_id = $1`, wh.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDueWebhookLogs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].DigestMinutes != 60 {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	peers, err := store.ClaimWebhookDigestPeers(ctx, wh.ID, []int64{claimed[0].ID}, 100)
	if err != nil || len(peers) != 2 {
		t.Fatalf("peers: %v %d", err, len(peers))
	}
	ids := []int64{claimed[0].ID, peers[0].ID, peers[1].ID}
	if again, err := store.ClaimWebhookDigestPeers(ctx, wh.ID, ids, 100); err != nil || len(again) != 0 {
		t.Fatalf("parked peers claimed again: %v %d", err, len(again))
	}

	w2, items, err := store.LoadWebhookDigestContext(ctx, wh.ID, ids)
	if err != nil || w2.ID != wh.ID || len(items) != 3 {
		t.Fatalf("load: %v %d", err, len(items))
	}
	if items[0].Feed.Title == "" || items[0].Entry.Title == "" {
		t.Fatalf("item context missing: %+v", items[0])
	}

	next := time.Now().Add(time.Minute)
	code := 503
	if err := store.MarkWebhookLogsFailed(ctx, ids, &code, "boom", "", &next, false, true); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWebhookLogsSent(ctx, ids, 200, "ok"); err != nil {
		t.Fatal(err)
	}
	row, err := store.GetAdminWebhook(ctx, owner.ID, wh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.SentCount != 1 || row.FailedCount != 0 || row.LastError != "boom" {
		t.Fatalf("counters: sent=%d failed=%d err=%q", row.SentCount, row.FailedCount, row.LastError)
	}
	var sent, attempt int
	if err := store.db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'sent'), max(attempt) FROM webhook_logs WHERE webhook_id = $1`, wh.ID).Scan(&sent, &attempt); err != nil {
		t.Fatal(err)
	}
	if sent != 3 || attempt != 2 {
		t.Fatalf("rows sent=%d attempt=%d", sent, attempt)
	}
}

// TestIntegration_SystemAlertLists covers the problem-set queries the alerter
// diffs: paused feeds (circuit / 410), silent feeds, failing webhooks and the
// system_alerts subscription itself.
func TestIntegration_SystemAlertLists(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "alerts")
	feed, _ := newIntegrationFeedWithEntries(t, store, owner.ID, 1)
	feed2, _ := newIntegrationFeedWithEntries(t, store, owner.ID, 1)

	if got, _ := store.ListPausedFeeds(ctx); len(got) != 0 {
		t.Fatalf("paused before: %+v", got)
	}
	now := time.Now()
	if err := store.RecordFeedPollFailure(ctx, RecordFeedPollFailureParams{ID: feed.ID, Error: "dial tcp: refused", Threshold: 1, CheckedAt: now, NextCheckAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// A user pause without an error is not an alert.
	if err := store.SetFeedManualPaused(ctx, feed2.ID, true); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListPausedFeeds(ctx)
	if err != nil || len(got) != 1 || got[0].ID != feed.ID || got[0].Error != "dial tcp: refused" {
		t.Fatalf("paused: %v %+v", err, got)
	}

	if _, err := store.db.Exec(ctx, `UPDATE feeds SET last_entry_at = now() - interval '10 days', created_at = now() - interval '30 days' WHERE id = $1`, feed2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `UPDATE feeds SET manual_paused = FALSE WHERE id = $1`, feed2.ID); err != nil {
		t.Fatal(err)
	}
	silent, err := store.ListSilentFeeds(ctx, 7*24*time.Hour)
	if err != nil || len(silent) != 1 || silent[0].ID != feed2.ID {
		t.Fatalf("silent: %v %+v", err, silent)
	}
	if s0, _ := store.ListSilentFeeds(ctx, 0); s0 != nil {
		t.Fatalf("silentAfter=0 must be disabled")
	}

	wh, err := store.CreateWebhook(ctx, CreateWebhookParams{
		UserID: owner.ID, Name: "ops", URL: "https://example.com/ops", Kind: WebhookKindHTTP, Enabled: true, SystemAlerts: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	subs, err := store.ListSystemAlertWebhooks(ctx)
	if err != nil || len(subs) != 1 || subs[0].ID != wh.ID || !subs[0].SystemAlerts {
		t.Fatalf("subscribers: %v %+v", err, subs)
	}
	if err := store.SetWebhookEnabled(ctx, owner.ID, wh.ID, false); err != nil {
		t.Fatal(err)
	}
	if subs, _ := store.ListSystemAlertWebhooks(ctx); len(subs) != 0 {
		t.Fatalf("disabled webhook must not receive alerts")
	}
	if err := store.SetWebhookEnabled(ctx, owner.ID, wh.ID, true); err != nil {
		t.Fatal(err)
	}

	if f, _ := store.ListFailingWebhooks(ctx); len(f) != 0 {
		t.Fatalf("failing before: %+v", f)
	}
	if _, err := store.db.Exec(ctx, `UPDATE webhooks SET last_failed_at = now(), failed_total = 1, last_error = 'non-2xx status: 500' WHERE id = $1`, wh.ID); err != nil {
		t.Fatal(err)
	}
	f, err := store.ListFailingWebhooks(ctx)
	if err != nil || len(f) != 1 || f[0].ID != wh.ID || f[0].LastError != "non-2xx status: 500" {
		t.Fatalf("failing: %v %+v", err, f)
	}
	if _, err := store.db.Exec(ctx, `UPDATE webhooks SET last_sent_at = now() + interval '1 second' WHERE id = $1`, wh.ID); err != nil {
		t.Fatal(err)
	}
	if f, _ := store.ListFailingWebhooks(ctx); len(f) != 0 {
		t.Fatalf("a success after the failure clears the alert: %+v", f)
	}
}
