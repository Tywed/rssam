package httpserver

import (
	"context"
	"time"

	"rssam/internal/storage"
)

type noopEntryStore struct{}

func (noopEntryStore) CreateEntries(_ context.Context, _ int64, _ []storage.CreateEntryParams) (int, []storage.Entry, error) {
	return 0, nil, nil
}
func (noopEntryStore) GetEntry(_ context.Context, _, _ int64) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (noopEntryStore) GetFeedEntry(_ context.Context, _, _, _ int64) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (noopEntryStore) GetEntryByID(_ context.Context, _ int64) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (noopEntryStore) UpdateEntryContent(_ context.Context, _ int64, _ storage.UpdateEntryContentParams) (storage.Entry, error) {
	return storage.Entry{}, nil
}
func (noopEntryStore) UpdateEntry(_ context.Context, _, _, _ int64, _ storage.UpdateEntryParams) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (noopEntryStore) ListEntries(_ context.Context, _ int64, _ storage.ListEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, nil
}
func (noopEntryStore) ListFeedEntries(_ context.Context, _, _ int64, _ storage.ListEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, nil
}
func (noopEntryStore) SearchEntries(_ context.Context, _ int64, _ storage.SearchEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, nil
}
func (noopEntryStore) ListEnclosuresByEntryIDs(_ context.Context, _ int64, _ []int64) (map[int64][]storage.Enclosure, error) {
	return map[int64][]storage.Enclosure{}, nil
}
func (noopEntryStore) CountUnreadByFeed(_ context.Context, _ int64) (int, error) { return 0, nil }
func (noopEntryStore) CountUnreadByCategory(_ context.Context, _ int64) (int, error) {
	return 0, nil
}
func (noopEntryStore) CountUnreadGlobal(_ context.Context) (int, error) { return 0, nil }
func (noopEntryStore) UnreadCountsForUser(_ context.Context, _ int64) (map[int64]int, map[int64]int, error) {
	return map[int64]int{}, map[int64]int{}, nil
}
func (noopEntryStore) BulkUpdateEntries(_ context.Context, _ int64, _ []int64, _ storage.BulkEntryUpdate) (int, error) {
	return 0, nil
}
func (noopEntryStore) MarkAllFeedEntriesRead(_ context.Context, _, _ int64) (int, error) {
	return 0, nil
}
func (noopEntryStore) MarkAllCategoryEntriesRead(_ context.Context, _, _ int64) (int, error) {
	return 0, nil
}
func (noopEntryStore) MarkAllEntriesRead(_ context.Context, _ int64) (int, error) {
	return 0, nil
}

type noopFeedStore struct{}

func (noopFeedStore) CreateFeed(_ context.Context, _ int64, _ storage.CreateFeedParams) (storage.Feed, error) {
	return storage.Feed{}, nil
}
func (noopFeedStore) GetFeed(_ context.Context, _ int64, _ int64) (storage.Feed, error) {
	return storage.Feed{}, storage.ErrNotFound
}
func (noopFeedStore) GetFeedByID(_ context.Context, _ int64) (storage.Feed, error) {
	return storage.Feed{}, storage.ErrNotFound
}
func (noopFeedStore) ListFeeds(_ context.Context, _ int64, _, _ int) ([]storage.Feed, int, error) {
	return nil, 0, nil
}
func (noopFeedStore) ListFeedsByCategory(_ context.Context, _ int64, _ int64) ([]storage.Feed, error) {
	return nil, nil
}
func (noopFeedStore) ListFeedsByCategoryPaginated(_ context.Context, _ int64, _ int64, _, _ int) ([]storage.Feed, int, error) {
	return nil, 0, nil
}
func (noopFeedStore) ListFeedsByStatus(_ context.Context, _ int64, _ string, _, _ int) ([]storage.Feed, int, error) {
	return nil, 0, nil
}
func (noopFeedStore) SearchFeeds(_ context.Context, _ int64, _ storage.SearchFeedsFilter) ([]storage.Feed, error) {
	return nil, nil
}
func (noopFeedStore) ListFeedsByIDs(_ context.Context, _ int64, _ []int64) ([]storage.Feed, error) {
	return nil, nil
}
func (noopFeedStore) FeedCountsByCategory(_ context.Context, _ int64) (storage.FeedCategoryCounts, error) {
	return storage.FeedCategoryCounts{ByCategory: map[int64]int{}}, nil
}
func (noopFeedStore) CountFeedStatuses(_ context.Context, _ int64) (int, int, error) {
	return 0, 0, nil
}
func (noopFeedStore) ListAllFeeds(_ context.Context, _ int) ([]storage.Feed, error) {
	return nil, nil
}
func (noopFeedStore) UpdateFeed(_ context.Context, _ int64, _ storage.UpdateFeedParams) (storage.Feed, error) {
	return storage.Feed{}, nil
}
func (noopFeedStore) UpdateFeedRefreshMeta(_ context.Context, _ storage.UpdateFeedRefreshMetaParams) error {
	return nil
}
func (noopFeedStore) UpdateFeedIcon(_ context.Context, _, _ int64, _ string, _ []byte) error {
	return nil
}
func (noopFeedStore) SetFeedNextCheckAt(_ context.Context, _ int64, _ time.Time) error {
	return nil
}
func (noopFeedStore) RecordFeedPollFailure(_ context.Context, _ int64, _ string, _ int, _ time.Time) error {
	return nil
}
func (noopFeedStore) ResetFeedPollCircuit(_ context.Context, _ int64) error { return nil }
func (noopFeedStore) ResetErrorFeedPollCircuits(_ context.Context) (int64, error) {
	return 0, nil
}
func (noopFeedStore) SetFeedManualPaused(_ context.Context, _ int64, _ bool) error { return nil }
func (noopFeedStore) BulkUpdateFeedsByCategory(_ context.Context, _ int64, _ int64, _ storage.BulkFeedUpdate) ([]int64, int, error) {
	return nil, 0, nil
}
func (noopFeedStore) DeleteFeed(_ context.Context, _ int64, _ int64) error { return nil }
