//go:build integration

package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"rssam/internal/auth"
)

// newIntegrationUser creates a throw-away user; DeleteUser cascades to every
// owned row so each test starts from a clean slate.
func newIntegrationUser(t *testing.T, store *PostgresStore, tag string) User {
	t.Helper()
	hash, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(context.Background(), CreateUserParams{
		Username:     fmt.Sprintf("int_%s_%d", tag, time.Now().UnixNano()),
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("create user %s: %v", tag, err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), u.ID) })
	return u
}

func newIntegrationFeedWithEntries(t *testing.T, store *PostgresStore, userID int64, n int) (Feed, []Entry) {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d-%d", userID, time.Now().UnixNano())
	feed, err := store.CreateFeed(ctx, userID, CreateFeedParams{
		FeedURL:         "https://example.com/rss/" + suffix + ".xml",
		FeedType:        "rss",
		Title:           "feed " + suffix,
		IntervalMinutes: 60,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	params := make([]CreateEntryParams, 0, n)
	for i := range n {
		params = append(params, CreateEntryParams{
			Title:   fmt.Sprintf("entry %d", i),
			URL:     fmt.Sprintf("https://example.com/e/%s/%d", suffix, i),
			Content: "body",
			Hash:    fmt.Sprintf("hash-%s-%d", suffix, i),
		})
	}
	_, entries, err := store.CreateEntries(ctx, feed.ID, params)
	if err != nil {
		t.Fatalf("create entries: %v", err)
	}
	if len(entries) != n {
		t.Fatalf("inserted %d entries, want %d", len(entries), n)
	}
	return feed, entries
}

func TestIntegration_WebhookLogsOwnershipAndRetry(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "wh_owner")
	other := newIntegrationUser(t, store, "wh_other")
	_, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 2)

	wh, err := store.CreateWebhook(ctx, CreateWebhookParams{
		UserID: owner.ID, Name: "hook", URL: "https://example.com/hook", Method: "POST",
		Headers: []byte(`{}`), Enabled: true, Kind: WebhookKindHTTP,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}

	// Webhook itself is tenant-scoped.
	if _, err := store.GetWebhook(ctx, other.ID, wh.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user GetWebhook: err=%v want ErrNotFound", err)
	}

	// Enqueue is idempotent per (webhook, entry).
	for range 2 {
		if err := store.EnqueueWebhookLogs(ctx, []int64{wh.ID}, entries[0].ID); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	if err := store.EnqueueWebhookLogs(ctx, []int64{wh.ID}, entries[1].ID); err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}
	logs, total, err := store.ListWebhookLogs(ctx, wh.ID, 10, 0)
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if total != 2 || len(logs) != 2 {
		t.Fatalf("logs after enqueue: total=%d len=%d", total, len(logs))
	}

	// Claim → mark one sent, one failed with a far-future retry.
	claimed, err := store.ClaimDueWebhookLogs(ctx, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	var mine []WebhookLog
	for _, l := range claimed {
		if l.WebhookID == wh.ID {
			mine = append(mine, l)
		}
	}
	if len(mine) != 2 {
		t.Fatalf("claimed %d of our logs, want 2", len(mine))
	}
	sentID, failedID := mine[0].ID, mine[1].ID
	if err := store.MarkWebhookLogSent(ctx, sentID, 1, 200, "ok"); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	code := 503
	future := time.Now().Add(time.Hour)
	if err := store.MarkWebhookLogFailed(ctx, failedID, &code, "boom", "", 1, &future, false); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	// A failed row scheduled for later is not claimable now...
	claimed, err = store.ClaimDueWebhookLogs(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range claimed {
		if l.ID == failedID || l.ID == sentID {
			t.Fatalf("log %d should not be due", l.ID)
		}
	}

	// ...a foreign user cannot force it...
	if err := store.RetryWebhookLogNow(ctx, other.ID, failedID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign retry: err=%v want ErrNotFound", err)
	}
	// ...a sent row cannot be retried...
	if err := store.RetryWebhookLogNow(ctx, owner.ID, sentID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retry sent row: err=%v want ErrNotFound", err)
	}
	// ...but the owner can, and it becomes due immediately.
	if err := store.RetryWebhookLogNow(ctx, owner.ID, failedID); err != nil {
		t.Fatalf("owner retry: %v", err)
	}
	claimed, err = store.ClaimDueWebhookLogs(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range claimed {
		if l.ID == failedID {
			found = true
			if l.Status != "pending" {
				t.Fatalf("retried status=%q want pending", l.Status)
			}
		}
	}
	if !found {
		t.Fatal("retried log was not claimable")
	}

	// Deleting the webhook removes its logs (cascade) and is tenant-scoped.
	if err := store.DeleteWebhook(ctx, other.ID, wh.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign delete: err=%v", err)
	}
	if err := store.DeleteWebhook(ctx, owner.ID, wh.ID); err != nil {
		t.Fatalf("delete webhook: %v", err)
	}
	if _, total, err := store.ListWebhookLogs(ctx, wh.ID, 10, 0); err != nil || total != 0 {
		t.Fatalf("logs after delete: total=%d err=%v", total, err)
	}
}

func TestIntegration_FiltersCRUDAndMatches(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "flt_owner")
	other := newIntegrationUser(t, store, "flt_other")
	feed, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 1)

	f, err := store.CreateFilter(ctx, CreateFilterParams{
		UserID: owner.ID, Name: "Foo", Enabled: true, FeedScope: FilterFeedScopeInclude,
		Rules:      []CreateFilterRuleParams{{Field: "title", Pattern: "foo", Op: "and", Priority: 1}},
		ScopeItems: []CreateFilterScopeItemParams{{FeedID: &feed.ID}},
		Actions:    []CreateFilterActionParams{{ActionType: "delete"}},
	})
	if err != nil {
		t.Fatalf("create filter: %v", err)
	}
	if len(f.Rules) != 1 || len(f.ScopeItems) != 1 || len(f.Actions) != 1 {
		t.Fatalf("created filter children: rules=%d scope=%d actions=%d", len(f.Rules), len(f.ScopeItems), len(f.Actions))
	}

	if _, err := store.GetFilter(ctx, other.ID, f.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign GetFilter: err=%v", err)
	}
	got, err := store.GetFilter(ctx, owner.ID, f.ID)
	if err != nil || got.Name != "Foo" || got.Rules[0].Pattern != "foo" {
		t.Fatalf("GetFilter: %+v err=%v", got, err)
	}

	list, total, err := store.ListFilters(ctx, owner.ID, 10, 0)
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("ListFilters: total=%d len=%d err=%v", total, len(list), err)
	}
	if _, total, _ := store.ListFilters(ctx, other.ID, 10, 0); total != 0 {
		t.Fatalf("foreign ListFilters total=%d", total)
	}

	// Update replaces children wholesale.
	upd, err := store.UpdateFilter(ctx, UpdateFilterParams{
		ID: f.ID, UserID: owner.ID, Name: "Bar", Enabled: false, FeedScope: FilterFeedScopeAll,
		Rules: []CreateFilterRuleParams{
			{Field: "title", Pattern: "bar", Op: "and"},
			{Field: "content", Pattern: "baz", Op: "or"},
		},
	})
	if err != nil {
		t.Fatalf("update filter: %v", err)
	}
	if upd.Name != "Bar" || upd.Enabled || len(upd.Rules) != 2 || len(upd.ScopeItems) != 0 || len(upd.Actions) != 0 {
		t.Fatalf("updated filter: %+v", upd)
	}
	if _, err := store.UpdateFilter(ctx, UpdateFilterParams{ID: f.ID, UserID: other.ID, Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign update: err=%v", err)
	}

	// Matches + counter.
	n, err := store.CreateFilterMatches(ctx, []CreateFilterMatchParams{
		{FilterID: f.ID, EntryID: entries[0].ID, MatchedAt: time.Now(), Details: []byte(`{"ok":true}`)},
		{FilterID: f.ID, EntryID: entries[0].ID, MatchedAt: time.Now(), Details: []byte(`{"dup":true}`)}, // duplicate
	})
	if err != nil {
		t.Fatalf("create matches: %v", err)
	}
	if n != 1 {
		t.Fatalf("inserted matches=%d want 1 (duplicate ignored)", n)
	}
	// CreateFilterMatches bumps match_count itself (only for rows actually inserted).
	got, _ = store.GetFilter(ctx, owner.ID, f.ID)
	if got.MatchCount != 1 {
		t.Fatalf("match_count=%d want 1", got.MatchCount)
	}
	if err := store.IncrementFilterMatchCount(ctx, f.ID, 2); err != nil {
		t.Fatal(err)
	}
	if got, _ = store.GetFilter(ctx, owner.ID, f.ID); got.MatchCount != 3 {
		t.Fatalf("match_count after increment=%d want 3", got.MatchCount)
	}
	rows, total, err := store.ListFilterMatches(ctx, f.ID, 10, 0)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].Entry.ID != entries[0].ID || rows[0].Entry.Title != "entry 0" {
		t.Fatalf("ListFilterMatches: total=%d rows=%+v err=%v", total, rows, err)
	}

	// Delete is tenant-scoped and cascades to matches.
	if err := store.DeleteFilter(ctx, other.ID, f.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign delete: err=%v", err)
	}
	if err := store.DeleteFilter(ctx, owner.ID, f.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, total, _ := store.ListFilterMatches(ctx, f.ID, 10, 0); total != 0 {
		t.Fatalf("matches after delete: %d", total)
	}
	if err := store.DeleteFilter(ctx, owner.ID, f.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: err=%v", err)
	}
}

func TestIntegration_FeedEntriesUpdateAndBulk(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "ent_owner")
	other := newIntegrationUser(t, store, "ent_other")
	feed, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 3)

	// GetFeedEntry / UpdateEntry are scoped by user AND feed.
	if _, err := store.GetFeedEntry(ctx, other.ID, feed.ID, entries[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign GetFeedEntry: %v", err)
	}
	if _, err := store.GetFeedEntry(ctx, owner.ID, feed.ID+1_000_000, entries[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong feed GetFeedEntry: %v", err)
	}
	read, starred := "read", true
	e, err := store.UpdateEntry(ctx, owner.ID, feed.ID, entries[0].ID, UpdateEntryParams{Status: &read, Starred: &starred})
	if err != nil {
		t.Fatalf("UpdateEntry: %v", err)
	}
	if e.Status != "read" || !e.Starred {
		t.Fatalf("updated entry: %+v", e)
	}
	if _, err := store.UpdateEntry(ctx, other.ID, feed.ID, entries[0].ID, UpdateEntryParams{Starred: &starred}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign UpdateEntry: %v", err)
	}

	// ListFeedEntries with status filter.
	unread := "unread"
	list, total, err := store.ListFeedEntries(ctx, owner.ID, feed.ID, ListEntriesFilter{Status: &unread, Limit: 10})
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("unread list: total=%d len=%d err=%v", total, len(list), err)
	}
	if _, total, _ := store.ListFeedEntries(ctx, other.ID, feed.ID, ListEntriesFilter{Limit: 10}); total != 0 {
		t.Fatalf("foreign ListFeedEntries total=%d", total)
	}

	// Unread counters per feed.
	byFeed, _, err := store.UnreadCountsForUser(ctx, owner.ID)
	if err != nil || byFeed[feed.ID] != 2 {
		t.Fatalf("UnreadCountsForUser: %v err=%v", byFeed, err)
	}

	// Bulk update ignores ids the user does not own.
	n, err := store.BulkUpdateEntries(ctx, other.ID, []int64{entries[1].ID, entries[2].ID}, BulkEntryUpdate{Status: &read})
	if err != nil || n != 0 {
		t.Fatalf("foreign bulk: n=%d err=%v", n, err)
	}
	n, err = store.BulkUpdateEntries(ctx, owner.ID, []int64{entries[1].ID}, BulkEntryUpdate{Status: &read})
	if err != nil || n != 1 {
		t.Fatalf("bulk: n=%d err=%v", n, err)
	}
	n, err = store.MarkAllFeedEntriesRead(ctx, owner.ID, feed.ID)
	if err != nil || n != 1 {
		t.Fatalf("mark all read: n=%d err=%v", n, err)
	}
	byFeed, _, _ = store.UnreadCountsForUser(ctx, owner.ID)
	if byFeed[feed.ID] != 0 {
		t.Fatalf("unread after mark all: %d", byFeed[feed.ID])
	}
}

func TestIntegration_APIKeysLifecycle(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "key_owner")
	other := newIntegrationUser(t, store, "key_other")

	raw, hash, err := auth.NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	k, err := store.CreateAPIKey(ctx, CreateAPIKeyParams{UserID: owner.ID, Name: "cli", TokenHash: hash})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	got, err := store.LookupAPIKey(ctx, auth.HashToken(raw))
	if err != nil || got.ID != k.ID || got.UserID != owner.ID {
		t.Fatalf("lookup: %+v err=%v", got, err)
	}
	if _, err := store.LookupAPIKey(ctx, auth.HashToken(raw+"x")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lookup wrong token: %v", err)
	}
	if err := store.TouchAPIKeyUsed(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	keys, err := store.ListAPIKeys(ctx, owner.ID)
	if err != nil || len(keys) != 1 || keys[0].LastUsedAt == nil || keys[0].Scope != "admin" || keys[0].ExpiresAt != nil {
		t.Fatalf("list keys: %+v err=%v", keys, err)
	}
	// last_used_at is refreshed at most once an hour.
	firstUsed := *keys[0].LastUsedAt
	if err := store.TouchAPIKeyUsed(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	if keys, _ = store.ListAPIKeys(ctx, owner.ID); !keys[0].LastUsedAt.Equal(firstUsed) {
		t.Fatalf("second touch within an hour rewrote last_used_at: %s -> %s", firstUsed, *keys[0].LastUsedAt)
	}

	exp := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	_, hash2, err := auth.NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := store.CreateAPIKey(ctx, CreateAPIKeyParams{UserID: owner.ID, Name: "ro", TokenHash: hash2, Scope: "read", ExpiresAt: &exp})
	if err != nil {
		t.Fatalf("create scoped key: %v", err)
	}
	if got, err := store.LookupAPIKey(ctx, hash2); err != nil || got.Scope != "read" || got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) || got.Expired(time.Now()) || !got.Expired(exp.Add(time.Second)) {
		t.Fatalf("scoped key roundtrip: %+v err=%v", got, err)
	}
	if _, err := store.CreateAPIKey(ctx, CreateAPIKeyParams{UserID: owner.ID, Name: "bad", TokenHash: hash2 + "x", Scope: "root"}); err == nil {
		t.Fatal("invalid scope must be rejected by the CHECK constraint")
	}
	if err := store.DeleteAPIKey(ctx, owner.ID, k2.ID); err != nil {
		t.Fatal(err)
	}
	if keys, _ := store.ListAPIKeys(ctx, other.ID); len(keys) != 0 {
		t.Fatalf("foreign list keys: %+v", keys)
	}
	if err := store.DeleteAPIKey(ctx, other.ID, k.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}
	if err := store.DeleteAPIKey(ctx, owner.ID, k.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.LookupAPIKey(ctx, auth.HashToken(raw)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lookup after delete: %v", err)
	}
}
