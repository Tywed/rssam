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

// categoryPollWindow returns the feed's category window, if any. Lookups are
// cached for pollHoursCacheTTL; on storage error the feed is treated as
// unrestricted.
func (r *FeedRefresher) categoryPollWindow(ctx context.Context, feed storage.Feed) (storage.PollWindow, bool) {
	if r.PollHours == nil || feed.CategoryID == nil {
		return storage.PollWindow{}, false
	}
	id := *feed.CategoryID
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
