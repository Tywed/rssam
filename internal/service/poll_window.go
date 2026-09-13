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
	at     time.Time
	window storage.PollWindow
	ok     bool
}

// ErrOutsidePollWindow is returned by background polls of a feed whose
// category window is closed; it carries the next opening as RetryAt so the
// worker re-schedules without counting an error or writing a poll log row.
type ErrOutsidePollWindow struct{ At time.Time }

func (e ErrOutsidePollWindow) Error() string      { return "outside category poll hours" }
func (e ErrOutsidePollWindow) RetryAt() time.Time { return e.At }

func (r *FeedRefresher) initPollHoursCache() {
	r.pollHoursOnce.Do(func() {
		r.pollHoursCache = make(map[int64]pollHoursCacheItem)
	})
}

// InvalidatePollHours drops the cached window of a category (0 = all).
func (r *FeedRefresher) InvalidatePollHours(categoryID int64) {
	if r == nil {
		return
	}
	r.initPollHoursCache()
	r.pollHoursMu.Lock()
	defer r.pollHoursMu.Unlock()
	if categoryID <= 0 {
		r.pollHoursCache = make(map[int64]pollHoursCacheItem)
		return
	}
	delete(r.pollHoursCache, categoryID)
}

// categoryPollWindow returns the feed's category window, if any. Lookups are
// cached for pollHoursCacheTTL; on storage error the feed is treated as
// unrestricted.
func (r *FeedRefresher) categoryPollWindow(ctx context.Context, feed storage.Feed) (storage.PollWindow, bool) {
	if r.PollHours == nil || feed.CategoryID == nil {
		return storage.PollWindow{}, false
	}
	id := *feed.CategoryID
	r.initPollHoursCache()
	r.pollHoursMu.Lock()
	if it, hit := r.pollHoursCache[id]; hit && time.Since(it.at) < pollHoursCacheTTL {
		r.pollHoursMu.Unlock()
		return it.window, it.ok
	}
	r.pollHoursMu.Unlock()

	raw, err := r.PollHours.GetCategoryPollHours(ctx, id)
	if err != nil {
		return storage.PollWindow{}, false
	}
	w, ok, _ := storage.ParsePollHours(raw)
	r.pollHoursMu.Lock()
	r.pollHoursCache[id] = pollHoursCacheItem{at: time.Now(), window: w, ok: ok}
	r.pollHoursMu.Unlock()
	return w, ok
}
