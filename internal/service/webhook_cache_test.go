package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"rssam/internal/storage"
)

type countingWebhookStore struct {
	storage.WebhookStore // nil: only ListEnabledWebhooks is used
	calls                int
	webhooks             []storage.Webhook
	err                  error
}

func (c *countingWebhookStore) ListEnabledWebhooks(_ context.Context, _ int64, _ int) ([]storage.Webhook, error) {
	c.calls++
	return c.webhooks, c.err
}

// Enabled webhooks are cached per user for enabledWebhookCacheTTL and
// invalidated explicitly when webhooks change.
func TestListLegacyFilterWebhooksCached(t *testing.T) {
	fid := int64(5)
	store := &countingWebhookStore{webhooks: []storage.Webhook{
		{ID: 1, FilterID: &fid},
		{ID: 2}, // feed-level webhook, not legacy
	}}
	r := &FeedRefresher{Webhooks: store}
	ctx := context.Background()

	for range 10 {
		got, err := r.listLegacyFilterWebhooksCached(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ID != 1 {
			t.Fatalf("want only the filter-bound webhook, got %+v", got)
		}
	}
	if store.calls != 1 {
		t.Fatalf("store queried %d times for 10 refreshes, want 1", store.calls)
	}
	// Another user is a separate cache entry.
	if _, err := r.listLegacyFilterWebhooksCached(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if store.calls != 2 {
		t.Fatalf("per-user cache: calls=%d want 2", store.calls)
	}
	// Invalidation for one user reloads only that user.
	r.InvalidateWebhookCache(1)
	_, _ = r.listLegacyFilterWebhooksCached(ctx, 1)
	_, _ = r.listLegacyFilterWebhooksCached(ctx, 2)
	if store.calls != 3 {
		t.Fatalf("after invalidate(1): calls=%d want 3", store.calls)
	}
	// Invalidate all.
	r.InvalidateWebhookCache(0)
	_, _ = r.listLegacyFilterWebhooksCached(ctx, 1)
	_, _ = r.listLegacyFilterWebhooksCached(ctx, 2)
	if store.calls != 5 {
		t.Fatalf("after invalidate(0): calls=%d want 5", store.calls)
	}
	// TTL expiry reloads.
	r.webhookCache.mu.Lock()
	it := r.webhookCache.items[1]
	it.at = time.Now().Add(-2 * enabledWebhookCacheTTL)
	r.webhookCache.items[1] = it
	r.webhookCache.mu.Unlock()
	_, _ = r.listLegacyFilterWebhooksCached(ctx, 1)
	if store.calls != 6 {
		t.Fatalf("after ttl expiry: calls=%d want 6", store.calls)
	}
}

func TestListLegacyFilterWebhooksCached_ErrorNotCached(t *testing.T) {
	store := &countingWebhookStore{err: errors.New("db down")}
	r := &FeedRefresher{Webhooks: store}
	if _, err := r.listLegacyFilterWebhooksCached(context.Background(), 1); err == nil {
		t.Fatal("expected error to be surfaced")
	}
	store.err = nil
	fid := int64(1)
	store.webhooks = []storage.Webhook{{ID: 9, FilterID: &fid}}
	got, err := r.listLegacyFilterWebhooksCached(context.Background(), 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("recovered call: got %v err %v", got, err)
	}
	if store.calls != 2 {
		t.Fatalf("errors must not be cached: calls=%d", store.calls)
	}
	// nil refresher / nil store are safe.
	var nilR *FeedRefresher
	nilR.InvalidateWebhookCache(1)
	if got, err := (&FeedRefresher{}).listLegacyFilterWebhooksCached(context.Background(), 1); got != nil || err != nil {
		t.Fatalf("nil store: %v %v", got, err)
	}
}
