package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"rssam/internal/filter"
	"rssam/internal/metrics"
	"rssam/internal/reader"
	"rssam/internal/storage"
)

type FeedRefresher struct {
	Feeds    storage.FeedStore
	Entries  storage.EntryStore
	Dedup    storage.EntryDedupStore
	Registry *reader.HandlerRegistry

	Filters storage.FilterStore
	Matches storage.FilterMatchStore
	Labels  storage.LabelStore
	Engine  *filter.Engine

	Webhooks    storage.WebhookStore
	WebhookLogs storage.WebhookLogStore

	// PollLog receives one row per finished poll attempt (nil = history off).
	PollLog storage.FeedPollLogStore

	Realtime RealtimePublisher

	// Log receives persistence errors that must not abort a poll but must not
	// disappear either (nil = slog.Default()).
	Log *slog.Logger

	// StoreEntriesMode: "full" (default) or "dedup_only" — see STORE_ENTRIES_MODE / DEDUP_ONLY_STORAGE.
	StoreEntriesMode string

	// CircuitBreakerThreshold consecutive poll errors before pausing background polling (0 = default 10).
	CircuitBreakerThreshold int

	MinPollInterval time.Duration
	MaxPollInterval time.Duration

	filterCacheOnce sync.Once
	filterCache     *enabledFilterCache

	webhookCacheOnce sync.Once
	webhookCache     *enabledWebhookCache
}

func (r *FeedRefresher) dedupOnlyStorage() bool {
	return strings.EqualFold(strings.TrimSpace(r.StoreEntriesMode), "dedup_only")
}

func (r *FeedRefresher) feedUsesHashOnlyStorage(feed storage.Feed) bool {
	return r.dedupOnlyStorage() || feed.StoreHashOnly
}

type RealtimePublisher interface {
	PublishNewEntries(ctx context.Context, feed storage.Feed, newEntries []storage.Entry)
	// PublishFeedStatusChanged receives the feed as it looks *after* the poll
	// was persisted (ParsingErrorCount, PollPaused, NextCheckAt, LastError,
	// LastCheckedAt are already updated on the copy).
	PublishFeedStatusChanged(feed storage.Feed, err error)
}

var (
	ErrFetchFeed     = errors.New("fetch feed")
	ErrCreateEntries = errors.New("create entries")
)

func (r *FeedRefresher) circuitThreshold() int {
	if r.CircuitBreakerThreshold > 0 {
		return r.CircuitBreakerThreshold
	}
	return 10
}

func (r *FeedRefresher) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// recordFailure persists the circuit-breaker counter, refresh meta and the
// error-backoff next_check_at for a failed poll, and returns the feed as it
// now looks in the database (for the WebSocket status event). Persistence
// errors are logged, not swallowed: a dead database would otherwise silently
// stop feeds from being paused/marked.
func (r *FeedRefresher) recordFailure(ctx context.Context, feed storage.Feed, now time.Time, cause error, bridgeState []byte) storage.Feed {
	after := feed
	after.ParsingErrorCount = feed.ParsingErrorCount + 1
	after.PollPaused = feed.PollPaused || after.ParsingErrorCount >= r.circuitThreshold()
	after.LastError = cause.Error()
	after.LastCheckedAt = &now
	// Same backoff the worker applies to the job: base interval × 2^errors.
	min, max := r.pollBounds()
	next := storage.FeedNextCheckAfterError(now, feed.IntervalMinutes, after.ParsingErrorCount, min, max)
	after.NextCheckAt = &next
	if err := r.Feeds.RecordFeedPollFailure(ctx, storage.RecordFeedPollFailureParams{
		ID:          feed.ID,
		Error:       cause.Error(),
		Threshold:   r.circuitThreshold(),
		CheckedAt:   now,
		NextCheckAt: next,
		BridgeState: bridgeState,
	}); err != nil {
		r.logger().Error("record feed poll failure failed", "feed_id", feed.ID, "err", err)
	}
	return after
}

// recordPoll appends one row to the per-feed poll history (best effort).
func (r *FeedRefresher) recordPoll(ctx context.Context, feedID int64, at time.Time, started time.Time, inserted int, cause error) {
	if r.PollLog == nil {
		return
	}
	p := storage.RecordFeedPollParams{
		FeedID:   feedID,
		At:       at,
		OK:       cause == nil,
		Inserted: inserted,
		Duration: time.Since(started),
	}
	if cause != nil {
		p.Error = cause.Error()
	}
	if err := r.PollLog.RecordFeedPoll(ctx, p); err != nil {
		r.logger().Warn("record feed poll log failed", "feed_id", feedID, "err", err)
	}
}

func (r *FeedRefresher) pollBounds() (min, max time.Duration) {
	min = r.MinPollInterval
	if min <= 0 {
		min = time.Minute
	}
	max = r.MaxPollInterval
	if max <= 0 {
		max = 24 * time.Hour
	}
	return min, max
}

