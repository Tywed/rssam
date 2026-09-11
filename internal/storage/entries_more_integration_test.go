//go:build integration

package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestIntegration_EntryDedupAndCollapse(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "dedup")
	feed, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 4)
	hashOnly, hashEntries := newIntegrationFeedWithEntries(t, store, owner.ID, 2)
	if _, err := store.UpdateFeed(ctx, owner.ID, UpdateFeedParams{ID: hashOnly.ID, FeedURL: hashOnly.FeedURL, Title: hashOnly.Title, IntervalMinutes: 60, StoreHashOnly: true}); err != nil {
		t.Fatal(err)
	}

	n, err := store.RecordFeedEntryDedup(ctx, feed.ID, []FeedEntryDedupParams{{Hash: "seen-1", URL: "https://example.com/seen"}, {Hash: "seen-2"}})
	if err != nil || n != 2 {
		t.Fatalf("record dedup: n=%d err=%v", n, err)
	}
	if n, err = store.RecordFeedEntryDedup(ctx, feed.ID, []FeedEntryDedupParams{{Hash: "seen-1"}}); err != nil || n != 0 {
		t.Fatalf("idempotent dedup: n=%d err=%v", n, err)
	}
	if _, err := store.RecordFeedEntryDedup(ctx, feed.ID, []FeedEntryDedupParams{{Hash: " "}}); err == nil {
		t.Fatal("blank hash accepted")
	}
	if n, err := store.RecordFeedEntryDedup(ctx, feed.ID, nil); err != nil || n != 0 {
		t.Fatalf("empty batch: n=%d err=%v", n, err)
	}
	known, err := store.FilterKnownEntryHashes(ctx, feed.ID, []string{"seen-1", entries[0].Hash, "fresh"})
	if err != nil || len(known) != 2 {
		t.Fatalf("known hashes: %v err=%v (dedup table + entries)", known, err)
	}
	if _, ok := known["fresh"]; ok {
		t.Fatal("fresh hash reported as known")
	}
	if empty, err := store.FilterKnownEntryHashes(ctx, feed.ID, nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty hashes: %v err=%v", empty, err)
	}
	// The dedup table blocks re-insertion of a seen hash.
	inserted, _, err := store.CreateEntries(ctx, feed.ID, []CreateEntryParams{
		{Title: "dup", URL: "https://example.com/dup", Hash: "seen-1"},
		{Title: "new", URL: "https://example.com/new", Hash: "brand-new"},
	})
	if err != nil || inserted != 1 {
		t.Fatalf("create with dedup: inserted=%d err=%v", inserted, err)
	}

	// Strip after webhook: body cleared, status removed, hash recorded.
	if err := store.StripEntryPayloadAfterWebhook(ctx, entries[0].ID); err != nil {
		t.Fatal(err)
	}
	stripped, _ := store.GetEntryByID(ctx, entries[0].ID)
	if stripped.Title != "" || stripped.Content != "" || stripped.Status != EntryStatusRemoved {
		t.Fatalf("stripped entry: %+v", stripped)
	}
	known, _ = store.FilterKnownEntryHashes(ctx, feed.ID, []string{entries[0].Hash})
	if len(known) != 1 {
		t.Fatal("stripped entry hash not in dedup table")
	}
	if err := store.StripEntryPayloadAfterWebhook(ctx, 999999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("strip missing: err=%v", err)
	}

	// Collapse: starred and labelled entries survive, pending webhook deliveries too.
	if _, err := store.BulkUpdateEntries(ctx, owner.ID, []int64{entries[1].ID}, BulkEntryUpdate{Starred: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	lbl, _ := store.CreateLabel(ctx, CreateLabelParams{UserID: owner.ID, Caption: "keep"})
	if err := store.AssignEntryLabel(ctx, entries[2].ID, lbl.ID); err != nil {
		t.Fatal(err)
	}
	wh, _ := store.CreateWebhook(ctx, CreateWebhookParams{UserID: owner.ID, Name: "c", URL: "https://example.com/c", Headers: []byte(`{}`), Enabled: true})
	if err := store.EnqueueWebhookLogs(ctx, []int64{wh.ID}, entries[3].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CollapseEntriesToHashes(ctx, CollapseEntriesParams{}); err == nil {
		t.Fatal("collapse without user accepted")
	}
	n64, err := store.CollapseEntriesToHashes(ctx, CollapseEntriesParams{UserID: owner.ID, OnlyHashOnlyFeeds: true})
	if err != nil || n64 != 2 {
		t.Fatalf("collapse hash-only feeds: n=%d err=%v", n64, err)
	}
	known, _ = store.FilterKnownEntryHashes(ctx, hashOnly.ID, []string{hashEntries[0].Hash, hashEntries[1].Hash})
	if len(known) != 2 {
		t.Fatalf("collapsed hashes not recorded: %v", known)
	}
	if _, err := store.GetEntryByID(ctx, hashEntries[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("collapsed entry still there: err=%v", err)
	}
	n64, err = store.CollapseEntriesToHashes(ctx, CollapseEntriesParams{UserID: owner.ID, FeedID: &feed.ID})
	if err != nil || n64 != 2 {
		t.Fatalf("collapse feed: n=%d err=%v (stripped + brand-new; starred, labelled, pending-webhook stay)", n64, err)
	}
	n64, err = store.CollapseEntriesToHashes(ctx, CollapseEntriesParams{UserID: owner.ID, FeedID: &feed.ID, IncludeLabeled: true})
	if err != nil || n64 != 1 {
		t.Fatalf("collapse including labelled: n=%d err=%v", n64, err)
	}
	remaining, _, _ := store.ListFeedEntries(ctx, owner.ID, feed.ID, ListEntriesFilter{Limit: 10})
	if len(remaining) != 2 {
		t.Fatalf("remaining after collapse: %d (starred + webhook-pending)", len(remaining))
	}
	cat, _ := store.CreateCategory(ctx, owner.ID, "collapse", "")
	if n64, err := store.CollapseEntriesToHashes(ctx, CollapseEntriesParams{UserID: owner.ID, CategoryID: &cat.ID}); err != nil || n64 != 0 {
		t.Fatalf("collapse empty category: n=%d err=%v", n64, err)
	}
	if n64, err := store.CollapseEntriesToHashes(ctx, CollapseEntriesParams{UserID: owner.ID, CategoryID: ptr(int64(0))}); err != nil || n64 != 0 {
		t.Fatalf("collapse uncategorized: n=%d err=%v", n64, err)
	}
}

func TestIntegration_EnclosuresUnreadCountsAndMarkAll(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "encl")
	other := newIntegrationUser(t, store, "encl_other")
	cat, _ := store.CreateCategory(ctx, owner.ID, "E", "")
	feed := newFeedForUser(t, store, owner.ID, "encl", 60)
	if _, err := store.UpdateFeed(ctx, owner.ID, UpdateFeedParams{ID: feed.ID, FeedURL: feed.FeedURL, Title: feed.Title, IntervalMinutes: 60, CategoryID: &cat.ID}); err != nil {
		t.Fatal(err)
	}
	loose := newFeedForUser(t, store, owner.ID, "loose", 60)
	_, foreignEntries := newIntegrationFeedWithEntries(t, store, other.ID, 1)

	suffix := time.Now().UnixNano()
	_, entries, err := store.CreateEntries(ctx, feed.ID, []CreateEntryParams{
		{Title: "podcast", URL: fmt.Sprintf("https://example.com/p/%d", suffix), Hash: fmt.Sprintf("p-%d", suffix), Enclosures: []CreateEnclosureParams{
			{URL: "https://example.com/a.mp3", Size: 10, MIMEType: "audio/mpeg"},
			{URL: "   "},
		}},
		{Title: "plain", URL: fmt.Sprintf("https://example.com/q/%d", suffix), Hash: fmt.Sprintf("q-%d", suffix)},
	})
	if err != nil || len(entries) != 2 {
		t.Fatalf("create entries: %v err=%v", entries, err)
	}
	if err := store.CreateEnclosures(ctx, owner.ID, entries[1].ID, []CreateEnclosureParams{{URL: "https://example.com/b.jpg", MIMEType: "image/jpeg"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateEnclosures(ctx, owner.ID, entries[1].ID, []CreateEnclosureParams{{URL: " "}}); err != nil {
		t.Fatalf("all-blank enclosures must be a no-op: %v", err)
	}
	encs, err := store.ListEnclosuresByEntryIDs(ctx, owner.ID, []int64{entries[0].ID, entries[1].ID})
	if err != nil || len(encs[entries[0].ID]) != 1 || len(encs[entries[1].ID]) != 1 || encs[entries[0].ID][0].MIMEType != "audio/mpeg" || encs[entries[0].ID][0].Size != 10 {
		t.Fatalf("enclosures: %v err=%v", encs, err)
	}
	if foreign, _ := store.ListEnclosuresByEntryIDs(ctx, other.ID, []int64{entries[0].ID}); len(foreign) != 0 {
		t.Fatalf("foreign enclosures visible: %v", foreign)
	}
	if empty, err := store.ListEnclosuresByEntryIDs(ctx, owner.ID, nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty ids: %v err=%v", empty, err)
	}

	if _, _, err := store.CreateEntries(ctx, loose.ID, []CreateEntryParams{{Title: "l", URL: fmt.Sprintf("https://example.com/l/%d", suffix), Hash: fmt.Sprintf("l-%d", suffix)}}); err != nil {
		t.Fatal(err)
	}
	if n, err := store.CountUnreadByFeed(ctx, feed.ID); err != nil || n != 2 {
		t.Fatalf("unread by feed=%d err=%v", n, err)
	}
	if n, err := store.CountUnreadByCategory(ctx, cat.ID); err != nil || n != 2 {
		t.Fatalf("unread by category=%d err=%v", n, err)
	}
	if n, err := store.CountUnreadGlobalForUser(ctx, owner.ID); err != nil || n != 3 {
		t.Fatalf("unread for user=%d err=%v", n, err)
	}
	if global, err := store.CountUnreadGlobal(ctx); err != nil || global != 4 {
		t.Fatalf("unread global=%d err=%v (ours + foreign)", global, err)
	}

	if _, err := store.MarkAllCategoryEntriesRead(ctx, other.ID, cat.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign category mark-all: err=%v", err)
	}
	if n, err := store.MarkAllCategoryEntriesRead(ctx, owner.ID, cat.ID); err != nil || n != 2 {
		t.Fatalf("category mark-all n=%d err=%v", n, err)
	}
	if n, _ := store.CountUnreadByCategory(ctx, cat.ID); n != 0 {
		t.Fatalf("still unread in category: %d", n)
	}
	if n, err := store.MarkAllEntriesRead(ctx, owner.ID); err != nil || n != 1 {
		t.Fatalf("mark-all n=%d err=%v", n, err)
	}
	if n, _ := store.CountUnreadGlobalForUser(ctx, owner.ID); n != 0 {
		t.Fatalf("still unread: %d", n)
	}
	if e, _ := store.GetEntryByID(ctx, foreignEntries[0].ID); e.Status != EntryStatusUnread {
		t.Fatalf("mark-all leaked to another user: %+v", e)
	}

	upd, err := store.UpdateEntryContent(ctx, owner.ID, UpdateEntryContentParams{ID: entries[1].ID, Content: "<p>full</p>", OriginalContent: "orig", ContentFetched: true})
	if err != nil || upd.Content != "<p>full</p>" || upd.OriginalContent != "orig" || !upd.ContentFetched {
		t.Fatalf("update content: %+v err=%v", upd, err)
	}
	if _, err := store.UpdateEntryContent(ctx, other.ID, UpdateEntryContentParams{ID: entries[1].ID, Content: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign content update: err=%v", err)
	}
	found, _, err := store.SearchEntries(ctx, owner.ID, SearchEntriesFilter{Query: "full", FeedID: &feed.ID, Limit: 5})
	if err != nil || len(found) != 1 || found[0].ID != entries[1].ID {
		t.Fatalf("search vector refreshed on content update: %v err=%v", found, err)
	}

	errs, inactive, err := store.CountFeedStatuses(ctx, owner.ID)
	if err != nil || errs != 0 || inactive != 0 {
		t.Fatalf("feed statuses: errs=%d inactive=%d err=%v", errs, inactive, err)
	}
	if err := store.SetFeedManualPaused(ctx, loose.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordFeedPollFailure(ctx, RecordFeedPollFailureParams{ID: feed.ID, Error: "x", CheckedAt: time.Now(), NextCheckAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	errs, inactive, _ = store.CountFeedStatuses(ctx, owner.ID)
	if errs != 2 || inactive != 1 {
		t.Fatalf("feed statuses after: errs=%d inactive=%d", errs, inactive)
	}
	feeds, total, err := store.ListFeedsByStatus(ctx, owner.ID, "inactive", 0, -1)
	if err != nil || total != 1 || len(feeds) != 1 || feeds[0].ID != loose.ID {
		t.Fatalf("inactive feeds: %v total=%d err=%v", feeds, total, err)
	}
	feeds, total, _ = store.ListFeedsByStatus(ctx, owner.ID, "errors", 1, 0)
	if total != 2 || len(feeds) != 1 {
		t.Fatalf("error feeds: n=%d total=%d", len(feeds), total)
	}
}

func TestIntegration_ListEnabledFiltersAndAuditCount(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "enf")

	mk := func(name string, enabled bool, order int, rules int) Filter {
		t.Helper()
		var rs []CreateFilterRuleParams
		for i := 0; i < rules; i++ {
			rs = append(rs, CreateFilterRuleParams{Field: "title", Pattern: fmt.Sprintf("%s-%d", name, i), Op: "and", Priority: rules - i})
		}
		f, err := store.CreateFilter(ctx, CreateFilterParams{UserID: owner.ID, Name: name, Enabled: enabled, OrderID: order, FeedScope: FilterFeedScopeAll, Rules: rs})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	second := mk("second", true, 2, 2)
	mk("disabled", false, 0, 1)
	first := mk("first", true, 1, 0)

	filters, err := store.ListEnabledFilters(ctx, owner.ID, 0)
	if err != nil || len(filters) != 2 || filters[0].ID != first.ID || filters[1].ID != second.ID {
		t.Fatalf("enabled filters ordered by order_id: %v err=%v", filters, err)
	}
	if len(filters[0].Rules) != 0 || len(filters[1].Rules) != 2 || filters[1].Rules[0].Pattern != "second-1" {
		t.Fatalf("rules grouped per filter, ordered by priority: %+v", filters[1].Rules)
	}
	// The LIMIT applies to joined rows, so a tiny limit truncates rules, not filters silently.
	if limited, _ := store.ListEnabledFilters(ctx, owner.ID, 1); len(limited) != 1 || limited[0].ID != first.ID {
		t.Fatalf("limit 1: %v", limited)
	}

	before, err := store.CountAuditLog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAudit(ctx, AuditEvent{ActorID: owner.ID, ActorName: owner.Username, Action: "it.count", TargetType: "filter", TargetID: first.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAudit(ctx, AuditEvent{Action: " "}); err == nil {
		t.Fatal("audit without action accepted")
	}
	after, _ := store.CountAuditLog(ctx)
	if after != before+1 {
		t.Fatalf("audit count %d → %d", before, after)
	}
}
