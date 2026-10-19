package service

import (
	"context"
	"errors"
	"sync"
	"time"

	bridge "rssam/internal/bridge/v1"
	"rssam/internal/reader"
	"rssam/internal/storage"
)

// ErrInboundFeed: polling was asked of a feed that only receives deliveries.
var ErrInboundFeed = errors.New("inbound feed is not polled")

// inboundLocks serializes deliveries to the same inbound feed, as the
// scheduler does polls: a sender's worker pool delivers several entries at
// once, and concurrent ingests of one feed would each log a poll row and
// race on the item-set hash.
var inboundLocks [64]sync.Mutex

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
	mu := &inboundLocks[uint64(feed.ID)%uint64(len(inboundLocks))]
	mu.Lock()
	defer mu.Unlock()
	started := time.Now()
	now := started.UTC()
	subs := r.feedSubscribers(ctx, feed.ID)
	meta := storage.UpdateFeedRefreshMetaParams{
		ID:            feed.ID,
		ETag:          feed.ETag,
		LastModified:  feed.LastModified,
		LastCheckedAt: now,
		BridgeState:   feed.BridgeState,
	}
	inserted, insertedEntries, metaStored, err := r.storeFetched(ctx, feed, subs, entries, &meta)
	if err != nil {
		r.recordPoll(ctx, feed, now, started, 0, err)
		return 0, err
	}
	r.afterStore(ctx, feed, subs, insertedEntries)
	if !metaStored {
		if err := r.Feeds.UpdateFeedRefreshMeta(ctx, meta); err != nil {
			r.logger().Error("update feed refresh meta failed", "feed_id", feed.ID, "err", err)
		}
	}
	r.recordPoll(ctx, feed, now, started, inserted, nil)
	return inserted, nil
}
