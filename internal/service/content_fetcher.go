package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"rssam/internal/scraper"
	"rssam/internal/storage"
)

var (
	ErrScrapeEntry   = errors.New("scrape entry")
	ErrEmptyEntryURL = errors.New("entry url is empty")
)

// ContentFetcher loads full article HTML for an entry.
type ContentFetcher struct {
	Feeds   storage.FeedStore
	Entries storage.EntryStore
	Scraper *scraper.Fetcher
}

func (c *ContentFetcher) FetchEntryContent(ctx context.Context, userID, entryID int64) (storage.Entry, error) {
	if c.Feeds == nil || c.Entries == nil || c.Scraper == nil {
		return storage.Entry{}, errors.New("content fetcher is not configured")
	}

	entry, err := c.Entries.GetEntry(ctx, userID, entryID)
	if err != nil {
		return storage.Entry{}, fmt.Errorf("get entry: %w", err)
	}
	if strings.TrimSpace(entry.URL) == "" {
		return storage.Entry{}, ErrEmptyEntryURL
	}

	feed, err := c.Feeds.GetFeed(ctx, userID, entry.FeedID)
	if err != nil {
		return storage.Entry{}, fmt.Errorf("get feed: %w", err)
	}

	content, err := c.Scraper.ScrapePage(ctx, entry.URL, feed.ScraperRules, feed.RewriteRules, feed.UserAgent, feed.FetchViaProxy)
	if err != nil {
		return storage.Entry{}, fmt.Errorf("%w: %w", ErrScrapeEntry, err)
	}

	original := entry.OriginalContent
	if !entry.ContentFetched && original == "" {
		original = entry.Content
	}

	return c.Entries.UpdateEntryContent(ctx, userID, storage.UpdateEntryContentParams{
		ID:              entry.ID,
		Content:         content,
		OriginalContent: original,
		ContentFetched:  true,
	})
}
