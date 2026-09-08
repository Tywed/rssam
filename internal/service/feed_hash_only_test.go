package service

import (
	"context"
	"testing"

	"rssam/internal/filter"
	"rssam/internal/storage"
)

type memDedupStore struct {
	recorded []storage.FeedEntryDedupParams
	known    map[string]struct{}
}

func (m *memDedupStore) FilterKnownEntryHashes(_ context.Context, _ int64, hashes []string) (map[string]struct{}, error) {
	if m.known == nil {
		m.known = map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(hashes))
	for _, h := range hashes {
		if _, ok := m.known[h]; ok {
			out[h] = struct{}{}
		}
	}
	return out, nil
}

func (m *memDedupStore) RecordFeedEntryDedup(_ context.Context, _ int64, items []storage.FeedEntryDedupParams) (int, error) {
	m.recorded = append(m.recorded, items...)
	for _, it := range items {
		if m.known == nil {
			m.known = map[string]struct{}{}
		}
		m.known[it.Hash] = struct{}{}
	}
	return len(items), nil
}

func (m *memDedupStore) StripEntryPayloadAfterWebhook(context.Context, int64) error { return nil }
func (m *memDedupStore) CollapseEntriesToHashes(context.Context, storage.CollapseEntriesParams) (int64, error) {
	return 0, nil
}

type memEntryCreate struct {
	created []storage.CreateEntryParams
}

func (m *memEntryCreate) CreateEntries(_ context.Context, _ int64, entries []storage.CreateEntryParams) (int, []storage.Entry, error) {
	m.created = append(m.created, entries...)
	out := make([]storage.Entry, 0, len(entries))
	for i, e := range entries {
		out = append(out, storage.Entry{
			ID:      int64(i + 1),
			FeedID:  1,
			Title:   e.Title,
			Content: e.Content,
			Hash:    e.Hash,
		})
	}
	return len(entries), out, nil
}

func (m *memEntryCreate) GetEntry(context.Context, int64, int64) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (m *memEntryCreate) GetFeedEntry(context.Context, int64, int64, int64) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (m *memEntryCreate) GetEntryByID(context.Context, int64) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (m *memEntryCreate) UpdateEntryContent(context.Context, int64, storage.UpdateEntryContentParams) (storage.Entry, error) {
	return storage.Entry{}, nil
}
func (m *memEntryCreate) UpdateEntry(context.Context, int64, int64, int64, storage.UpdateEntryParams) (storage.Entry, error) {
	return storage.Entry{}, nil
}
func (m *memEntryCreate) ListEntries(context.Context, int64, storage.ListEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, nil
}
func (m *memEntryCreate) ListFeedEntries(context.Context, int64, int64, storage.ListEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, nil
}
func (m *memEntryCreate) SearchEntries(context.Context, int64, storage.SearchEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, nil
}
func (m *memEntryCreate) ListEnclosuresByEntryIDs(context.Context, int64, []int64) (map[int64][]storage.Enclosure, error) {
	return nil, nil
}
func (m *memEntryCreate) CountUnreadByFeed(context.Context, int64) (int, error) { return 0, nil }
func (m *memEntryCreate) CountUnreadByCategory(context.Context, int64) (int, error) {
	return 0, nil
}
func (m *memEntryCreate) CountUnreadGlobal(context.Context) (int, error)               { return 0, nil }
func (m *memEntryCreate) CountUnreadGlobalForUser(context.Context, int64) (int, error) { return 0, nil }
func (m *memEntryCreate) UnreadCountsForUser(context.Context, int64) (map[int64]int, map[int64]int, error) {
	return nil, nil, nil
}
func (m *memEntryCreate) BulkUpdateEntries(context.Context, int64, []int64, storage.BulkEntryUpdate) (int, error) {
	return 0, nil
}
func (m *memEntryCreate) MarkAllFeedEntriesRead(context.Context, int64, int64) (int, error) {
	return 0, nil
}
func (m *memEntryCreate) MarkAllCategoryEntriesRead(context.Context, int64, int64) (int, error) {
	return 0, nil
}
func (m *memEntryCreate) MarkAllEntriesRead(context.Context, int64) (int, error) { return 0, nil }
func (m *memEntryCreate) MarkEntriesRemoved(context.Context, int64, []int64) (int, error) {
	return 0, nil
}

type memFilterList struct {
	filters []storage.Filter
}

func (m *memFilterList) ListFilters(context.Context, int64, int, int) ([]storage.Filter, int, error) {
	return nil, 0, nil
}
func (m *memFilterList) CreateFilter(context.Context, storage.CreateFilterParams) (storage.Filter, error) {
	return storage.Filter{}, nil
}
func (m *memFilterList) GetFilter(context.Context, int64, int64) (storage.Filter, error) {
	return storage.Filter{}, storage.ErrNotFound
}
func (m *memFilterList) UpdateFilter(context.Context, storage.UpdateFilterParams) (storage.Filter, error) {
	return storage.Filter{}, nil
}
func (m *memFilterList) DeleteFilter(context.Context, int64, int64) error { return nil }
func (m *memFilterList) ListEnabledFilters(context.Context, int64, int) ([]storage.Filter, error) {
	return m.filters, nil
}

func TestFeedRefresherPerFeedHashOnly(t *testing.T) {
	dedup := &memDedupStore{}
	entries := &memEntryCreate{}
	filters := &memFilterList{filters: []storage.Filter{{
		ID: 1, UserID: 1, Enabled: true, FeedScope: storage.FilterFeedScopeAll,
		Rules: []storage.FilterRule{{Field: "title", Pattern: "match", Op: "regex"}},
	}}}
	r := &FeedRefresher{
		Dedup:   dedup,
		Entries: entries,
		Filters: filters,
		Engine:  filter.New(filter.Config{}),
	}
	feed := storage.Feed{ID: 1, UserID: 1, StoreHashOnly: true}

	inserted, created, err := r.processEntriesDedupOnly(context.Background(), feed, []storage.CreateEntryParams{
		{Title: "skip me", Hash: "h1", URL: "https://example.com/1"},
		{Title: "match item", Hash: "h2", URL: "https://example.com/2", Content: "body"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 1 || len(created) != 1 {
		t.Fatalf("inserted=%d created=%d, want 1/1", inserted, len(created))
	}
	if len(dedup.recorded) != 1 || dedup.recorded[0].Hash != "h1" {
		t.Fatalf("dedup recorded=%+v", dedup.recorded)
	}
	if len(entries.created) != 1 || entries.created[0].Hash != "h2" {
		t.Fatalf("full entry created=%+v", entries.created)
	}
}

func TestFeedUsesHashOnlyStorage(t *testing.T) {
	r := &FeedRefresher{StoreEntriesMode: "full"}
	if r.feedUsesHashOnlyStorage(storage.Feed{StoreHashOnly: true}) {
		// ok per-feed
	} else {
		t.Fatal("per-feed flag should enable hash-only")
	}
	if r.feedUsesHashOnlyStorage(storage.Feed{}) {
		t.Fatal("full mode without per-feed flag should be false")
	}
	r.StoreEntriesMode = "dedup_only"
	if !r.feedUsesHashOnlyStorage(storage.Feed{}) {
		t.Fatal("global dedup_only should enable hash-only")
	}
}
