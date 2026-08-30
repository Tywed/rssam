package worker

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"rssam/internal/storage"
)

type stubFeedRefresher struct {
	inserted int
	err      error
	calls    int
	store    *stubPollStore
	cfg      Config
}

func (s *stubFeedRefresher) RefreshLoadedFeed(ctx context.Context, feed storage.Feed) (int, error) {
	return s.RefreshFeed(ctx, feed.ID)
}

func (s *stubFeedRefresher) RefreshFeed(_ context.Context, feedID int64) (int, error) {
	s.calls++
	if feedID <= 0 {
		return 0, errors.New("bad feed id")
	}
	if s.err != nil {
		if s.store != nil {
			s.store.feed.ParsingErrorCount++
		}
		return 0, s.err
	}
	if s.store != nil {
		s.store.feed.ParsingErrorCount = 0
		s.store.feed.PollPaused = false
		s.store.feed.LastError = ""
		from := time.Now().UTC()
		next := storage.FeedNextCheckAt(from, s.store.feed.IntervalMinutes, s.cfg.MinPollInterval, s.cfg.MaxPollInterval)
		_ = s.store.SetFeedNextCheckAt(context.Background(), feedID, next)
		s.store.lastSuccessNext = next
	}
	return s.inserted, nil
}

type stubPollStore struct {
	feed            storage.Feed
	completeCalls   int
	rescheduleCall  bool
	nextCheckAt     time.Time
	lastSuccessNext time.Time
}

func (s *stubPollStore) CompleteJob(context.Context, int64, string) (bool, error) {
	s.completeCalls++
	return true, nil
}
func (s *stubPollStore) RescheduleJob(context.Context, int64, string, time.Time, string) error {
	s.rescheduleCall = true
	return nil
}
func (s *stubPollStore) GetFeedByID(context.Context, int64) (storage.Feed, error) {
	return s.feed, nil
}
func (s *stubPollStore) SetFeedNextCheckAt(_ context.Context, _ int64, next time.Time) error {
	s.nextCheckAt = next
	return nil
}

func TestProcessPollFeed_SuccessSetsNextCheck(t *testing.T) {
	feedID := int64(42)
	store := &stubPollStore{
		feed: storage.Feed{ID: feedID, IntervalMinutes: 30},
	}
	cfg := Config{
		InstanceID:      "test",
		MinPollInterval: time.Minute,
		MaxPollInterval: time.Hour,
	}
	ref := &stubFeedRefresher{inserted: 3, store: store, cfg: cfg}

	j := storage.Job{ID: 9, Type: "poll_feed", FeedID: &feedID, Attempts: 0}
	processPollFeedJob(context.Background(), j, store, ref, cfg, slog.Default())

	if ref.calls != 1 {
		t.Fatalf("refresh calls=%d", ref.calls)
	}
	if store.completeCalls != 1 {
		t.Fatalf("complete calls=%d", store.completeCalls)
	}
	if store.rescheduleCall {
		t.Fatal("expected no reschedule on success")
	}
	if store.nextCheckAt.IsZero() {
		t.Fatal("expected next_check_at to be set")
	}
}

func TestProcessPollFeed_ErrorDoublesInterval(t *testing.T) {
	feedID := int64(7)
	store := &stubPollStore{feed: storage.Feed{ID: feedID, IntervalMinutes: 5}}
	cfg := Config{
		InstanceID:      "test",
		MinPollInterval: time.Minute,
		MaxPollInterval: 24 * time.Hour,
	}
	ref := &stubFeedRefresher{err: errors.New("fetch failed"), store: store, cfg: cfg}

	processPollFeedJob(context.Background(), storage.Job{
		ID: 1, Type: "poll_feed", FeedID: &feedID, Attempts: 0,
	}, store, ref, cfg, slog.Default())

	if store.feed.ParsingErrorCount != 1 {
		t.Fatalf("error count=%d, want 1", store.feed.ParsingErrorCount)
	}
	wantDelay := 10 * time.Minute
	gotDelay := store.nextCheckAt.Sub(time.Now().UTC())
	if gotDelay < wantDelay-time.Second || gotDelay > wantDelay+time.Second {
		t.Fatalf("next_check delay=%s, want ~%s (5m × 2 after 1 error)", gotDelay, wantDelay)
	}
}

func TestProcessPollFeed_SuccessResetsIntervalAfterErrors(t *testing.T) {
	feedID := int64(42)
	store := &stubPollStore{
		feed: storage.Feed{
			ID: feedID, IntervalMinutes: 5,
			ParsingErrorCount: 3, PollPaused: false, LastError: "timeout",
		},
	}
	cfg := Config{
		InstanceID:      "test",
		MinPollInterval: time.Minute,
		MaxPollInterval: 24 * time.Hour,
	}
	ref := &stubFeedRefresher{inserted: 1, store: store, cfg: cfg}

	processPollFeedJob(context.Background(), storage.Job{
		ID: 9, Type: "poll_feed", FeedID: &feedID,
	}, store, ref, cfg, slog.Default())

	if store.feed.ParsingErrorCount != 0 {
		t.Fatalf("error count=%d, want 0 after success", store.feed.ParsingErrorCount)
	}
	if store.feed.LastError != "" {
		t.Fatalf("last_error=%q, want empty after success", store.feed.LastError)
	}
	wantDelay := 5 * time.Minute
	gotDelay := store.lastSuccessNext.Sub(time.Now().UTC())
	if gotDelay < wantDelay-time.Second || gotDelay > wantDelay+time.Second {
		t.Fatalf("success next_check delay=%s, want ~%s (user interval)", gotDelay, wantDelay)
	}
}

func TestRunnerPauseResume(t *testing.T) {
	var r Runner
	if r.Paused() {
		t.Fatal("new runner should not be paused")
	}
	r.Pause()
	if !r.Paused() {
		t.Fatal("expected paused")
	}
	r.Resume()
	if r.Paused() {
		t.Fatal("expected running")
	}
}
