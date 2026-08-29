package service

import (
	"context"
	"testing"
	"time"

	"rssam/internal/storage"
)

type memLabelStore struct {
	labels map[int64]storage.Label
	assign []struct{ entryID, labelID int64 }
}

func (m *memLabelStore) ListLabels(context.Context, int64, int, int) ([]storage.Label, int, error) {
	return nil, 0, nil
}
func (m *memLabelStore) CreateLabel(context.Context, storage.CreateLabelParams) (storage.Label, error) {
	return storage.Label{}, nil
}
func (m *memLabelStore) GetLabel(_ context.Context, userID, id int64) (storage.Label, error) {
	if l, ok := m.labels[id]; ok && l.UserID == userID {
		return l, nil
	}
	return storage.Label{}, storage.ErrNotFound
}
func (m *memLabelStore) UpdateLabel(context.Context, storage.UpdateLabelParams) (storage.Label, error) {
	return storage.Label{}, nil
}
func (m *memLabelStore) DeleteLabel(context.Context, int64, int64) error { return nil }
func (m *memLabelStore) AssignEntryLabel(_ context.Context, entryID, labelID int64) error {
	m.assign = append(m.assign, struct{ entryID, labelID int64 }{entryID, labelID})
	return nil
}
func (m *memLabelStore) EntryCountsByLabel(context.Context, int64) (map[int64]int, error) {
	return nil, nil
}
func (m *memLabelStore) UnreadCountsByLabel(context.Context, int64) (map[int64]int, error) {
	return nil, nil
}

type memEntryBulk struct {
	removed []int64
}

func (m *memEntryBulk) CreateEntries(context.Context, int64, []storage.CreateEntryParams) (int, []storage.Entry, error) {
	return 0, nil, nil
}
func (m *memEntryBulk) GetEntry(context.Context, int64, int64) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (m *memEntryBulk) GetFeedEntry(context.Context, int64, int64, int64) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (m *memEntryBulk) GetEntryByID(context.Context, int64) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (m *memEntryBulk) UpdateEntryContent(context.Context, int64, storage.UpdateEntryContentParams) (storage.Entry, error) {
	return storage.Entry{}, nil
}
func (m *memEntryBulk) UpdateEntry(context.Context, int64, int64, int64, storage.UpdateEntryParams) (storage.Entry, error) {
	return storage.Entry{}, nil
}
func (m *memEntryBulk) ListEntries(context.Context, int64, storage.ListEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, nil
}
func (m *memEntryBulk) ListFeedEntries(context.Context, int64, int64, storage.ListEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, nil
}
func (m *memEntryBulk) SearchEntries(context.Context, int64, storage.SearchEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, nil
}
func (m *memEntryBulk) ListEnclosuresByEntryIDs(context.Context, int64, []int64) (map[int64][]storage.Enclosure, error) {
	return nil, nil
}
func (m *memEntryBulk) CountUnreadByFeed(context.Context, int64) (int, error) { return 0, nil }
func (m *memEntryBulk) CountUnreadByCategory(context.Context, int64) (int, error) {
	return 0, nil
}
func (m *memEntryBulk) CountUnreadGlobal(context.Context) (int, error) { return 0, nil }
func (m *memEntryBulk) UnreadCountsForUser(context.Context, int64) (map[int64]int, map[int64]int, error) {
	return nil, nil, nil
}
func (m *memEntryBulk) BulkUpdateEntries(_ context.Context, _ int64, entryIDs []int64, update storage.BulkEntryUpdate) (int, error) {
	if update.Status != nil && *update.Status == storage.EntryStatusRemoved {
		m.removed = append(m.removed, entryIDs...)
	}
	return len(entryIDs), nil
}
func (m *memEntryBulk) MarkAllFeedEntriesRead(context.Context, int64, int64) (int, error) {
	return 0, nil
}
func (m *memEntryBulk) MarkAllCategoryEntriesRead(context.Context, int64, int64) (int, error) {
	return 0, nil
}
func (m *memEntryBulk) MarkAllEntriesRead(context.Context, int64) (int, error) { return 0, nil }

type memWebhookLogStore struct {
	enqueued []int64
}

func (m *memWebhookLogStore) EnqueueWebhookLogs(_ context.Context, webhookIDs []int64, _ int64) error {
	m.enqueued = append(m.enqueued, webhookIDs...)
	return nil
}
func (m *memWebhookLogStore) ClaimDueWebhookLogs(context.Context, int) ([]storage.WebhookLog, error) {
	return nil, nil
}
func (m *memWebhookLogStore) MarkWebhookLogSent(context.Context, int64, int, int, string) error {
	return nil
}
func (m *memWebhookLogStore) MarkWebhookLogFailed(context.Context, int64, *int, string, string, int, *time.Time, bool) error {
	return nil
}
func (m *memWebhookLogStore) ListWebhookLogs(context.Context, int64, int, int) ([]storage.WebhookLog, int, error) {
	return nil, 0, nil
}
func (m *memWebhookLogStore) RetryWebhookLogNow(context.Context, int64) error { return nil }

func TestApplyFilterActions(t *testing.T) {
	labels := &memLabelStore{labels: map[int64]storage.Label{7: {ID: 7, UserID: 1}}}
	entries := &memEntryBulk{}
	logs := &memWebhookLogStore{}
	r := &FeedRefresher{Labels: labels, Entries: entries, WebhookLogs: logs}
	entry := storage.Entry{ID: 100, Title: "t"}
	filters := []storage.Filter{{
		ID: 1,
		Actions: []storage.FilterAction{
			{ActionType: storage.FilterActionLabel, ActionParam: "7"},
			{ActionType: storage.FilterActionWebhook, ActionParam: "3"},
			{ActionType: storage.FilterActionDelete},
		},
	}}
	r.applyFilterActions(context.Background(), 1, entry, filters, map[int64]struct{}{1: {}})
	if len(labels.assign) != 1 || labels.assign[0].labelID != 7 {
		t.Fatalf("label not assigned: %+v", labels.assign)
	}
	if len(logs.enqueued) != 1 || logs.enqueued[0] != 3 {
		t.Fatalf("webhook not enqueued: %+v", logs.enqueued)
	}
	if len(entries.removed) != 1 || entries.removed[0] != 100 {
		t.Fatalf("entry not removed: %+v", entries.removed)
	}
}
