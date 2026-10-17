package service

import (
	"net/url"
	"strings"
	"sync"
	"time"

	"rssam/internal/reader"
)

// HostLimiter caps how many polls hit one source host at the same time
// (POLL_HOST_CONCURRENCY). A poll that finds the host full is deferred, not
// queued: the worker would otherwise hold a slot idle.
type HostLimiter struct {
	limit int
	mu    sync.Mutex
	busy  map[string]int
}

func NewHostLimiter(limit int) *HostLimiter {
	if limit <= 0 {
		return nil
	}
	return &HostLimiter{limit: limit, busy: map[string]int{}}
}

// Acquire takes a slot of host; release must be called when ok.
func (l *HostLimiter) Acquire(host string) (release func(), ok bool) {
	if l == nil || host == "" {
		return func() {}, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.busy[host] >= l.limit {
		return nil, false
	}
	l.busy[host]++
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.busy[host] <= 1 {
			delete(l.busy, host)
		} else {
			l.busy[host]--
		}
	}, true
}

// ErrHostBusy defers a poll whose host already has POLL_HOST_CONCURRENCY
// polls in flight.
type ErrHostBusy struct {
	Host string
	At   time.Time
}

func (e ErrHostBusy) Error() string       { return "host busy: " + e.Host }
func (e ErrHostBusy) RetryAt() time.Time  { return e.At }
func (e ErrHostBusy) DeferReason() string { return "host" }

// hostBusyRetry is when a deferred poll comes back: a minute plus up to
// 30 s derived from the feed id, so the feeds of one host do not line up
// again.
func hostBusyRetry(now time.Time, feedID int64) time.Time {
	return now.Add(time.Minute + time.Duration(feedID%31)*time.Second)
}

// bridgeHosts maps bridge feed types to the host their requests go to;
// their feed URLs may be pseudo-schemes (dzen-news://, ok-group://).
var bridgeHosts = map[string]string{
	reader.FeedTypeTelegram:    "t.me",
	reader.FeedTypeMax:         "max.ru",
	reader.FeedTypeMaxstat:     "maxstat.ru",
	reader.FeedTypeVKSearch:    "vk.com",
	reader.FeedTypeRutube:      "rutube.ru",
	reader.FeedTypeDzenNews:    "dzen.ru",
	reader.FeedTypeDzenChannel: "dzen.ru",
	reader.FeedTypeOKGroup:     "ok.ru",
	reader.FeedTypeSmotrim:     "smotrim.ru",
}

// PollHost is the limiter key of a feed: the bridge's host, or the feed
// URL's host without "www." ("" = unknown, never limited).
func PollHost(feedURL, feedType string) string {
	if h, ok := bridgeHosts[reader.NormalizeFeedType(feedType)]; ok {
		return h
	}
	raw := strings.TrimSpace(feedURL)
	raw = strings.TrimPrefix(raw, "page+")
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	return strings.TrimPrefix(host, "www.")
}
