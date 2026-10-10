package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"rssam/internal/bridge/v1"
	maxbridge "rssam/internal/reader/max"
	"sort"
	"strconv"
	"strings"
	"time"

	"rssam/internal/filter"
	"rssam/internal/metrics"
	"rssam/internal/reader"
	"rssam/internal/storage"

	"github.com/cespare/xxhash/v2"
)

// FeedSubscribers is the slice of the subscription store the refresher needs.
type FeedSubscribers interface {
	ListFeedSubscribers(ctx context.Context, feedID int64) ([]storage.Subscription, error)
}

type FeedRefresher struct {
	Feeds    storage.FeedStore
	Entries  storage.EntryStore
	Dedup    storage.EntryDedupStore
	Registry *reader.HandlerRegistry
	// Subscribers lists who reads a feed: filters, webhooks and realtime
	// events are applied per subscriber (nil = nobody, as for a feed with no
	// subscribers).
	Subscribers FeedSubscribers

	Filters storage.FilterStore
	Matches storage.FilterMatchStore
	Labels  storage.LabelStore
	Engine  *filter.Engine
	// Queries evaluates `query` rules in SQL (nil = such rules never match).
	Queries filter.QueryMatcher

	Webhooks    storage.WebhookStore
	WebhookLogs storage.WebhookLogStore

	// PollLog receives one row per finished poll attempt (nil = history off).
	PollLog storage.FeedPollLogStore

	Realtime RealtimePublisher

	// Log receives persistence errors that must not abort a poll but must not
	// disappear either (nil = slog.Default()).
	Log *slog.Logger

	// StoreEntriesMode: "full" (default) or "dedup_only" — see STORE_ENTRIES_MODE.
	StoreEntriesMode string

	// CircuitBreakerThreshold consecutive poll errors before pausing background polling (0 = default 10).
	CircuitBreakerThreshold int

	MinPollInterval time.Duration
	MaxPollInterval time.Duration
	// AdaptiveMaxInterval caps adaptive_interval feeds; Activity supplies the
	// weekly item count (nil = adaptive polling off).
	AdaptiveMaxInterval time.Duration
	Activity            storage.FeedActivityStore

	// PollHours supplies per-category polling windows (nil = no windows).
	PollHours storage.CategoryPollHoursStore

	filterCache    ttlCache[[]storage.Filter]
	pollHoursCache ttlCache[pollHoursCacheItem]
}

func (r *FeedRefresher) dedupOnlyStorage() bool {
	return strings.EqualFold(strings.TrimSpace(r.StoreEntriesMode), "dedup_only")
}

func (r *FeedRefresher) feedUsesHashOnlyStorage(feed storage.Feed) bool {
	return r.dedupOnlyStorage() || feed.StoreHashOnly
}

