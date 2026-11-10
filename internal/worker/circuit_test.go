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
	feed := store.feed
	feed.ID = 7
	processPollFeed(context.Background(), feed, store, noopRefresher{}, Config{InstanceID: "test"}, slog.Default(), nil)
}