func (r *FeedRefresher) scheduleNextCheck(ctx context.Context, feedID int64, intervalMinutes int, from time.Time) (time.Time, error) {
	min, max := r.pollBounds()
	next := storage.FeedNextCheckAt(from, intervalMinutes, min, max)
	if r.Feeds == nil {
		return next, nil
	}
	return next, r.Feeds.SetFeedNextCheckAt(ctx, feedID, next)
}

// RescheduleFeed sets next_check_at from now using the feed interval (e.g. after interval change).
func (r *FeedRefresher) RescheduleFeed(ctx context.Context, feedID int64, intervalMinutes int) error {
	_, err := r.scheduleNextCheck(ctx, feedID, intervalMinutes, time.Now().UTC())
	return err
}

// RefreshFeed polls a feed and persists entries. Use ResetCircuit first for manual recovery.
func (r *FeedRefresher) RefreshFeed(ctx context.Context, feedID int64) (inserted int, err error) {
	if r.Feeds == nil || r.Entries == nil || r.Registry == nil {
		return 0, errors.New("feed refresher is not configured")
	}
	feed, err := r.Feeds.GetFeedByID(ctx, feedID)
	if err != nil {
		return 0, fmt.Errorf("get feed: %w", err)
	}
	return r.RefreshLoadedFeed(ctx, feed)
}

// RefreshLoadedFeed polls an already-loaded feed (worker already fetched it for pause checks).
func (r *FeedRefresher) RefreshLoadedFeed(ctx context.Context, feed storage.Feed) (inserted int, err error) {
	return r.refreshLoaded(ctx, feed, false)
}

// RefreshFeedManual resets the circuit breaker then polls (POST /v1/feeds/{id}/refresh).
func (r *FeedRefresher) RefreshFeedManual(ctx context.Context, feedID int64) (inserted int, err error) {
	if r.Feeds != nil {
		if err := r.Feeds.ResetFeedPollCircuit(ctx, feedID); err != nil {
			r.logger().Warn("reset feed poll circuit failed", "feed_id", feedID, "err", err)
		}
	}
	if r.Feeds == nil || r.Entries == nil || r.Registry == nil {
		return 0, errors.New("feed refresher is not configured")
	}
	feed, err := r.Feeds.GetFeedByID(ctx, feedID)
	if err != nil {
		return 0, fmt.Errorf("get feed: %w", err)
	}
	feed.PollPaused = false
	feed.ParsingErrorCount = 0
	return r.refreshLoaded(ctx, feed, true)
}

func (r *FeedRefresher) refreshLoaded(ctx context.Context, feed storage.Feed, manual bool) (inserted int, err error) {
	if r.Feeds == nil || r.Entries == nil || r.Registry == nil {
		return 0, errors.New("feed refresher is not configured")
	}
	feedID := feed.ID
	if !manual && storage.FeedPollingBlocked(feed) {
		return 0, ErrFeedCircuitOpen
	}

	started := time.Now()
	now := started.UTC()
	bridgeState := reader.ParseBridgeState(feed.BridgeState)

	if manual && reader.NormalizeFeedType(feed.FeedType) == reader.FeedTypeMax {
		if bridgeState.Max == nil {
			bridgeState.Max = &reader.MaxBridgeState{}
		}
		bridgeState.Max.LastEndTimeMs = 0
	}

	res, fetchErr := r.Registry.Fetch(ctx, reader.FetchRequest{
		FeedURL:       feed.FeedURL,
		FeedType:      feed.FeedType,
		UserAgent:     feed.UserAgent,
		ETag:          feed.ETag,
		LastModified:  feed.LastModified,
		BridgeState:   bridgeState,
		FetchViaProxy: feed.FetchViaProxy,
		TLSInsecure:   feed.TLSInsecure,
	})
	if fetchErr != nil {
		if _, ok := reader.RetryAt(fetchErr); ok {
			// Rate limited by the source: not counted as a feed error, but it
			// is still a poll that did not deliver anything.
			r.recordPoll(ctx, feedID, now, started, 0, fetchErr)
			return 0, fetchErr
		}
		after := r.recordFailure(ctx, feed, now, fetchErr, bridgeStateJSON(res.BridgeState))
		err := fmt.Errorf("%w: %w", ErrFetchFeed, fetchErr)
		r.recordPoll(ctx, feedID, now, started, 0, err)
		if r.Realtime != nil {
			r.Realtime.PublishFeedStatusChanged(after, err)
		}
		return 0, err
	}

	inserted = 0
	if !res.NotModified {
		entries := reader.ApplyFeedRules(res.Entries, feed.BlockedRules, feed.KeepRules)
		entries = reader.ApplyURLRewriteRules(entries, feed.RewriteRules)

		var insertedEntries []storage.Entry
		if r.feedUsesHashOnlyStorage(feed) {
			inserted, insertedEntries, err = r.processEntriesDedupOnly(ctx, feed, entries)
		} else {
			inserted, insertedEntries, err = r.Entries.CreateEntries(ctx, feedID, entries)
		}
		if err != nil {
			after := r.recordFailure(ctx, feed, now, err, bridgeStateJSON(res.BridgeState))
			wrappedErr := fmt.Errorf("%w: %w", ErrCreateEntries, err)
			r.recordPoll(ctx, feedID, now, started, 0, wrappedErr)
			if r.Realtime != nil {
				r.Realtime.PublishFeedStatusChanged(after, wrappedErr)
			}
			return 0, wrappedErr
		}

		r.applyFiltersBestEffort(ctx, feed, insertedEntries)
		if r.Realtime != nil && len(insertedEntries) > 0 {
			r.Realtime.PublishNewEntries(ctx, feed, insertedEntries)
		}
	}

	etag := strings.TrimSpace(res.ETag)
	lastMod := strings.TrimSpace(res.LastModified)
	if etag == "" {
		etag = feed.ETag
	}
	if lastMod == "" {
		lastMod = feed.LastModified
	}

	min, max := r.pollBounds()
	next := storage.FeedNextCheckAt(now, feed.IntervalMinutes, min, max)
	if err := r.Feeds.UpdateFeedRefreshMeta(ctx, storage.UpdateFeedRefreshMetaParams{
		ID:            feedID,
		ETag:          etag,
		LastModified:  lastMod,
		LastCheckedAt: now,
		LastError:     "",
		BridgeState:   bridgeStateJSON(res.BridgeState),
		NextCheckAt:   &next,
	}); err != nil {
		r.logger().Error("update feed refresh meta failed", "feed_id", feedID, "err", err)
	}
	r.recordPoll(ctx, feedID, now, started, inserted, nil)
	if r.Realtime != nil {
		after := feed
		after.ParsingErrorCount = 0
		after.PollPaused = false
		after.LastError = ""
		after.LastCheckedAt = &now
		after.NextCheckAt = &next
		r.Realtime.PublishFeedStatusChanged(after, nil)
	}

	return inserted, nil
}

