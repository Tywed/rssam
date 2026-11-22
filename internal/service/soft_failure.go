package service

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"rssam/internal/metrics"
	"rssam/internal/storage"
)

// softFailureLimit is how many soft failures in a row a healthy feed may
// have before one counts as a poll failure. On the production fleet 97 % of
// Telegram «channel is private or does not exist» answers and every apimax
// «Not connected to the server» clear by the next poll; each used to cost
// an error counter, a doubled interval, a poll-log row and two journal
// lines (failed, recovered).
const softFailureLimit = 3

// softFailureMarkers are the transient answers, matched case-insensitively
// against the error text. Network timeouts and resets are matched by type.
var softFailureMarkers = []string{
	"channel is private or does not exist",
	"not connected to the server",
	"connection error",
	"keepalive",
	"no close frame",
	"context deadline exceeded",
	"client.timeout exceeded",
	"tls handshake timeout",
	"connection reset by peer",
	"connection refused",
	"server misbehaving",
	"i/o timeout",
	"unexpected eof",
	// Dzen answers 404 for a living channel every few hours (≈125 flaps in
	// 5 days on 41 channels) and clears by the next poll.
	"dzen_channel: channel not found",
}

// softStatusPattern matches a 429 or 5xx from any bridge («unexpected
// status 503», «unexpected status code: 502»): the front door is busy, not
// the feed. A real outage still becomes a failure after softFailureLimit.
var softStatusPattern = regexp.MustCompile(`unexpected status(?: code)?:? (?:429|5\d\d)\b`)

// IsSoftFailure reports whether err looks like a passing condition of the
// network or of the source's front door rather than a fault of the feed.
func IsSoftFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, m := range softFailureMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return softStatusPattern.MatchString(msg)
}

// SoftFailureReason buckets a soft failure for metrics (bounded label set).
func SoftFailureReason(err error) string {
	if err == nil {
		return "other"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "channel is private or does not exist"), strings.Contains(msg, "channel not found"):
		return "unavailable"
	case strings.Contains(msg, "status code: 429"), strings.Contains(msg, "status 429"):
		return "status_429"
	case softStatusPattern.MatchString(msg):
		return "status_5xx"
	case strings.Contains(msg, "connection reset"), strings.Contains(msg, "unexpected eof"), strings.Contains(msg, "no close frame"), strings.Contains(msg, "keepalive"), strings.Contains(msg, "not connected to the server"), strings.Contains(msg, "connection error"):
		return "reset"
	case strings.Contains(msg, "connection refused"):
		return "refused"
	case strings.Contains(msg, "server misbehaving"), strings.Contains(msg, "no such host"):
		return "dns"
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded") {
		return "timeout"
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return "timeout"
	}
	return "other"
}

// fetchedVia is implemented by bridge errors that know which proxy path
// produced the answer (telegram.ChannelUnavailableError).
type fetchedVia interface{ FetchedVia() string }

// softStreaks counts soft failures in a row per feed, in memory: nothing
// about a soft failure is written anywhere. A success drops the feed.
type softStreaks struct {
	mu sync.Mutex
	n  map[int64]int
}

// next records one more soft failure and reports whether the feed is still
// within the limit.
func (s *softStreaks) next(feedID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.n == nil {
		s.n = make(map[int64]int)
	}
	s.n[feedID]++
	return s.n[feedID] <= softFailureLimit
}

func (s *softStreaks) reset(feedID int64) {
	s.mu.Lock()
	delete(s.n, feedID)
	s.mu.Unlock()
}

// ErrSoftFailure defers a poll that failed softly to the feed's next
// ordinary check; the refresher has already written next_check_at.
type ErrSoftFailure struct {
	At  time.Time
	Err error
}

func (e ErrSoftFailure) Error() string            { return e.Err.Error() }
func (e ErrSoftFailure) Unwrap() error            { return e.Err }
func (e ErrSoftFailure) RetryAt() time.Time       { return e.At }
func (e ErrSoftFailure) DeferReason() string      { return "soft_error" }
func (e ErrSoftFailure) NextCheckPersisted() bool { return true }

// deferSoft handles a soft failure of a healthy feed: one UPDATE moves
// last_checked_at and next_check_at the way a quiet poll would, keeping
// the error counter, last_error, cursors and the poll log as they were.
func (r *FeedRefresher) deferSoft(ctx context.Context, feed storage.Feed, now, next time.Time, cause error) error {
	reason := SoftFailureReason(cause)
	metrics.PollSoftFailures.WithLabelValues(feed.FeedType, reason).Inc()
	// One line per absorbed failure, with the proxy path when the bridge
	// knows it: whether Telegram's «private» flaps cluster by proxy is the
	// open question of the production fleet.
	var fv fetchedVia
	if errors.As(cause, &fv) && fv.FetchedVia() != "" {
		r.logger().Info("poll_feed soft failure", "feed_id", feed.ID, "bridge", feed.FeedType, "reason", reason, "via", fv.FetchedVia())
	} else {
		r.logger().Debug("poll_feed soft failure", "feed_id", feed.ID, "bridge", feed.FeedType, "reason", reason, "err", cause.Error())
	}
	meta := storage.UpdateFeedRefreshMetaParams{
		ID:            feed.ID,
		ETag:          feed.ETag,
		LastModified:  feed.LastModified,
		LastCheckedAt: now,
		LastError:     feed.LastError,
		BridgeState:   feed.BridgeState,
		NextCheckAt:   &next,
	}
	if err := r.Feeds.UpdateFeedRefreshMeta(ctx, meta); err != nil {
		r.logger().Error("record soft failure failed", "feed_id", feed.ID, "err", err)
	}
	return ErrSoftFailure{At: next, Err: cause}
}
