package service

import (
	"context"
	"time"

	"rssam/internal/storage"
)

// pollHoursCacheTTL bounds how long the worker (a separate FeedRefresher from
// the HTTP one) keeps a stale window after an edit; one PK lookup per
// category per TTL, and only when one of its feeds is actually polled.
const pollHoursCacheTTL = 10 * time.Second

type pollHoursCacheItem struct {
	window storage.PollWindow
	ok     bool
}

// ErrOutsidePollWindow is returned by background polls of a feed whose
// category window is closed; it carries the next opening as RetryAt so the
// worker re-schedules without counting an error or writing a poll log row.
type ErrOutsidePollWindow struct{ At time.Time }

func (e ErrOutsidePollWindow) Error() string      { return "outside category poll hours" }
func (e ErrOutsidePollWindow) RetryAt() time.Time { return e.At }

// InvalidatePollHours drops the cached window of a category (0 = all).
func (r *FeedRefresher) InvalidatePollHours(categoryID int64) {
	if r == nil {
		return
	}
	r.pollHoursCache.invalidate(categoryID)
}

// pollWindow combines the category windows of the feed's subscribers: a
// subscriber without a window (no category or an always-open one) lifts the
// restriction; otherwise the feed is polled whenever any window is open and
// the next opening is the earliest one. Lookups are cached for
// pollHoursCacheTTL; on storage error a category counts as unrestricted.
func (r *FeedRefresher) pollWindow(ctx context.Context, subs []storage.Subscription, now time.Time) (storage.PollWindow, bool) {
	if r.PollHours == nil || len(subs) == 0 {
		return storage.PollWindow{}, false
	}
	var (
		windows []storage.PollWindow
		seen    = make(map[int64]struct{}, len(subs))
	)
	for _, sub := range subs {
		if sub.CategoryID == nil {
			return storage.PollWindow{}, false
		}
		id := *sub.CategoryID
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		w, ok := r.categoryPollWindow(ctx, id)
		if !ok {
			return storage.PollWindow{}, false
		}
		windows = append(windows, w)
	}
	best := windows[0]
	for _, w := range windows[1:] {
		if w.Contains(now) {
			return w, true
		}
		if w.NextOpen(now).Before(best.NextOpen(now)) {
			best = w
		}
	}
	return best, true
}

func (r *FeedRefresher) categoryPollWindow(ctx context.Context, id int64) (storage.PollWindow, bool) {
	if it, hit := r.pollHoursCache.get(id, pollHoursCacheTTL); hit {
		return it.window, it.ok
	}
	raw, err := r.PollHours.GetCategoryPollHours(ctx, id)
	if err != nil {
		return storage.PollWindow{}, false
	}
	w, ok, _ := storage.ParsePollHours(raw)
	r.pollHoursCache.put(id, pollHoursCacheItem{window: w, ok: ok})
	return w, ok
}
