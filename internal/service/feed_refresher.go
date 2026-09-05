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
}

func (r *FeedRefresher) dedupOnlyStorage() bool {
	return strings.EqualFold(strings.TrimSpace(r.StoreEntriesMode), "dedup_only")
}

func (r *FeedRefresher) feedUsesHashOnlyStorage(feed storage.Feed) bool {
	return r.dedupOnlyStorage() || feed.StoreHashOnly
}

type RealtimePublisher interface {
	PublishNewEntries(ctx context.Context, feed storage.Feed, newEntries []storage.Entry)
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

// recordFailure persists the circuit-breaker counter and refresh meta for a
// failed poll. Errors here used to be discarded, which hid a dead database
// from the logs while feeds silently stopped being paused/marked.
func (r *FeedRefresher) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func (r *FeedRefresher) recordFailure(ctx context.Context, feed storage.Feed, now time.Time, cause error, bridgeState []byte) {
	if err := r.Feeds.RecordFeedPollFailure(ctx, feed.ID, cause.Error(), r.circuitThreshold(), now); err != nil {
		r.logger().Error("record feed poll failure failed", "feed_id", feed.ID, "err", err)
	}
	if err := r.Feeds.UpdateFeedRefreshMeta(ctx, storage.UpdateFeedRefreshMetaParams{
		ID:            feed.ID,
		ETag:          feed.ETag,
		LastModified:  feed.LastModified,
		LastCheckedAt: now,
		LastError:     cause.Error(),
		BridgeState:   bridgeState,
	}); err != nil {
		r.logger().Error("update feed refresh meta failed", "feed_id", feed.ID, "err", err)
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

func (r *FeedRefresher) scheduleNextCheck(ctx context.Context, feedID int64, intervalMinutes int, from time.Time) error {
	if r.Feeds == nil {
		return nil
	}
	min, max := r.pollBounds()
	next := storage.FeedNextCheckAt(from, intervalMinutes, min, max)
	return r.Feeds.SetFeedNextCheckAt(ctx, feedID, next)
}

// RescheduleFeed sets next_check_at from now using the feed interval (e.g. after interval change).
func (r *FeedRefresher) RescheduleFeed(ctx context.Context, feedID int64, intervalMinutes int) error {
	return r.scheduleNextCheck(ctx, feedID, intervalMinutes, time.Now().UTC())
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

	now := time.Now().UTC()
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
			return 0, fetchErr
		}
		r.recordFailure(ctx, feed, now, fetchErr, bridgeStateJSON(res.BridgeState))
		err := fmt.Errorf("%w: %w", ErrFetchFeed, fetchErr)
		if r.Realtime != nil {
			r.Realtime.PublishFeedStatusChanged(feed, err)
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
			r.recordFailure(ctx, feed, now, err, bridgeStateJSON(res.BridgeState))
			wrappedErr := fmt.Errorf("%w: %w", ErrCreateEntries, err)
			if r.Realtime != nil {
				r.Realtime.PublishFeedStatusChanged(feed, wrappedErr)
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

	_ = r.Feeds.UpdateFeedRefreshMeta(ctx, storage.UpdateFeedRefreshMetaParams{
		ID:            feedID,
		ETag:          etag,
		LastModified:  lastMod,
		LastCheckedAt: now,
		LastError:     "",
		BridgeState:   bridgeStateJSON(res.BridgeState),
	})
	_ = r.scheduleNextCheck(ctx, feedID, feed.IntervalMinutes, now)
	if r.Realtime != nil {
		r.Realtime.PublishFeedStatusChanged(feed, nil)
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
		all, _ := r.Webhooks.ListEnabledWebhooks(ctx, feed.UserID, 1000)
		for _, wh := range all {
			if wh.FilterID != nil {
				legacyWebhooks = append(legacyWebhooks, wh)
			}
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
