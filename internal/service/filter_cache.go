package service

import (
	"context"
	"time"

	"rssam/internal/storage"
)

const enabledFilterCacheTTL = 5 * time.Second

// InvalidateFilterCache drops cached enabled filters for userID (0 = all).
func (r *FeedRefresher) InvalidateFilterCache(userID int64) {
	if r == nil {
		return
	}
	r.filterCache.invalidate(userID)
}

func (r *FeedRefresher) listEnabledFiltersCached(ctx context.Context, userID int64) ([]storage.Filter, error) {
	if r.Filters == nil {
		return nil, nil
	}
	if filters, ok := r.filterCache.get(userID, enabledFilterCacheTTL); ok {
		return filters, nil
	}
	filters, err := r.Filters.ListEnabledFilters(ctx, userID, 1000)
	if err != nil {
		return nil, err
	}
	r.filterCache.put(userID, filters)
	return filters, nil
}
