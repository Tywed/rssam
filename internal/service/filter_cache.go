package service

import (
	"context"
	"sync"
	"time"

	"rssam/internal/storage"
)

const enabledFilterCacheTTL = 5 * time.Second

type enabledFilterCache struct {
	mu    sync.Mutex
	ttl   time.Duration
	items map[int64]enabledFilterCacheItem
}

type enabledFilterCacheItem struct {
	at      time.Time
	filters []storage.Filter
}

func (r *FeedRefresher) initFilterCache() {
	r.filterCacheOnce.Do(func() {
		r.filterCache = &enabledFilterCache{
			ttl:   enabledFilterCacheTTL,
			items: make(map[int64]enabledFilterCacheItem),
		}
	})
}

// InvalidateFilterCache drops cached enabled filters for userID (0 = all).
func (r *FeedRefresher) InvalidateFilterCache(userID int64) {
	if r == nil {
		return
	}
	r.initFilterCache()
	r.filterCache.mu.Lock()
	defer r.filterCache.mu.Unlock()
	if userID <= 0 {
		r.filterCache.items = make(map[int64]enabledFilterCacheItem)
		return
	}
	delete(r.filterCache.items, userID)
}

func (r *FeedRefresher) listEnabledFiltersCached(ctx context.Context, userID int64) ([]storage.Filter, error) {
	if r.Filters == nil {
		return nil, nil
	}
	r.initFilterCache()
	r.filterCache.mu.Lock()
	if it, ok := r.filterCache.items[userID]; ok && time.Since(it.at) < r.filterCache.ttl {
		filters := it.filters
		r.filterCache.mu.Unlock()
		return filters, nil
	}
	delete(r.filterCache.items, userID)
	r.filterCache.mu.Unlock()

	filters, err := r.Filters.ListEnabledFilters(ctx, userID, 1000)
	if err != nil {
		return nil, err
	}
	r.filterCache.mu.Lock()
	r.filterCache.items[userID] = enabledFilterCacheItem{at: time.Now(), filters: filters}
	r.filterCache.mu.Unlock()
	return filters, nil
}