func bridgeStateJSON(st reader.BridgeState) []byte {
	if st.Max == nil && st.Telegram == nil && st.VKSearch == nil && st.Rutube == nil && st.DzenNews == nil && st.Smotrim == nil {
		return nil
	}
	b, err := json.Marshal(st)
	if err != nil {
		return nil
	}
	return b
}

func (r *FeedRefresher) applyFiltersBestEffort(ctx context.Context, feed storage.Feed, entries []storage.Entry) {
	if len(entries) == 0 {
		return
	}
	if r.Filters == nil || r.Matches == nil || r.Engine == nil {
		return
	}
	filters, err := r.listEnabledFiltersCached(ctx, feed.UserID)
	if err != nil || len(filters) == 0 {
		r.enqueueFeedWebhooksBestEffort(ctx, feed, entries)
		return
	}
	var legacyWebhooks []storage.Webhook
	if r.Webhooks != nil && r.WebhookLogs != nil {
		var err error
		legacyWebhooks, err = r.listLegacyFilterWebhooksCached(ctx, feed.UserID)
		if err != nil && r.Log != nil {
			r.Log.Warn("list enabled webhooks failed; legacy filter webhooks skipped for this refresh",
				"feed_id", feed.ID, "user_id", feed.UserID, "err", err)
		}
	}
	matchCtx := filter.MatchContext{FeedID: feed.ID, CategoryID: feed.CategoryID}
	now := time.Now().UTC()
	for _, e := range entries {
		start := time.Now()
		matches, err := r.Engine.MatchEntryWithContext(e, matchCtx, filters)
		metrics.FilterProcessingDuration.Observe(time.Since(start).Seconds())
		if err != nil {
			continue
		}
		matchedFilterIDs := make(map[int64]struct{}, len(matches))
		if len(matches) > 0 {
			toInsert := make([]storage.CreateFilterMatchParams, 0, len(matches))
			for _, m := range matches {
				toInsert = append(toInsert, storage.CreateFilterMatchParams{
					FilterID:  m.FilterID,
					EntryID:   e.ID,
					MatchedAt: now,
					Details:   m.Details,
				})
				matchedFilterIDs[m.FilterID] = struct{}{}
			}
			if inserted, err := r.Matches.CreateFilterMatches(ctx, toInsert); err == nil && inserted > 0 {
				metrics.FilterMatchesTotal.Add(float64(inserted))
			}
			r.applyFilterActions(ctx, feed.UserID, e, filters, matchedFilterIDs)
			enqueueLegacyFilterWebhooks(ctx, legacyWebhooks, r.WebhookLogs, matchedFilterIDs, e.ID)
		}
		if feed.WebhookID != nil && r.WebhookLogs != nil {
			_ = r.WebhookLogs.EnqueueWebhookLogs(ctx, []int64{*feed.WebhookID}, e.ID)
		}
	}
}

func (r *FeedRefresher) enqueueFeedWebhooksBestEffort(ctx context.Context, feed storage.Feed, entries []storage.Entry) {
	if feed.WebhookID == nil || r.WebhookLogs == nil || len(entries) == 0 {
		return
	}
	for _, e := range entries {
		_ = r.WebhookLogs.EnqueueWebhookLogs(ctx, []int64{*feed.WebhookID}, e.ID)
	}
}
