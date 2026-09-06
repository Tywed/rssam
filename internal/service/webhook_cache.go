package service

import (
	"context"
	"sync"
	"time"

	"rssam/internal/storage"
)

// Enabled webhooks are needed on every feed refresh that has filters (to
// route legacy filter-bound webhooks), which was one extra query per poll.
// They change rarely, so they are cached per user like enabled filters.
const enabledWebhookCacheTTL = enabledFilterCacheTTL

type enabledWebhookCache struct {
	mu    sync.Mutex
	ttl   time.Duration
	items map[int64]enabledWebhookCacheItem
}

type enabledWebhookCacheItem struct {
	at       time.Time
	webhooks []storage.Webhook
}

func (r *FeedRefresher) initWebhookCache() {
	r.webhookCacheOnce.Do(func() {
		r.webhookCache = &enabledWebhookCache{
			ttl:   enabledWebhookCacheTTL,
			items: make(map[int64]enabledWebhookCacheItem),
		}
	})
}

// InvalidateWebhookCache drops cached enabled webhooks for userID (0 = all).
// Call it after creating, updating, deleting or (un)pausing a webhook.
func (r *FeedRefresher) InvalidateWebhookCache(userID int64) {
	if r == nil {
		return
	}
	r.initWebhookCache()
	r.webhookCache.mu.Lock()
	defer r.webhookCache.mu.Unlock()
	if userID <= 0 {
		r.webhookCache.items = make(map[int64]enabledWebhookCacheItem)
		return
	}
	delete(r.webhookCache.items, userID)
}

// listLegacyFilterWebhooksCached returns the user's enabled webhooks that are
// bound to a filter (legacy routing), from cache when fresh.
func (r *FeedRefresher) listLegacyFilterWebhooksCached(ctx context.Context, userID int64) ([]storage.Webhook, error) {
	if r.Webhooks == nil {
		return nil, nil
	}
	r.initWebhookCache()
	r.webhookCache.mu.Lock()
	if it, ok := r.webhookCache.items[userID]; ok && time.Since(it.at) < r.webhookCache.ttl {
		webhooks := it.webhooks
		r.webhookCache.mu.Unlock()
		return webhooks, nil
	}
	delete(r.webhookCache.items, userID)
	r.webhookCache.mu.Unlock()

	all, err := r.Webhooks.ListEnabledWebhooks(ctx, userID, 1000)
	if err != nil {
		return nil, err
	}
	var legacy []storage.Webhook
	for _, wh := range all {
		if wh.FilterID != nil {
			legacy = append(legacy, wh)
		}
	}
	r.webhookCache.mu.Lock()
	r.webhookCache.items[userID] = enabledWebhookCacheItem{at: time.Now(), webhooks: legacy}
	r.webhookCache.mu.Unlock()
	return legacy, nil
}
