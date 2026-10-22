//go:build integration

// End-to-end webhook pipeline on a real PostgreSQL: the storage-level part of
// the chain the worker executes (enqueue → claim → deliver → mark → on_success
// → retention). The HTTP delivery itself (HMAC, template, backoff, dead) lives
// in internal/worker (unit tests with an in-memory store) and in
// internal/service/pipeline_integration_test.go (this store + real refresher).
package storage

import (
	"context"
	"testing"
	"time"
)

// TestIntegration_WebhookOnSuccessEntry_AppliesOnce covers the storage
// contract behind on_success_entry: the action is applied only after every
// webhook of the entry finished and the strongest action wins
// (delete > hash > mark_read).
func TestIntegration_WebhookOnSuccessEntry_ResolvesAcrossWebhooks(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "onsucc")
	_, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 3)

	mk := func(name, onSuccess string) Webhook {
		t.Helper()
		wh, err := store.CreateWebhook(ctx, CreateWebhookParams{
			UserID: owner.ID, Name: name, URL: "https://example.com/" + name, Method: "POST",
			Headers: []byte(`{}`), Enabled: true, Kind: WebhookKindHTTP, OnSuccessEntry: onSuccess,
		})
		if err != nil {
			t.Fatalf("create webhook %s: %v", name, err)
		}
		t.Cleanup(func() { _ = store.DeleteWebhook(context.Background(), owner.ID, wh.ID) })
		return wh
	}
	whRead := mk("read", WebhookOnSuccessMarkRead)
	whHash := mk("hash", WebhookOnSuccessHash)
	whDel := mk("del", WebhookOnSuccessDelete)

	// Entry 0 goes to read+hash, entry 1 to del alone, entry 2 to read alone.
	if err := store.EnqueueWebhookLogs(ctx, []int64{whRead.ID, whHash.ID}, entries[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := store.EnqueueWebhookLogs(ctx, []int64{whDel.ID}, entries[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := store.EnqueueWebhookLogs(ctx, []int64{whRead.ID}, entries[2].ID); err != nil {
		t.Fatal(err)
	}
	byKey := map[[2]int64]WebhookLog{}
	for _, l := range claimAllDueWebhookLogs(t, store) {
		byKey[[2]int64{l.WebhookID, l.EntryID}] = l
	}
	get := func(wh Webhook, e Entry) WebhookLog {
		t.Helper()
		l, ok := byKey[[2]int64{wh.ID, e.ID}]
		if !ok {
			t.Fatalf("log for webhook %d entry %d not claimed", wh.ID, e.ID)
		}
		return l
	}

	// Entry 0: after the first webhook is sent one log is still incomplete →
	// the worker must not apply the action yet.
	l0read := get(whRead, entries[0])
	if err := store.MarkWebhookLogSent(ctx, l0read.ID, 1, 200, ""); err != nil {
		t.Fatal(err)
	}
	n, action, err := store.EntryWebhookOutcome(ctx, entries[0].ID, l0read.ID)
	if err != nil || n != 1 {
		t.Fatalf("incomplete after first sent: n=%d err=%v", n, err)
	}
	l0hash := get(whHash, entries[0])
	if err := store.MarkWebhookLogSent(ctx, l0hash.ID, 1, 200, ""); err != nil {
		t.Fatal(err)
	}
	n, action, err = store.EntryWebhookOutcome(ctx, entries[0].ID, l0hash.ID)
	if err != nil || n != 0 {
		t.Fatalf("incomplete after both sent: n=%d err=%v", n, err)
	}
	if merged := MergeOnSuccessActions([]string{WebhookOnSuccessHash, action}); merged != WebhookOnSuccessHash {
		t.Fatalf("merged action for read+hash = %q, want hash", merged)
	}
	if err := store.StripEntryPayloadAfterWebhook(ctx, entries[0].ID); err != nil {
		t.Fatal(err)
	}
	e0, err := store.GetEntry(ctx, owner.ID, entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if e0.Title != "" || e0.Content != "" || e0.Status != EntryStatusRemoved || e0.Hash != entries[0].Hash {
		t.Fatalf("hashed entry: title=%q content=%q status=%q hash=%q", e0.Title, e0.Content, e0.Status, e0.Hash)
	}

	// Entry 1: delete keeps the payload but removes the entry.
	if err := store.MarkEntryRemovedKeepPayload(ctx, owner.ID, entries[1].ID); err != nil {
		t.Fatal(err)
	}
	e1, err := store.GetEntry(ctx, owner.ID, entries[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if e1.Status != EntryStatusRemoved || e1.Title == "" {
		t.Fatalf("deleted entry: status=%q title=%q", e1.Status, e1.Title)
	}

	// Entry 2: mark_read only flips unread → read.
	if err := store.MarkEntryReadIfActive(ctx, owner.ID, entries[2].ID); err != nil {
		t.Fatal(err)
	}
	e2, err := store.GetEntry(ctx, owner.ID, entries[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	if e2.Status != EntryStatusRead || e2.Title == "" {
		t.Fatalf("read entry: status=%q title=%q", e2.Status, e2.Title)
	}
	// ...and is idempotent for non-unread rows.
	if err := store.MarkEntryReadIfActive(ctx, owner.ID, entries[1].ID); err != nil {
		t.Fatal(err)
	}
	e1, _ = store.GetEntry(ctx, owner.ID, entries[1].ID)
	if e1.Status != EntryStatusRemoved {
		t.Fatalf("mark_read must not resurrect removed entries: %q", e1.Status)
	}

	// Duplicate enqueue for an already-sent (webhook, entry) pair is a no-op.
	if err := store.EnqueueWebhookLogs(ctx, []int64{whRead.ID}, entries[2].ID); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := store.ListWebhookLogs(ctx, whRead.ID, 10, 0); total != 2 {
		t.Fatalf("logs for whRead = %d, want 2 (no duplicate)", total)
	}
}

// TestIntegration_WebhookLogRetention_OnlyOldRowsPurged: WEBHOOK_LOG_RETENTION_DAYS
// removes only rows older than the cutoff, regardless of status.
func TestIntegration_WebhookLogRetention_OnlyOldRowsPurged(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "whret")
	_, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 3)
	wh, err := store.CreateWebhook(ctx, CreateWebhookParams{
		UserID: owner.ID, Name: "ret", URL: "https://example.com/ret", Method: "POST",
		Headers: []byte(`{}`), Enabled: true, Kind: WebhookKindHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DeleteWebhook(context.Background(), owner.ID, wh.ID) })
	for _, e := range entries {
		if err := store.EnqueueWebhookLogs(ctx, []int64{wh.ID}, e.ID); err != nil {
			t.Fatal(err)
		}
	}
	logs, _, err := store.ListWebhookLogs(ctx, wh.ID, 10, 0)
	if err != nil || len(logs) != 3 {
		t.Fatalf("logs=%d err=%v", len(logs), err)
	}
	// Age two of them: one sent, one still pending (retention does not care).
	if err := store.MarkWebhookLogSent(ctx, logs[0].ID, 1, 200, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `UPDATE webhook_logs SET created_at = now() - interval '2 days' WHERE id = ANY($1)`, []int64{logs[0].ID, logs[1].ID}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	res, err := store.RunRetentionCleanup(ctx, RetentionCleanupOpts{
		RemovedEntriesBefore: RetentionCutoff(now, 3650),
		WebhookLogsBefore:    RetentionCutoff(now, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.WebhookLogs < 2 {
		t.Fatalf("deleted webhook logs = %d, want >= 2", res.WebhookLogs)
	}
	rest, total, err := store.ListWebhookLogs(ctx, wh.ID, 10, 0)
	if err != nil || total != 1 || rest[0].ID != logs[2].ID {
		t.Fatalf("remaining logs: total=%d err=%v", total, err)
	}
	// The sent counter survived (persistent stats).
	row, err := store.GetAdminWebhook(ctx, owner.ID, wh.ID)
	if err != nil || row.SentCount != 1 {
		t.Fatalf("sent_total after retention = %d err=%v", row.SentCount, err)
	}
}

// TestIntegration_MarkEntriesRemoved_TenantScopedSoftDelete: the filter
// "delete" action must soft-delete through MarkEntriesRemoved because
// BulkUpdateEntries refuses status=removed by contract (regression: the
// action silently did nothing).
func TestIntegration_MarkEntriesRemoved_TenantScopedSoftDelete(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "rm_owner")
	other := newIntegrationUser(t, store, "rm_other")
	_, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 2)
	ids := []int64{entries[0].ID, entries[1].ID}

	removed := EntryStatusRemoved
	if _, err := store.BulkUpdateEntries(ctx, owner.ID, ids, BulkEntryUpdate{Status: &removed}); err == nil {
		t.Fatal("BulkUpdateEntries must reject status=removed")
	}

	// Another tenant cannot remove the owner's entries.
	if n, err := store.MarkEntriesRemoved(ctx, other.ID, ids); err != nil || n != 0 {
		t.Fatalf("cross-tenant remove: n=%d err=%v", n, err)
	}
	n, err := store.MarkEntriesRemoved(ctx, owner.ID, ids)
	if err != nil || n != 2 {
		t.Fatalf("remove: n=%d err=%v", n, err)
	}
	for _, id := range ids {
		e, err := store.GetEntry(ctx, owner.ID, id)
		if err != nil || e.Status != EntryStatusRemoved || e.Title == "" {
			t.Fatalf("entry %d after remove: status=%q title=%q err=%v", id, e.Status, e.Title, err)
		}
	}
	// Idempotent: already removed rows are not touched again.
	if n, err := store.MarkEntriesRemoved(ctx, owner.ID, ids); err != nil || n != 0 {
		t.Fatalf("second remove: n=%d err=%v", n, err)
	}
	if n, err := store.MarkEntriesRemoved(ctx, owner.ID, nil); err != nil || n != 0 {
		t.Fatalf("empty remove: n=%d err=%v", n, err)
	}
}

// TestIntegration_RelayEntryDeletedAfterWebhook: a relay entry is deleted
// once its last delivery is done; the hash stays in feed_entry_dedup, the
// log rows survive with their feed, and the claim path ignores the row.
func TestIntegration_RelayEntryDeletedAfterWebhook(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "relaydel")
	feed, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 2)
	mk := func(name string) Webhook {
		t.Helper()
		wh, err := store.CreateWebhook(ctx, CreateWebhookParams{
			UserID: owner.ID, Name: name, URL: "https://example.com/" + name, Method: "POST",
			Headers: []byte(`{}`), Enabled: true, Kind: WebhookKindHTTP,
		})
		if err != nil {
			t.Fatal(err)
		}
		return wh
	}
	whA, whB := mk("a"), mk("b")
	if err := store.EnqueueWebhookLogs(ctx, []int64{whA.ID, whB.ID}, entries[0].ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDueWebhookLogs(ctx, 10, nil)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("claim: %v %d", err, len(claimed))
	}
	// The claim writes nothing; exclude hides the webhooks this process holds.
	held := []int64{claimed[0].WebhookID, claimed[1].WebhookID}
	if again, err := store.ClaimDueWebhookLogs(ctx, 10, held); err != nil || len(again) != 0 {
		t.Fatalf("excluded webhooks claimed again: %v %d", err, len(again))
	}
	if _, ok, err := store.NextWebhookRetryAt(ctx, held); err != nil || ok {
		t.Fatalf("next retry must ignore in-flight rows: ok=%v err=%v", ok, err)
	}

	if err := store.MarkWebhookLogSent(ctx, claimed[0].ID, 1, 200, ""); err != nil {
		t.Fatal(err)
	}
	if done, err := store.DeleteRelayEntryAfterWebhook(ctx, entries[0].ID, claimed[0].ID); err != nil || done {
		t.Fatalf("entry deleted while a delivery is pending: done=%v err=%v", done, err)
	}
	if err := store.MarkWebhookLogSent(ctx, claimed[1].ID, 1, 200, ""); err != nil {
		t.Fatal(err)
	}
	if done, err := store.DeleteRelayEntryAfterWebhook(ctx, entries[0].ID, claimed[1].ID); err != nil || !done {
		t.Fatalf("entry not deleted after the last delivery: done=%v err=%v", done, err)
	}
	var n int
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM entries WHERE id = $1`, entries[0].ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("entry row still there: n=%d err=%v", n, err)
	}
	known, err := store.FilterKnownEntryHashes(ctx, feed.ID, []string{entries[0].Hash})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := known[entries[0].Hash]; !ok {
		t.Fatal("deleted relay entry must stay known to dedup")
	}
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM feed_entry_dedup WHERE feed_id = $1`, feed.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("dedup rows for the feed: n=%d err=%v (only the deleted entry)", n, err)
	}
	rows, total, err := store.ListWebhookLogRows(ctx, whA.ID, "", nil, 10, 0)
	if err != nil || total != 1 || rows[0].EntryID != entries[0].ID || rows[0].FeedID != feed.ID || rows[0].EntryTitle != "" {
		t.Fatalf("log row after entry delete: total=%d err=%v rows=%+v", total, err, rows)
	}
	if logs, total, err := store.ListWebhookLogs(ctx, whB.ID, 10, 0); err != nil || total != 1 || logs[0].Status != "sent" {
		t.Fatalf("plain log list: total=%d err=%v", total, err)
	}
}

// TestIntegration_ClaimOneRowPerWebhookInOrder: the dispatcher gets the
// oldest due row of every webhook it is not busy with, so one webhook
// delivers one message at a time and in enqueue order.
func TestIntegration_ClaimOneRowPerWebhookInOrder(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "serial")
	_, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 3)
	mk := func(name string) Webhook {
		t.Helper()
		wh, err := store.CreateWebhook(ctx, CreateWebhookParams{
			UserID: owner.ID, Name: name, URL: "https://example.com/" + name, Method: "POST",
			Headers: []byte(`{}`), Enabled: true, Kind: WebhookKindHTTP,
		})
		if err != nil {
			t.Fatal(err)
		}
		return wh
	}
	whA, whB := mk("a"), mk("b")
	for _, e := range entries {
		if err := store.EnqueueWebhookLogs(ctx, []int64{whA.ID, whB.ID}, e.ID); err != nil {
			t.Fatal(err)
		}
	}

	claimed, err := store.ClaimDueWebhookLogs(ctx, 100, nil)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("claim: %v %d (want one row per webhook)", err, len(claimed))
	}
	for _, l := range claimed {
		if l.EntryID != entries[0].ID {
			t.Fatalf("webhook %d: got entry %d, want the oldest %d", l.WebhookID, l.EntryID, entries[0].ID)
		}
	}
	// Webhook A is busy (or paused): only B's row is claimable, and the
	// earliest retry known to the dispatcher is B's as well.
	only, err := store.ClaimDueWebhookLogs(ctx, 100, []int64{whA.ID})
	if err != nil || len(only) != 1 || only[0].WebhookID != whB.ID {
		t.Fatalf("claim with A excluded: %v %+v", err, only)
	}
	if _, ok, err := store.NextWebhookRetryAt(ctx, []int64{whA.ID, whB.ID}); err != nil || ok {
		t.Fatalf("both busy: ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.NextWebhookRetryAt(ctx, []int64{whA.ID}); err != nil || !ok {
		t.Fatalf("B due: ok=%v err=%v", ok, err)
	}
	// After A's first row is sent, the next claim hands out A's second entry.
	if err := store.MarkWebhookLogSent(ctx, claimed[0].ID, 1, 200, ""); err != nil {
		t.Fatal(err)
	}
	next, err := store.ClaimDueWebhookLogs(ctx, 100, []int64{whB.ID})
	if err != nil || len(next) != 1 || next[0].WebhookID != claimed[0].WebhookID || next[0].EntryID != entries[1].ID {
		t.Fatalf("second row of the sent webhook: %v %+v", err, next)
	}
}