type RealtimePublisher interface {
	// PublishNewEntries notifies every subscriber in subs; feed carries the
	// catalog row, each subscription its own category.
	PublishNewEntries(ctx context.Context, feed storage.Feed, subs []storage.Subscription, newEntries []storage.Entry)
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
	subs := r.feedSubscribers(ctx, feedID)
	window, hasWindow := r.pollWindow(ctx, feed)
	if !manual && hasWindow && !window.Contains(now) {
		return 0, ErrOutsidePollWindow{At: window.NextOpen(now)}
	}
	bridgeState := reader.ParseBridgeState(feed.BridgeState)
	if manual {
		bridgeState = fullRefetchState(&feed, bridgeState)
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
		return 0, r.failPoll(ctx, feed, now, started, res.BridgeState, fetchErr, ErrFetchFeed, errors.Is(fetchErr, reader.ErrFeedGone))
	}

	// fresh counts items the feed delivered for the first time, including
	// hash-only ones that never become entries; it stamps last_entry_at.
	var fresh int
	var itemsHash *string
	if !res.NotModified {
		var insertedEntries []storage.Entry
		inserted, insertedEntries, fresh, itemsHash, err = r.storeFetched(ctx, feed, subs, res.Entries)
		if err != nil {
			return 0, r.failPoll(ctx, feed, now, started, res.BridgeState, err, ErrCreateEntries, false)
		}
		r.applyFiltersBestEffort(ctx, feed, subs, insertedEntries)
		if r.Realtime != nil && len(insertedEntries) > 0 {
			r.Realtime.PublishNewEntries(ctx, feed, subs, insertedEntries)
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
	next := r.nextCheckAfterPoll(ctx, feed, now, res.MinNextCheck)
	if hasWindow {
		next = window.NextOpen(next)
	}
	movedTo := movedFeedURL(feed, res.NewURL)
	if movedTo != "" {
		r.logger().Info("feed moved permanently", "feed_id", feedID, "from", feed.FeedURL, "to", movedTo)
	}
	if err := r.Feeds.UpdateFeedRefreshMeta(ctx, storage.UpdateFeedRefreshMetaParams{
		ID:            feedID,
		ETag:          etag,
		LastModified:  lastMod,
		LastCheckedAt: now,
		LastError:     "",
		BridgeState:   bridgeStateJSON(res.BridgeState),
		NextCheckAt:   &next,
		NewEntries:    fresh,
		NewFeedURL:    movedTo,
		ItemsHash:     itemsHash,
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

// fullRefetchState strips every shortcut a manual refresh must not take:
// the RSS body hash, the item-set hash and the Max bridge's time cursor.
func fullRefetchState(feed *storage.Feed, st reader.BridgeState) reader.BridgeState {
	st = st.With(reader.FeedTypeRSS, nil)
	feed.ItemsHash = ""
	if reader.NormalizeFeedType(feed.FeedType) == reader.FeedTypeMax {
		var ms maxbridge.FetchState
		_ = bridge.DecodeState(st.Raw(reader.FeedTypeMax), &ms)
		ms.LastEndTimeMs = 0
		st = st.With(reader.FeedTypeMax, bridge.EncodeState(ms))
	}
	return st
}

// failPoll records a failed poll (circuit counter, poll log, realtime
// status) and returns the error wrapped in kind. gone parks the feed the
// way an admin would: a 410 is final, neither the backoff nor the daily
// reset should poll it again; the error text stays visible on the feed
// until someone deletes it or changes the URL.
func (r *FeedRefresher) failPoll(ctx context.Context, feed storage.Feed, now, started time.Time, state reader.BridgeState, cause, kind error, gone bool) error {
	after := r.recordFailure(ctx, feed, now, cause, bridgeStateJSON(state))
	if gone {
		if err := r.Feeds.SetFeedManualPaused(ctx, feed.ID, true); err != nil {
			r.logger().Error("pause gone feed failed", "feed_id", feed.ID, "err", err)
		} else {
			after.ManualPaused = true
		}
	}
	err := fmt.Errorf("%w: %w", kind, cause)
	r.recordPoll(ctx, feed.ID, now, started, 0, err)
	if r.Realtime != nil {
		r.Realtime.PublishFeedStatusChanged(after, err)
	}
	return err
}

// storeFetched applies the feed's rules and stores what is new. itemsHash
// is nil when the item set is the same as last time: then nothing can be
// inserted (every hash is already in entries or feed_entry_dedup) and the
// stored hash needs no update.
func (r *FeedRefresher) storeFetched(ctx context.Context, feed storage.Feed, subs []storage.Subscription, fetched []bridge.Entry) (inserted int, insertedEntries []storage.Entry, fresh int, itemsHash *string, err error) {
	entries := reader.ApplyFeedRules(fetched, feed.BlockedRules, feed.KeepRules)
	entries = reader.ApplyURLRewriteRules(entries, feed.RewriteRules)
	h := itemSetHash(entries)
	if h != "" && h == feed.ItemsHash {
		return 0, nil, 0, nil, nil
	}
	if r.feedUsesHashOnlyStorage(feed) {
		inserted, insertedEntries, fresh, err = r.processEntriesDedupOnly(ctx, feed, subs, entries)
	} else {
		inserted, insertedEntries, err = r.Entries.CreateEntries(ctx, feed.ID, entries)
		fresh = inserted
	}
	if err != nil {
		return 0, nil, 0, nil, err
	}
	return inserted, insertedEntries, fresh, &h, nil
}

// nextCheckAfterPoll: the feed's interval, raised by the adaptive schedule
// and by the source's own freshness hint (max-age/Expires/<ttl>). Hints
// only ever postpone a poll, never bring it forward, and stay inside the
// configured maximum.
func (r *FeedRefresher) nextCheckAfterPoll(ctx context.Context, feed storage.Feed, now, minNextCheck time.Time) time.Time {
	lo, hi := r.pollBounds()
	next := storage.FeedNextCheckAt(now, feed.IntervalMinutes, lo, hi)
	if adaptive := r.adaptiveNextCheck(ctx, feed, now, lo, hi); adaptive.After(next) {
		next = adaptive
	}
	if minNextCheck.After(next) {
		next = minNextCheck
		if limit := now.Add(hi); next.After(limit) {
			next = limit
		}
	}
	return next
}

func (r *FeedRefresher) feedSubscribers(ctx context.Context, feedID int64) []storage.Subscription {
	if r.Subscribers == nil {
		return nil
	}
	subs, err := r.Subscribers.ListFeedSubscribers(ctx, feedID)
	if err != nil {
		r.logger().Error("list feed subscribers failed", "feed_id", feedID, "err", err)
		return nil
	}
	return subs
}

// movedFeedURL returns the permanent-redirect destination worth storing:
// different from the current URL and still a plain http(s) feed address (a
// hop onto a bridge domain would change the feed type under the user).
func movedFeedURL(feed storage.Feed, newURL string) string {
	newURL = strings.TrimSpace(newURL)
	if newURL == "" || newURL == feed.FeedURL {
		return ""
	}
	switch reader.NormalizeFeedType(feed.FeedType) {
	case "", reader.FeedTypeRSS, reader.FeedTypeAtom, reader.FeedTypeJSON:
	default:
		return ""
	}
	if reader.DetectFeedTypeFromURL(newURL) != reader.FeedTypeRSS {
		return ""
	}
	return newURL
}

func bridgeStateJSON(st reader.BridgeState) []byte {
	if st.IsEmpty() {
		return nil
	}
	b, err := json.Marshal(st)
	if err != nil {
		return nil
	}
	return b
}

// applyFiltersBestEffort runs every subscriber's filters over the new
// entries and enqueues their subscription webhooks. The `query` rules are
// evaluated once per distinct pattern set, filter actions per subscriber.
func (r *FeedRefresher) applyFiltersBestEffort(ctx context.Context, feed storage.Feed, subs []storage.Subscription, entries []storage.Entry) {
	if len(entries) == 0 {
		return
	}
	for _, sub := range subs {
		r.applySubscriberFilters(ctx, feed, sub, entries)
	}
}

func (r *FeedRefresher) applySubscriberFilters(ctx context.Context, feed storage.Feed, sub storage.Subscription, entries []storage.Entry) {
	var filters []storage.Filter
	if r.Filters != nil && r.Matches != nil && r.Engine != nil {
		filters, _ = r.listEnabledFiltersCached(ctx, sub.UserID)
	}
	if len(filters) == 0 {
		r.enqueueSubscriptionWebhookBestEffort(ctx, sub, entries)
		return
	}
	matchCtx := filter.MatchContext{FeedID: feed.ID, CategoryID: feed.CategoryID}
	queryHits := r.queryHitsBestEffort(ctx, feed.ID, filters, entries)
	now := time.Now().UTC()
	for i, e := range entries {
		start := time.Now()
		if queryHits != nil {
			matchCtx.QueryHits = queryHits[i]
		}
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
			r.applyFilterActions(ctx, sub.UserID, e, filters, matchedFilterIDs)
		}
		if sub.WebhookID != nil && r.WebhookLogs != nil {
			_ = r.WebhookLogs.EnqueueWebhookLogs(ctx, []int64{*sub.WebhookID}, e.ID)
		}
	}
}

// adaptiveNextCheck returns the stretched next poll time for feeds with
// adaptive_interval, or the zero time when the feature does not apply. The
// weekly item count is one indexed read; on error the base interval stands.
func (r *FeedRefresher) adaptiveNextCheck(ctx context.Context, feed storage.Feed, now time.Time, min, max time.Duration) time.Time {
	if !feed.AdaptiveInterval || r.Activity == nil || r.AdaptiveMaxInterval <= 0 {
		return time.Time{}
	}
	ceiling := r.AdaptiveMaxInterval
	if ceiling > max {
		ceiling = max
	}
	base := storage.FeedNextCheckAt(now, feed.IntervalMinutes, min, max).Sub(now)
	if ceiling <= base {
		return time.Time{}
	}
	items, err := r.Activity.CountFeedItemsSince(ctx, feed.ID, now.Add(-storage.AdaptivePollWindow))
	if err != nil {
		r.logger().Warn("count feed items failed; adaptive interval skipped", "feed_id", feed.ID, "err", err)
		return time.Time{}
	}
	var silentFor time.Duration
	if items == 0 {
		lastItem := feed.CreatedAt
		if feed.LastEntryAt != nil {
			lastItem = *feed.LastEntryAt
		}
		if !lastItem.IsZero() {
			silentFor = now.Sub(lastItem)
		}
	}
	return now.Add(storage.AdaptivePollInterval(base, items, silentFor, ceiling, max))
}

// queryHitsBestEffort runs one SQL for all `query` rules of the batch; on
// failure those rules are treated as non-matching (regex rules still apply)
// and the error is logged once per poll rather than per entry.
func (r *FeedRefresher) queryHitsBestEffort(ctx context.Context, feedID int64, filters []storage.Filter, entries []storage.Entry) []map[string]bool {
	items := make([]storage.QueryMatchItem, len(entries))
	for i, e := range entries {
		items[i] = filter.QueryItemFromEntry(e)
	}
	hits, err := filter.QueryHits(ctx, r.Queries, filters, items)
	if err != nil && r.Log != nil {
		r.Log.Warn("query rules skipped for this refresh", "feed_id", feedID, "err", err)
	}
	return hits
}

func (r *FeedRefresher) enqueueSubscriptionWebhookBestEffort(ctx context.Context, sub storage.Subscription, entries []storage.Entry) {
	if sub.WebhookID == nil || r.WebhookLogs == nil || len(entries) == 0 {
		return
	}
	for _, e := range entries {
		_ = r.WebhookLogs.EnqueueWebhookLogs(ctx, []int64{*sub.WebhookID}, e.ID)
	}
}

// itemSetHash fingerprints the set of item hashes after the feed's rules;
// order and content changes do not count, only which items are present.
func itemSetHash(entries []bridge.Entry) string {
	if len(entries) == 0 {
		return ""
	}
	hashes := make([]string, 0, len(entries))
	for _, e := range entries {
		hashes = append(hashes, e.Hash)
	}
	sort.Strings(hashes)
	d := xxhash.New()
	for _, h := range hashes {
		_, _ = d.WriteString(h)
		_, _ = d.Write([]byte{0})
	}
	return strconv.FormatUint(d.Sum64(), 16)
}
