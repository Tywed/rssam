package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"rssam/internal/reader"
	"rssam/internal/storage"
)

// statusFeedStore embeds the interface so only the methods refreshLoaded
// touches need to exist; anything else panics loudly.
type statusFeedStore struct {
	storage.FeedStore
	feed      storage.Feed
	failures  int
	threshold int
	meta      storage.UpdateFeedRefreshMetaParams
	next      time.Time
	circuit   int
}

func (s *statusFeedStore) GetFeedByID(_ context.Context, _ int64) (storage.Feed, error) {
	return s.feed, nil
}
func (s *statusFeedStore) RecordFeedPollFailure(_ context.Context, p storage.RecordFeedPollFailureParams) error {
	s.failures++
	s.threshold = p.Threshold
	s.feed.ParsingErrorCount++
	s.feed.PollPaused = s.feed.ParsingErrorCount >= p.Threshold
	s.feed.LastError = p.Error
	s.feed.LastCheckedAt = &p.CheckedAt
	s.next = p.NextCheckAt
	s.feed.NextCheckAt = &p.NextCheckAt
	return nil
}
func (s *statusFeedStore) UpdateFeedRefreshMeta(_ context.Context, p storage.UpdateFeedRefreshMetaParams) error {
	s.meta = p
	if p.LastError == "" {
		s.feed.ParsingErrorCount = 0
		s.feed.PollPaused = false
	}
	s.feed.LastError = p.LastError
	if p.NextCheckAt != nil {
		s.next = *p.NextCheckAt
		s.feed.NextCheckAt = p.NextCheckAt
	}
	return nil
}
func (s *statusFeedStore) SetFeedNextCheckAt(_ context.Context, _ int64, next time.Time) error {
	s.next = next
	s.feed.NextCheckAt = &next
	return nil
}
func (s *statusFeedStore) SetFeedManualPaused(_ context.Context, _ int64, paused bool) error {
	s.feed.ManualPaused = paused
	return nil
}
func (s *statusFeedStore) ResetFeedPollCircuit(_ context.Context, _ int64) error {
	s.circuit++
	s.feed.ParsingErrorCount = 0
	s.feed.PollPaused = false
	return nil
}

type statusPollLog struct {
	rows []storage.RecordFeedPollParams
}

func (l *statusPollLog) RecordFeedPoll(_ context.Context, p storage.RecordFeedPollParams) error {
	l.rows = append(l.rows, p)
	return nil
}
func (l *statusPollLog) ListFeedPollLog(context.Context, int64, int) ([]storage.FeedPollLogEntry, error) {
	return nil, nil
}

type statusPublisher struct {
	feeds []storage.Feed
	errs  []error
}

func (p *statusPublisher) PublishNewEntries(context.Context, storage.Feed, []storage.Entry) {}
func (p *statusPublisher) PublishFeedStatusChanged(feed storage.Feed, err error) {
	p.feeds = append(p.feeds, feed)
	p.errs = append(p.errs, err)
}

type stubHandler struct {
	res reader.FetchResponse
	err error
}

func (h *stubHandler) Name() string                 { return "rss" }
func (h *stubHandler) DetectFeedType(string) string { return "rss" }
func (h *stubHandler) Fetch(context.Context, reader.FetchRequest) (reader.FetchResponse, error) {
	return h.res, h.err
}

type retryAfterErr struct{ at time.Time }

func (e retryAfterErr) Error() string      { return "rate limited" }
func (e retryAfterErr) RetryAt() time.Time { return e.at }

func newStatusRefresher(h reader.Handler, feed storage.Feed) (*FeedRefresher, *statusFeedStore, *statusPollLog, *statusPublisher) {
	fs := &statusFeedStore{feed: feed}
	pl := &statusPollLog{}
	pub := &statusPublisher{}
	r := &FeedRefresher{
		Feeds:                   fs,
		Entries:                 &memEntryCreate{},
		Dedup:                   &memDedupStore{},
		Registry:                reader.NewHandlerRegistry(h),
		PollLog:                 pl,
		Realtime:                pub,
		CircuitBreakerThreshold: 3,
		MinPollInterval:         time.Minute,
		MaxPollInterval:         24 * time.Hour,
	}
	return r, fs, pl, pub
}

