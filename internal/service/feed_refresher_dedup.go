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
		Status:      p.Status,
	}
}

func (r *FeedRefresher) processEntriesDedupOnly(
	ctx context.Context,
	feed storage.Feed,
	entries []storage.CreateEntryParams,
) (inserted int, insertedEntries []storage.Entry, err error) {
	if r.Dedup == nil {
		return 0, nil, fmt.Errorf("dedup store is not configured")
	}
	if len(entries) == 0 {
		return 0, nil, nil
	}
	feedID := feed.ID
	userID := feed.UserID

	hashes := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.TrimSpace(e.Hash) != "" {
			hashes = append(hashes, e.Hash)
		}
	}
	known, err := r.Dedup.FilterKnownEntryHashes(ctx, feedID, hashes)
	if err != nil {
		return 0, nil, err
	}

	var filters []storage.Filter
	if r.Filters != nil && r.Engine != nil {
		filters, err = r.listEnabledFiltersCached(ctx, userID)
		if err != nil {
			return 0, nil, fmt.Errorf("list filters: %w", err)
		}
	}
	matchCtx := filter.MatchContext{FeedID: feedID, CategoryID: feed.CategoryID}

	var toDedup []storage.FeedEntryDedupParams
	for _, p := range entries {
		if p.Hash == "" {
			continue
		}
		if _, ok := known[p.Hash]; ok {
			continue
		}
		known[p.Hash] = struct{}{}

		var matches []filter.Match
		if r.Engine != nil && len(filters) > 0 {
			var matchErr error
			matches, matchErr = r.Engine.MatchEntryWithContext(entryFromCreateParams(feedID, p), matchCtx, filters)
			if matchErr != nil {
				continue
			}
		}
		if len(matches) == 0 {
			toDedup = append(toDedup, storage.FeedEntryDedupParams{Hash: p.Hash, URL: p.URL})
			continue
		}

		n, created, createErr := r.Entries.CreateEntries(ctx, feedID, []storage.CreateEntryParams{p})
		if createErr != nil {
			return inserted, insertedEntries, createErr
		}
		inserted += n
		insertedEntries = append(insertedEntries, created...)
	}

	if len(toDedup) > 0 {
		if _, err := r.Dedup.RecordFeedEntryDedup(ctx, feedID, toDedup); err != nil {
			return inserted, insertedEntries, err
		}
	}
	return inserted, insertedEntries, nil
}
