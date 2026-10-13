package service

import (
	"context"
	"errors"
	"time"

	bridge "rssam/internal/bridge/v1"
	"rssam/internal/reader"
	"rssam/internal/storage"
)

// ErrInboundFeed: polling was asked of a feed that only receives deliveries.
var ErrInboundFeed = errors.New("inbound feed is not polled")

// Ingest stores entries delivered to an inbound feed exactly as a poll
// would: sanitized, filtered by the feed's rules, fanned out to the
// subscribers, run through their filters and webhooks, logged in
// feed_poll_log. next_check_at is left alone (an inbound feed is never due).
func (r *FeedRefresher) Ingest(ctx context.Context, feed storage.Feed, entries []bridge.Entry) (inserted int, err error) {
	if r.Feeds == nil || r.Entries == nil {
		return 0, errors.New("feed refresher is not configured")
	}
	if feed.FeedType != reader.FeedTypeInbound {
		return 0, ErrInboundFeed
	}
	started := time.Now()
	now := started.UTC()
	subs := r.feedSubscribers(ctx, feed.ID)
	reader.SanitizeEntries(entries)
	meta := storage.UpdateFeedRefreshMetaParams{
		ID:            feed.ID,
		ETag:          feed.ETag,
		LastModified:  feed.LastModified,
		LastCheckedAt: now,
		BridgeState:   feed.BridgeState,
	}
	inserted, insertedEntries, metaStored, err := r.storeFetched(ctx, feed, subs, entries, &meta)
	if err != nil {
		r.recordPoll(ctx, feed.ID, now, started, 0, err)
		return 0, err
	}
	r.applyFiltersBestEffort(ctx, feed, subs, insertedEntries)
	if r.Realtime != nil && len(insertedEntries) > 0 {
		r.Realtime.PublishNewEntries(ctx, feed, subs, insertedEntries)
	}
	if !metaStored {
		if err := r.Feeds.UpdateFeedRefreshMeta(ctx, meta); err != nil {
			r.logger().Error("update feed refresh meta failed", "feed_id", feed.ID, "err", err)
		}
	}
	r.recordPoll(ctx, feed.ID, now, started, inserted, nil)
	return inserted, nil
}