func TestRefreshLoadedFeed_FailurePublishesPersistedStateAndLogsPoll(t *testing.T) {
	feed := storage.Feed{ID: 7, UserID: 1, FeedURL: "https://example.com/f.xml", IntervalMinutes: 10, ParsingErrorCount: 2}
	r, fs, pl, pub := newStatusRefresher(&stubHandler{err: errors.New("boom 503")}, feed)

	_, err := r.RefreshLoadedFeed(context.Background(), feed)
	if !errors.Is(err, ErrFetchFeed) {
		t.Fatalf("err=%v, want ErrFetchFeed", err)
	}
	if fs.failures != 1 || fs.threshold != 3 {
		t.Fatalf("failures=%d threshold=%d", fs.failures, fs.threshold)
	}
	// Third consecutive error with threshold 3 → circuit opens; the event must
	// say so, not report the stale pre-poll copy or a hardcoded count of 1.
	if len(pub.feeds) != 1 {
		t.Fatalf("published %d events, want 1", len(pub.feeds))
	}
	got := pub.feeds[0]
	if got.ParsingErrorCount != 3 || !got.PollPaused || got.LastError == "" || got.NextCheckAt == nil || got.LastCheckedAt == nil {
		t.Fatalf("published feed state = %+v", got)
	}
	if pub.errs[0] == nil {
		t.Fatal("published err must be non-nil on failure")
	}
	// Error backoff: 10m × 2^3 = 80m from now.
	wantDelay := 80 * time.Minute
	if d := time.Until(*got.NextCheckAt); d < wantDelay-5*time.Second || d > wantDelay+5*time.Second {
		t.Fatalf("next_check_at delay=%s, want ~%s", d, wantDelay)
	}
	if !fs.next.Equal(*got.NextCheckAt) {
		t.Fatal("published next_check_at must equal the persisted one")
	}

	if len(pl.rows) != 1 {
		t.Fatalf("poll log rows=%d, want 1", len(pl.rows))
	}
	row := pl.rows[0]
	if row.OK || row.FeedID != 7 || row.Inserted != 0 || row.Error == "" || row.Duration < 0 {
		t.Fatalf("poll log row = %+v", row)
	}
}

func TestRefreshLoadedFeed_SuccessPublishesClearedStateAndLogsPoll(t *testing.T) {
	feed := storage.Feed{ID: 8, UserID: 1, FeedURL: "https://example.com/f.xml", IntervalMinutes: 15, ParsingErrorCount: 2, LastError: "old"}
	h := &stubHandler{res: reader.FetchResponse{Entries: []storage.CreateEntryParams{
		{Title: "a", URL: "https://example.com/a", Hash: "a"},
		{Title: "b", URL: "https://example.com/b", Hash: "b"},
	}}}
	r, fs, pl, pub := newStatusRefresher(h, feed)

	inserted, err := r.RefreshLoadedFeed(context.Background(), feed)
	if err != nil || inserted != 2 {
		t.Fatalf("inserted=%d err=%v", inserted, err)
	}
	if fs.meta.LastError != "" {
		t.Fatalf("meta.LastError=%q, want empty", fs.meta.LastError)
	}
	if len(pub.feeds) != 1 || pub.errs[0] != nil {
		t.Fatalf("events=%d errs=%v", len(pub.feeds), pub.errs)
	}
	got := pub.feeds[0]
	if got.ParsingErrorCount != 0 || got.PollPaused || got.LastError != "" || got.NextCheckAt == nil {
		t.Fatalf("published feed state = %+v", got)
	}
	wantDelay := 15 * time.Minute
	if d := time.Until(*got.NextCheckAt); d < wantDelay-5*time.Second || d > wantDelay+5*time.Second {
		t.Fatalf("next_check_at delay=%s, want ~%s", d, wantDelay)
	}
	if len(pl.rows) != 1 || !pl.rows[0].OK || pl.rows[0].Inserted != 2 || pl.rows[0].Error != "" {
		t.Fatalf("poll log rows = %+v", pl.rows)
	}
}

