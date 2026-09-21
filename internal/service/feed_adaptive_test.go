package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"rssam/internal/reader"
	"rssam/internal/storage"
)

type memActivity struct {
	items int
	err   error
	calls int
	since time.Time
}

func (m *memActivity) CountFeedItemsSince(_ context.Context, _ int64, since time.Time) (int, error) {
	m.calls++
	m.since = since
	return m.items, m.err
}

func nextDelay(t *testing.T, r *FeedRefresher, fs *statusFeedStore, feed storage.Feed) time.Duration {
	t.Helper()
	before := time.Now()
	if _, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
	if fs.meta.NextCheckAt == nil {
		t.Fatal("next_check_at not written")
	}
	return fs.meta.NextCheckAt.Sub(before).Round(time.Minute)
}

func TestRefreshLoadedFeed_AdaptiveInterval(t *testing.T) {
	h := &stubHandler{res: reader.FetchResponse{}}
	base := storage.Feed{ID: 9, UserID: 1, FeedURL: "https://example.com/f.xml", IntervalMinutes: 15}

	t.Run("off by default", func(t *testing.T) {
		r, fs, _, _ := newStatusRefresher(h, base)
		act := &memActivity{items: 0}
		r.Activity, r.AdaptiveMaxInterval = act, 6*time.Hour
		if d := nextDelay(t, r, fs, base); d != 15*time.Minute {
			t.Fatalf("delay=%s want 15m", d)
		}
		if act.calls != 0 {
			t.Fatal("activity must not be queried for non-adaptive feeds")
		}
	})

	feed := base
	feed.AdaptiveInterval = true

	t.Run("silent week sits at ceiling", func(t *testing.T) {
		r, fs, _, _ := newStatusRefresher(h, feed)
		act := &memActivity{items: 0}
		r.Activity, r.AdaptiveMaxInterval = act, 6*time.Hour
		if d := nextDelay(t, r, fs, feed); d != 6*time.Hour {
			t.Fatalf("delay=%s want 6h", d)
		}
		if act.calls != 1 || time.Since(act.since) < storage.AdaptivePollWindow-time.Minute {
			t.Fatalf("activity calls=%d since=%s", act.calls, act.since)
		}
	})

	t.Run("three silent weeks reach MAX_POLL_INTERVAL", func(t *testing.T) {
		silent := feed
		last := time.Now().Add(-21 * 24 * time.Hour)
		silent.LastEntryAt = &last
		r, fs, _, _ := newStatusRefresher(h, silent)
		r.Activity, r.AdaptiveMaxInterval = &memActivity{items: 0}, 6*time.Hour
		if d := nextDelay(t, r, fs, silent); d != 24*time.Hour {
			t.Fatalf("delay=%s want 24h", d)
		}
		r.MaxPollInterval = 12 * time.Hour
		if d := nextDelay(t, r, fs, silent); d != 12*time.Hour {
			t.Fatalf("delay=%s want 12h", d)
		}
	})

	t.Run("silence counts from creation when nothing ever arrived", func(t *testing.T) {
		young := feed
		young.CreatedAt = time.Now().Add(-10 * 24 * time.Hour)
		r, fs, _, _ := newStatusRefresher(h, young)
		r.Activity, r.AdaptiveMaxInterval = &memActivity{items: 0}, 6*time.Hour
		if d := nextDelay(t, r, fs, young); d != 6*time.Hour {
			t.Fatalf("delay=%s want 6h", d)
		}
		young.CreatedAt = time.Now().Add(-15 * 24 * time.Hour)
		if d := nextDelay(t, r, fs, young); d != 12*time.Hour {
			t.Fatalf("delay=%s want 12h", d)
		}
	})

	t.Run("hourly feed polls every 30m", func(t *testing.T) {
		r, fs, _, _ := newStatusRefresher(h, feed)
		r.Activity, r.AdaptiveMaxInterval = &memActivity{items: 168}, 6*time.Hour
		if d := nextDelay(t, r, fs, feed); d != 30*time.Minute {
			t.Fatalf("delay=%s want 30m", d)
		}
	})

	t.Run("busy feed keeps its own interval", func(t *testing.T) {
		r, fs, _, _ := newStatusRefresher(h, feed)
		r.Activity, r.AdaptiveMaxInterval = &memActivity{items: 5000}, 6*time.Hour
		if d := nextDelay(t, r, fs, feed); d != 15*time.Minute {
			t.Fatalf("delay=%s want 15m", d)
		}
	})

	t.Run("ceiling bounded by MAX_POLL_INTERVAL", func(t *testing.T) {
		r, fs, _, _ := newStatusRefresher(h, feed)
		r.MaxPollInterval = 2 * time.Hour
		r.Activity, r.AdaptiveMaxInterval = &memActivity{items: 0}, 6*time.Hour
		if d := nextDelay(t, r, fs, feed); d != 2*time.Hour {
			t.Fatalf("delay=%s want 2h", d)
		}
	})

	t.Run("count error falls back to base", func(t *testing.T) {
		r, fs, _, _ := newStatusRefresher(h, feed)
		r.Activity, r.AdaptiveMaxInterval = &memActivity{err: errors.New("db down")}, 6*time.Hour
		if d := nextDelay(t, r, fs, feed); d != 15*time.Minute {
			t.Fatalf("delay=%s want 15m", d)
		}
	})

	t.Run("no activity store means off", func(t *testing.T) {
		r, fs, _, _ := newStatusRefresher(h, feed)
		r.AdaptiveMaxInterval = 6 * time.Hour
		if d := nextDelay(t, r, fs, feed); d != 15*time.Minute {
			t.Fatalf("delay=%s want 15m", d)
		}
	})

	t.Run("source hint still wins when later", func(t *testing.T) {
		hint := &stubHandler{res: reader.FetchResponse{MinNextCheck: time.Now().Add(8 * time.Hour)}}
		r, fs, _, _ := newStatusRefresher(hint, feed)
		r.Activity, r.AdaptiveMaxInterval = &memActivity{items: 168}, 6*time.Hour
		if d := nextDelay(t, r, fs, feed); d != 8*time.Hour {
			t.Fatalf("delay=%s want 8h", d)
		}
	})
}
