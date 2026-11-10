package worker

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"rssam/internal/reader"
	"rssam/internal/service"
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
			if _, ok := reader.RetryAt(s.err); !ok {
				s.store.feed.ParsingErrorCount++
			}
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
	nextCheckAt     time.Time
	setCalls        int
	lastSuccessNext time.Time
}

func (s *stubPollStore) SetFeedNextCheckAt(_ context.Context, _ int64, next time.Time) error {
	s.setCalls++
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

	processPollFeed(context.Background(), store.feed, store, ref, cfg, slog.Default(), nil)

	if ref.calls != 1 {
		t.Fatalf("refresh calls=%d", ref.calls)
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

	processPollFeed(context.Background(), store.feed, store, ref, cfg, slog.Default(), nil)

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

	processPollFeed(context.Background(), store.feed, store, ref, cfg, slog.Default(), nil)

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

type testBackoffErr struct{ at time.Time }

func (e testBackoffErr) Error() string      { return "max slot busy" }
func (e testBackoffErr) RetryAt() time.Time { return e.at }

func TestProcessPollFeed_BackoffSkipsWithoutError(t *testing.T) {
	feedID := int64(11)
	store := &stubPollStore{feed: storage.Feed{ID: feedID, IntervalMinutes: 15}}
	until := time.Now().UTC().Add(3 * time.Second)
	cfg := Config{InstanceID: "test"}
	ref := &stubFeedRefresher{err: testBackoffErr{at: until}, store: store, cfg: cfg}

	got, scope := processPollFeed(context.Background(), store.feed, store, ref, cfg, slog.Default(), nil)

	if store.feed.ParsingErrorCount != 0 {
		t.Fatalf("error count=%d, want 0", store.feed.ParsingErrorCount)
	}
	if scope != deferFeed || got.Before(until.Add(-time.Second)) {
		t.Fatalf("scope=%v until=%s, want ~%s", scope, got, until)
	}
	if store.setCalls != 0 {
		t.Fatalf("a rate-limited poll must not write the row, got next_check=%s", store.nextCheckAt)
	}
}

// When the refresher has already persisted the error backoff (next_check_at
// in the future) the worker must not write a second, slightly different
// value.
func TestProcessPollFeed_ErrorReusesPersistedNextCheck(t *testing.T) {
	feedID := int64(7)
	persisted := time.Now().UTC().Add(42 * time.Minute)
	store := &stubPollStore{feed: storage.Feed{ID: feedID, IntervalMinutes: 5}}
	cfg := Config{InstanceID: "test", MinPollInterval: time.Minute, MaxPollInterval: 24 * time.Hour}
	ref := &stubFeedRefresher{err: errors.New("fetch failed"), store: store, cfg: cfg}
	// Simulate service.FeedRefresher.recordFailure having set next_check_at.
	ref.store.feed.NextCheckAt = &persisted

	processPollFeed(context.Background(), store.feed, store, ref, cfg, slog.Default(), nil)

	if store.setCalls != 0 {
		t.Fatalf("SetFeedNextCheckAt must not be called again, got %s", store.nextCheckAt)
	}
}

type testBridgeBusyErr struct{ at time.Time }

func (e testBridgeBusyErr) Error() string         { return "slots busy" }
func (e testBridgeBusyErr) RetryAt() time.Time    { return e.at }
func (e testBridgeBusyErr) DeferBridgeWide() bool { return true }

func TestProcessPollFeed_BridgeWideDeferralHoldsTheBridge(t *testing.T) {
	feedID := int64(12)
	store := &stubPollStore{feed: storage.Feed{ID: feedID, FeedType: "max", IntervalMinutes: 15}}
	until := time.Now().UTC().Add(2 * time.Second)
	cfg := Config{InstanceID: "test"}
	ref := &stubFeedRefresher{err: testBridgeBusyErr{at: until}, store: store, cfg: cfg}

	got, scope := processPollFeed(context.Background(), store.feed, store, ref, cfg, slog.Default(), nil)

	if scope != deferBridge || !got.Equal(until) {
		t.Fatalf("scope=%v until=%s, want bridge %s", scope, got, until)
	}
	if store.setCalls != 0 || store.feed.ParsingErrorCount != 0 {
		t.Fatalf("a busy bridge must not touch the row: setCalls=%d errors=%d", store.setCalls, store.feed.ParsingErrorCount)
	}
}

func TestDuePausesEarliestResume(t *testing.T) {
	var p duePauses[string]
	now := time.Now()
	p.pause("max", now.Add(3*time.Second))
	p.pause("telegram", now.Add(time.Second))
	p.pause("vk_search", now.Add(-time.Second))
	ids, resume, ok := p.active(now)
	if !ok || len(ids) != 2 || !resume.Equal(now.Add(time.Second)) {
		t.Fatalf("ids=%v resume=%v ok=%v", ids, resume, ok)
	}
}

func TestProcessPollFeed_LongDeferralIsWritten(t *testing.T) {
	feedID := int64(13)
	store := &stubPollStore{feed: storage.Feed{ID: feedID, IntervalMinutes: 15}}
	until := time.Now().UTC().Add(3 * time.Hour)
	cfg := Config{InstanceID: "test"}
	ref := &stubFeedRefresher{err: testBackoffErr{at: until}, store: store, cfg: cfg}

	if _, scope := processPollFeed(context.Background(), store.feed, store, ref, cfg, slog.Default(), nil); scope != deferNone {
		t.Fatalf("scope=%v, want none", scope)
	}
	if store.setCalls != 1 || !store.nextCheckAt.Equal(until) {
		t.Fatalf("next_check_at must be written for a long wait: calls=%d at=%s", store.setCalls, store.nextCheckAt)
	}
}

// A failure the refresher already recorded comes back as service.PollFailed
// with the persisted row: the worker writes nothing.
func TestProcessPollFeed_PollFailedWritesNothing(t *testing.T) {
	feedID := int64(31)
	persisted := time.Now().UTC().Add(10 * time.Minute)
	store := &stubPollStore{feed: storage.Feed{ID: feedID, IntervalMinutes: 5}}
	cfg := Config{InstanceID: "test", MinPollInterval: time.Minute, MaxPollInterval: 24 * time.Hour}
	after := storage.Feed{ID: feedID, IntervalMinutes: 5, ParsingErrorCount: 1, NextCheckAt: &persisted}
	ref := &stubFeedRefresher{err: &service.PollFailed{Feed: after, Err: errors.New("fetch failed")}, cfg: cfg}

	processPollFeed(context.Background(), store.feed, store, ref, cfg, slog.Default(), nil)

	if store.setCalls != 0 {
		t.Fatalf("SetFeedNextCheckAt must not be called, got %s", store.nextCheckAt)
	}
}