func TestRefreshLoadedFeed_RetryAfterIsLoggedButNotAnError(t *testing.T) {
	feed := storage.Feed{ID: 9, UserID: 1, FeedURL: "https://example.com/f.xml", IntervalMinutes: 5}
	r, fs, pl, pub := newStatusRefresher(&stubHandler{err: retryAfterErr{at: time.Now().Add(time.Hour)}}, feed)

	_, err := r.RefreshLoadedFeed(context.Background(), feed)
	if _, ok := reader.RetryAt(err); !ok {
		t.Fatalf("expected retry-after error, got %v", err)
	}
	if fs.failures != 0 || len(pub.feeds) != 0 {
		t.Fatalf("rate limiting must not count as a feed error: failures=%d events=%d", fs.failures, len(pub.feeds))
	}
	if len(pl.rows) != 1 || pl.rows[0].OK || pl.rows[0].Error == "" {
		t.Fatalf("poll log rows = %+v", pl.rows)
	}
}

func TestRefreshLoadedFeed_NoPollLogConfigured(t *testing.T) {
	feed := storage.Feed{ID: 10, UserID: 1, FeedURL: "https://example.com/f.xml", IntervalMinutes: 5}
	r, _, _, _ := newStatusRefresher(&stubHandler{}, feed)
	r.PollLog = nil
	r.Realtime = nil
	if _, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshLoadedFeed_GoneFeedIsPausedForGood(t *testing.T) {
	feed := storage.Feed{ID: 10, UserID: 1, FeedURL: "https://example.com/f.xml", IntervalMinutes: 10}
	r, fs, pl, pub := newStatusRefresher(&stubHandler{err: reader.ErrFeedGone}, feed)

	_, err := r.RefreshLoadedFeed(context.Background(), feed)
	if !errors.Is(err, ErrFetchFeed) {
		t.Fatalf("err=%v", err)
	}
	if !fs.feed.ManualPaused || fs.failures != 1 {
		t.Fatalf("feed must be manually paused after 410: paused=%v failures=%d", fs.feed.ManualPaused, fs.failures)
	}
	if len(pub.feeds) != 1 || !pub.feeds[0].ManualPaused || pub.feeds[0].LastError == "" {
		t.Fatalf("published %+v", pub.feeds)
	}
	if len(pl.rows) != 1 || pl.rows[0].OK {
		t.Fatalf("poll log %+v", pl.rows)
	}
}

func TestRefreshLoadedFeed_SourceFreshnessPostponesNextCheck(t *testing.T) {
	feed := storage.Feed{ID: 11, UserID: 1, FeedURL: "https://example.com/f.xml", IntervalMinutes: 15}
	h := &stubHandler{res: reader.FetchResponse{MinNextCheck: time.Now().Add(2 * time.Hour)}}
	r, fs, _, _ := newStatusRefresher(h, feed)
	if _, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
	if d := time.Until(fs.next); d < 2*time.Hour-5*time.Second || d > 2*time.Hour+5*time.Second {
		t.Fatalf("next_check_at delay=%s, want ~2h (source max-age)", d)
	}

	// A hint shorter than the feed interval does not bring the poll forward.
	h.res.MinNextCheck = time.Now().Add(time.Minute)
	if _, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
	if d := time.Until(fs.next); d < 15*time.Minute-5*time.Second {
		t.Fatalf("next_check_at delay=%s, want ~15m", d)
	}

	// And never beyond MaxPollInterval.
	r.MaxPollInterval = time.Hour
	h.res.MinNextCheck = time.Now().Add(6 * time.Hour)
	if _, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
	if d := time.Until(fs.next); d > time.Hour+5*time.Second {
		t.Fatalf("next_check_at delay=%s, want ≤1h", d)
	}
}
