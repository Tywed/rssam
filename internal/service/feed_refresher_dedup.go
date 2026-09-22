package service

import (
	"context"
	"fmt"
	"strings"

	"rssam/internal/filter"
	"rssam/internal/storage"
)

func entryFromCreateParams(feedID int64, p storage.CreateEntryParams) storage.Entry {
	return storage.Entry{
		FeedID:      feedID,
		Title:       p.Title,
		URL:         p.URL,
		Content:     p.Content,
		Author:      p.Author,
		PublishedAt: p.PublishedAt,
		Hash:        p.Hash,
		Status:      storage.EntryStatusUnread,
	}
}

// processEntriesDedupOnly is the hash-only path: a new item becomes an
// entry only when it matches a filter of at least one subscriber (the
// entry is then shared by all of them and the actions run in
// applyFiltersBestEffort); otherwise only its hash is kept.
func (r *FeedRefresher) processEntriesDedupOnly(
	ctx context.Context,
	feed storage.Feed,
	subs []storage.Subscription,
	entries []storage.CreateEntryParams,
) (inserted int, insertedEntries []storage.Entry, fresh int, err error) {
	if r.Dedup == nil {
		return 0, nil, 0, fmt.Errorf("dedup store is not configured")
	}
	if len(entries) == 0 {
		return 0, nil, 0, nil
	}
	feedID := feed.ID

	hashes := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.TrimSpace(e.Hash) != "" {
			hashes = append(hashes, e.Hash)
		}
	}
	known, err := r.Dedup.FilterKnownEntryHashes(ctx, feedID, hashes)
	if err != nil {
		return 0, nil, 0, err
	}

	type subscriberFilters struct {
		filters  []storage.Filter
		matchCtx filter.MatchContext
	}
	var perSub []subscriberFilters
	if r.Filters != nil && r.Engine != nil {
		for _, sub := range subs {
			filters, err := r.listEnabledFiltersCached(ctx, sub.UserID)
			if err != nil {
				return 0, nil, 0, fmt.Errorf("list filters: %w", err)
			}
			if len(filters) == 0 {
				continue
			}
			perSub = append(perSub, subscriberFilters{
				filters:  filters,
				matchCtx: filter.MatchContext{FeedID: feedID, CategoryID: sub.CategoryID},
			})
		}
	}

	var toDedup []storage.FeedEntryDedupParams
	freshParams := make([]storage.CreateEntryParams, 0, len(entries))
	freshEntries := make([]storage.Entry, 0, len(entries))
	for _, p := range entries {
		if p.Hash == "" {
			continue
		}
		if _, ok := known[p.Hash]; ok {
			continue
		}
		known[p.Hash] = struct{}{}
		freshParams = append(freshParams, p)
		freshEntries = append(freshEntries, entryFromCreateParams(feedID, p))
	}
	fresh = len(freshEntries)
	queryHits := make([][]map[string]bool, len(perSub))
	for i, sf := range perSub {
		queryHits[i] = r.queryHitsBestEffort(ctx, feedID, sf.filters, freshEntries)
	}

	for i, e := range freshEntries {
		p := freshParams[i]
		wanted := false
		for j, sf := range perSub {
			matchCtx := sf.matchCtx
			if queryHits[j] != nil {
				matchCtx.QueryHits = queryHits[j][i]
			}
			matches, matchErr := r.Engine.MatchEntryWithContext(e, matchCtx, sf.filters)
			if matchErr == nil && len(matches) > 0 {
				wanted = true
				break
			}
		}
		if !wanted {
			toDedup = append(toDedup, storage.FeedEntryDedupParams{Hash: p.Hash, URL: p.URL})
			continue
		}

		n, created, createErr := r.Entries.CreateEntries(ctx, feedID, []storage.CreateEntryParams{p})
		if createErr != nil {
			return inserted, insertedEntries, fresh, createErr
		}
		inserted += n
		insertedEntries = append(insertedEntries, created...)
	}

	if len(toDedup) > 0 {
		if _, err := r.Dedup.RecordFeedEntryDedup(ctx, feedID, toDedup); err != nil {
			return inserted, insertedEntries, fresh, err
		}
	}
	return inserted, insertedEntries, fresh, nil
}
