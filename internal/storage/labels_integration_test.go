//go:build integration

package storage

import (
	"context"
	"errors"
	"testing"
)

// A hashed (removed) entry keeps its entry_labels row so dedup and counters
// survive, but it must not show up in label/feed lists or search, and label
// mark-read must be tenant-scoped like feed/category mark-read.
func TestIntegration_LabelEntriesHideRemovedAndMarkRead(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "lbl_owner")
	other := newIntegrationUser(t, store, "lbl_other")
	feed, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 4)

	label, err := store.CreateLabel(ctx, CreateLabelParams{UserID: owner.ID, Caption: "Росгвардия", BgColor: "#2980b9", FgColor: "#ffffff"})
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteLabel(ctx, owner.ID, label.ID) })
	for _, e := range entries[:3] {
		if err := store.AssignEntryLabel(ctx, e.ID, label.ID); err != nil {
			t.Fatalf("assign label: %v", err)
		}
	}
	if err := store.StripEntryPayloadAfterWebhook(ctx, entries[0].ID); err != nil {
		t.Fatalf("strip: %v", err)
	}
	if _, err := store.MarkEntriesRemoved(ctx, owner.ID, []int64{entries[3].ID}); err != nil {
		t.Fatalf("remove: %v", err)
	}

	list, total, err := store.ListEntries(ctx, owner.ID, ListEntriesFilter{LabelID: &label.ID, Limit: 10})
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("label list: total=%d len=%d err=%v", total, len(list), err)
	}
	for _, e := range list {
		if e.Title == "" || e.Status == EntryStatusRemoved {
			t.Fatalf("removed entry leaked into label list: %+v", e)
		}
	}
	if _, total, _ = store.ListFeedEntries(ctx, owner.ID, feed.ID, ListEntriesFilter{Limit: 10}); total != 2 {
		t.Fatalf("feed list total=%d, want 2", total)
	}
	removed := EntryStatusRemoved
	if _, total, _ = store.ListEntries(ctx, owner.ID, ListEntriesFilter{LabelID: &label.ID, Status: &removed, Limit: 10}); total != 1 {
		t.Fatalf("explicit status=removed total=%d, want 1", total)
	}
	for _, rank := range []bool{false, true} {
		found, total, err := store.SearchEntries(ctx, owner.ID, SearchEntriesFilter{Query: "entry", FeedID: &feed.ID, Rank: rank, Limit: 10})
		if err != nil || total != 2 || len(found) != 2 {
			t.Fatalf("search rank=%v: total=%d len=%d err=%v", rank, total, len(found), err)
		}
	}
	counts, err := store.EntryCountsByLabel(ctx, owner.ID)
	if err != nil || counts[label.ID] != 2 {
		t.Fatalf("EntryCountsByLabel=%v err=%v", counts, err)
	}

	if _, err := store.MarkAllLabelEntriesRead(ctx, other.ID, label.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign label mark-read: err=%v", err)
	}
	n, err := store.MarkAllLabelEntriesRead(ctx, owner.ID, label.ID)
	if err != nil || n != 2 {
		t.Fatalf("label mark-read: n=%d err=%v", n, err)
	}
	unread, err := store.UnreadCountsByLabel(ctx, owner.ID)
	if err != nil || unread[label.ID] != 0 {
		t.Fatalf("unread by label after mark-read=%v err=%v", unread, err)
	}
	e0, _ := store.GetEntryByID(ctx, entries[0].ID)
	if e0.Status != EntryStatusRemoved {
		t.Fatalf("mark-read must not resurrect the hashed entry: %q", e0.Status)
	}
	if n, _ = store.MarkAllLabelEntriesRead(ctx, owner.ID, label.ID); n != 0 {
		t.Fatalf("second mark-read n=%d, want 0", n)
	}
}
