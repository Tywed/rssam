package service

import (
	"context"

	"rssam/internal/storage"
)

// Enabled webhooks are needed on every feed refresh that has filters (to
// route legacy filter-bound webhooks). They change rarely, so they are cached
// per user like enabled filters instead of being queried on every poll.
const enabledWebhookCacheTTL = enabledFilterCacheTTL

// InvalidateWebhookCache drops cached enabled webhooks for userID (0 = all).
// Call it after creating, updating, deleting or (un)pausing a webhook.
func (r *FeedRefresher) InvalidateWebhookCache(userID int64) {
	if r == nil {
		return
	}
	r.webhookCache.invalidate(userID)
}

// listLegacyFilterWebhooksCached returns the user's enabled webhooks that are
// bound to a filter (legacy routing), from cache when fresh.
func (r *FeedRefresher) listLegacyFilterWebhooksCached(ctx context.Context, userID int64) ([]storage.Webhook, error) {
	if r.Webhooks == nil {
		return nil, nil
	}
	if webhooks, ok := r.webhookCache.get(userID, enabledWebhookCacheTTL); ok {
		return webhooks, nil
	}
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
	r.webhookCache.put(userID, legacy)
	return legacy, nil
}
