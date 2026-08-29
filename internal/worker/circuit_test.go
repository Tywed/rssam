package worker

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"rssam/internal/service"
	"rssam/internal/storage"
)

type circuitFeedStore struct {
	feed storage.Feed
}

func (c *circuitFeedStore) CompleteJob(_ context.Context, _ int64, _ string) (bool, error) {
	return true, nil
}
func (c *circuitFeedStore) RescheduleJob(_ context.Context, _ int64, _ string, _ time.Time, _ string) error {
	return nil
}
func (c *circuitFeedStore) GetFeedByID(_ context.Context, id int64) (storage.Feed, error) {
	f := c.feed
	f.ID = id
	return f, nil
}
func (c *circuitFeedStore) SetFeedNextCheckAt(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

type noopRefresher struct{}

func (noopRefresher) RefreshFeed(_ context.Context, _ int64) (int, error) {
	return 0, service.ErrFeedCircuitOpen
}

func (noopRefresher) RefreshLoadedFeed(_ context.Context, _ storage.Feed) (int, error) {
	return 0, service.ErrFeedCircuitOpen
}

func TestProcessPollFeed_SkipsCircuitOpen(t *testing.T) {
	store := &circuitFeedStore{
		feed: storage.Feed{PollPaused: true, ParsingErrorCount: 10},
	}
	feedID := int64(7)
	j := storage.Job{ID: 1, Type: "poll_feed", FeedID: &feedID}
	processPollFeedJob(context.Background(), j, store, noopRefresher{}, Config{InstanceID: "test"}, slog.Default())
}
