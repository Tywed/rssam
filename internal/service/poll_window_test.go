package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"rssam/internal/reader"
	"rssam/internal/storage"
)

type memPollHours struct {
	hours map[int64]string
	calls int
}

func (m *memPollHours) GetCategoryPollHours(_ context.Context, id int64) (string, error) {
	m.calls++
	v, ok := m.hours[id]
	if !ok {
		return "", storage.ErrNotFound
	}
	return v, nil
}

func (m *memPollHours) SetCategoryPollHours(context.Context, int64, int64, string) error { return nil }

// closedWindow is a 1-hour window that starts 2 hours from now (local time),
// so "now" is always outside it and the next opening is ~2 h away.
func closedWindow(now time.Time) (string, time.Time) {
	start := now.Local().Add(2 * time.Hour).Truncate(time.Minute)
	end := start.Add(time.Hour)
	return fmt.Sprintf("%02d:%02d-%02d:%02d", start.Hour(), start.Minute(), end.Hour(), end.Minute()), start
}

func TestRefreshLoadedFeed_OutsideCategoryWindowSkipsFetch(t *testing.T) {
	now := time.Now()
	spec, opensAt := closedWindow(now)
	catID := int64(5)
	feed := storage.Feed{ID: 21, UserID: 1, FeedURL: "https://example.com/f.xml", IntervalMinutes: 15, CategoryID: &catID}
	h := &stubHandler{}
	r, fs, pl, pub := newStatusRefresher(h, feed)
	ph := &memPollHours{hours: map[int64]string{catID: spec}}
	r.PollHours = ph

	_, err := r.RefreshLoadedFeed(context.Background(), feed)
	var outside ErrOutsidePollWindow
	if !errors.As(err, &outside) {
		t.Fatalf("err=%v, want ErrOutsidePollWindow", err)
	}
	if outside.At.Sub(opensAt).Abs() > time.Second {
		t.Fatalf("RetryAt=%s, want window start %s", outside.At, opensAt)
	}
	if at, ok := reader.RetryAt(err); !ok || !at.Equal(outside.At) {
		t.Fatalf("worker must see it as retry-at: %v %v", at, ok)
	}
	if h.calls != 0 {
		t.Fatalf("fetch must not happen outside the window, calls=%d", h.calls)
	}
	if fs.updates != 0 || fs.failures != 0 || len(pl.rows) != 0 || len(pub.feeds) != 0 {
		t.Fatalf("no writes expected: updates=%d failures=%d polls=%d events=%d", fs.updates, fs.failures, len(pl.rows), len(pub.feeds))
	}

	// Window lookups are cached per category.
	if _, err := r.RefreshLoadedFeed(context.Background(), feed); !errors.As(err, &outside) {
		t.Fatalf("second call err=%v", err)
	}
	if ph.calls != 1 {
		t.Fatalf("store calls=%d, want 1 (cached)", ph.calls)
	}
	r.InvalidatePollHours(catID)
	ph.hours[catID] = ""
	if _, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil {
		t.Fatalf("after clearing the window the poll must run: %v", err)
	}
	if h.calls != 1 || ph.calls != 2 {
		t.Fatalf("calls: fetch=%d store=%d", h.calls, ph.calls)
	}

	// Manual refresh ignores the window.
	ph.hours[catID] = spec
	r.InvalidatePollHours(0)
	if _, err := r.RefreshFeedManual(context.Background(), feed.ID); err != nil {
		t.Fatalf("manual: %v", err)
	}
	if h.calls != 2 {
		t.Fatalf("manual refresh must fetch, calls=%d", h.calls)
	}
}

func TestRefreshLoadedFeed_NextCheckClampedToWindow(t *testing.T) {
	// Window is open right now and closes in ~1 minute; the 15-minute
	// interval would land outside it, so next_check_at moves to tomorrow's opening.
	now := time.Now().Local()
	start := now.Add(-30 * time.Minute).Truncate(time.Minute)
	end := now.Add(time.Minute).Truncate(time.Minute).Add(time.Minute)
	if end.Day() != start.Day() {
		t.Skip("window would wrap midnight; run again later")
	}
	spec := fmt.Sprintf("%02d:%02d-%02d:%02d", start.Hour(), start.Minute(), end.Hour(), end.Minute())
	catID := int64(6)
	feed := storage.Feed{ID: 22, UserID: 1, FeedURL: "https://example.com/f.xml", IntervalMinutes: 15, CategoryID: &catID}
	h := &stubHandler{}
	r, fs, _, _ := newStatusRefresher(h, feed)
	r.PollHours = &memPollHours{hours: map[int64]string{catID: spec}}

	if _, err := r.RefreshLoadedFeed(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
	if h.calls != 1 {
		t.Fatalf("fetch calls=%d", h.calls)
	}
	want := start.AddDate(0, 0, 1)
	if fs.next.Sub(want).Abs() > time.Second {
		t.Fatalf("next_check_at=%s, want tomorrow's opening %s", fs.next, want)
	}

	// Feed without a category, or a category without a window, keeps its interval.
	plain := storage.Feed{ID: 23, UserID: 1, FeedURL: "https://example.com/p.xml", IntervalMinutes: 15}
	r2, fs2, _, _ := newStatusRefresher(&stubHandler{}, plain)
	r2.PollHours = &memPollHours{hours: map[int64]string{}}
	if _, err := r2.RefreshLoadedFeed(context.Background(), plain); err != nil {
		t.Fatal(err)
	}
	if d := time.Until(fs2.next); d < 14*time.Minute || d > 16*time.Minute {
		t.Fatalf("plain feed delay=%s, want ~15m", d)
	}
}
