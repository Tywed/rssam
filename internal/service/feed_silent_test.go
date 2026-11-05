package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"rssam/internal/reader"
	"rssam/internal/storage"
)

// countingSubscribers records how often the refresher asked for the feed's
// subscriptions.
type countingSubscribers struct {
	feedSubscribers
	calls int
}

func (m *countingSubscribers) ListFeedSubscribers(ctx context.Context, feedID int64) ([]storage.Subscription, error) {
	m.calls++
	return m.feedSubscribers.ListFeedSubscribers(ctx, feedID)
}

func TestRefreshLoadedFeed_SilentFeedIsPolledAtSilentInterval(t *testing.T) {
	old := time.Now().UTC().Add(-20 * 24 * time.Hour)
	feed := storage.Feed{ID: 21, OwnerID: 1, FeedURL: "https://example.com/s.xml", IntervalMinutes: 5, CreatedAt: old, LastEntryAt: &old, StoreMode: storage.StoreModeFiltered}
	h := &stubHandler{res: reader.FetchResponse{NotModified: true}}
	r, fs, _, _ := newStatusRefresher(h, feed)
	r.SilentAfter = 14 * 24 * time.Hour
	r.SilentInterval = 30 * time.Minute

	if _, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
	if d := time.Until(fs.next); d < 29*time.Minute || d > 31*time.Minute {
		t.Fatalf("silent feed next check in %s, want ~30m", d)
	}

	// Not silent long enough: own interval.
	recent := time.Now().UTC().Add(-13 * 24 * time.Hour)
	feed.LastEntryAt = &recent
	if _, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
	if d := time.Until(fs.next); d < 4*time.Minute || d > 6*time.Minute {
		t.Fatalf("active feed next check in %s, want ~5m", d)
	}

	// A new feed counts from creation.
	feed.LastEntryAt = nil
	if _, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
	if d := time.Until(fs.next); d < 29*time.Minute {
		t.Fatalf("never-produced feed next check in %s, want ~30m", d)
	}

	// Off when either knob is zero.
	r.SilentInterval = 0
	if _, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
	if d := time.Until(fs.next); d > 6*time.Minute {
		t.Fatalf("FEED_SILENT_INTERVAL=0 must keep the own interval, got %s", d)
	}
}

func TestRefreshLoadedFeed_SilentFeedWakesUpOnFirstItem(t *testing.T) {
	old := time.Now().UTC().Add(-20 * 24 * time.Hour)
	feed := storage.Feed{ID: 22, OwnerID: 1, FeedURL: "https://example.com/w.xml", IntervalMinutes: 5, CreatedAt: old, LastEntryAt: &old}
	h := &stubHandler{res: reader.FetchResponse{Entries: []storage.CreateEntryParams{
		{Title: "a", URL: "https://example.com/a", Hash: "a"},
	}}}
	r, fs, _, _ := newStatusRefresher(h, feed)
	r.SilentAfter = 14 * 24 * time.Hour
	r.SilentInterval = 30 * time.Minute

	inserted, err := r.RefreshLoadedFeed(context.Background(), feed)
	if err != nil || inserted != 1 {
		t.Fatalf("inserted=%d err=%v", inserted, err)
	}
	if d := time.Until(fs.next); d < 4*time.Minute || d > 6*time.Minute {
		t.Fatalf("feed with a new item next check in %s, want its own ~5m", d)
	}
}

func TestRefreshLoadedFeed_QuietPollDoesNotLoadSubscribers(t *testing.T) {
	feed := storage.Feed{ID: 23, OwnerID: 1, FeedURL: "https://example.com/q.xml", IntervalMinutes: 5, StoreMode: storage.StoreModeFiltered}
	h := &stubHandler{res: reader.FetchResponse{NotModified: true}}
	r, _, _, _ := newStatusRefresher(h, feed)
	subs := &countingSubscribers{feedSubscribers: feedSubscribers{feed: feed}}
	r.Subscribers = subs

	if _, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
	if subs.calls != 0 {
		t.Fatalf("quiet poll asked for subscribers %d times", subs.calls)
	}

	// A poll that stores entries runs the subscribers' filters: loaded once.
	full := feed
	full.StoreMode = storage.StoreModeFull
	h.res = reader.FetchResponse{Entries: []storage.CreateEntryParams{{Title: "a", URL: "https://example.com/a", Hash: "a"}}}
	if _, err := r.RefreshLoadedFeed(context.Background(), full); err != nil {
		t.Fatal(err)
	}
	if subs.calls != 1 {
		t.Fatalf("poll with a new entry asked for subscribers %d times, want 1", subs.calls)
	}
}

func TestRefreshLoadedFeed_FailureReturnsPollFailedWithPersistedState(t *testing.T) {
	feed := storage.Feed{ID: 24, OwnerID: 1, FeedURL: "https://example.com/f.xml", IntervalMinutes: 5, ParsingErrorCount: 1}
	h := &stubHandler{err: errors.New("boom")}
	r, _, _, _ := newStatusRefresher(h, feed)

	_, err := r.RefreshLoadedFeed(context.Background(), feed)
	var failed *PollFailed
	if !errors.As(err, &failed) || !errors.Is(err, ErrFetchFeed) {
		t.Fatalf("err=%v, want PollFailed wrapping ErrFetchFeed", err)
	}
	if failed.Feed.ParsingErrorCount != 2 || failed.Feed.NextCheckAt == nil {
		t.Fatalf("persisted state = %+v", failed.Feed)
	}
}
