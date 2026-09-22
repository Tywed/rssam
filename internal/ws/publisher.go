package ws

import (
	"context"
	"log/slog"
	"strconv"

	"rssam/internal/storage"
)

type UnreadCounter interface {
	CountUnreadByFeed(ctx context.Context, userID, feedID int64) (int, error)
	CountUnreadByCategory(ctx context.Context, userID, categoryID int64) (int, error)
	CountUnreadGlobalForUser(ctx context.Context, userID int64) (int, error)
}

// FeedSubscribers resolves who receives a feed's status events.
type FeedSubscribers interface {
	ListFeedSubscribers(ctx context.Context, feedID int64) ([]storage.Subscription, error)
}

type Publisher struct {
	log     *slog.Logger
	hub     *Hub
	entries UnreadCounter
	subs    FeedSubscribers
}

func NewPublisher(log *slog.Logger, hub *Hub, entries UnreadCounter, subs FeedSubscribers) *Publisher {
	if log == nil {
		log = slog.Default()
	}
	return &Publisher{
		log:     log,
		hub:     hub,
		entries: entries,
		subs:    subs,
	}
}

func feedChannels(feedID int64, categoryID *int64) []string {
	channels := []string{ChannelAll, "feed:" + strconv.FormatInt(feedID, 10)}
	if categoryID != nil && *categoryID > 0 {
		channels = append(channels, "category:"+strconv.FormatInt(*categoryID, 10))
	}
	return channels
}

func (p *Publisher) PublishNewEntries(ctx context.Context, feed storage.Feed, subs []storage.Subscription, newEntries []storage.Entry) {
	if p == nil || p.hub == nil || len(newEntries) == 0 {
		return
	}
	for _, sub := range subs {
		channels := feedChannels(feed.ID, sub.CategoryID)
		for _, entry := range newEntries {
			p.hub.PublishToUser(sub.UserID, channels, Envelope{
				Event: "new_entry",
				Data: map[string]any{
					"entry": newEntryPayload(feed, sub.CategoryID, entry),
				},
			})
		}
		p.publishUnreadCounters(ctx, feed, sub)
	}
}

// newEntryPayload is the minimal, explicitly whitelisted projection of an entry
// pushed over WebSocket. storage.Entry has no json tags; publishing it directly
// would expose every column (content, raw HTML, hashes) with Go field names.
func newEntryPayload(feed storage.Feed, categoryID *int64, e storage.Entry) map[string]any {
	return map[string]any{
		"id":           e.ID,
		"feed_id":      e.FeedID,
		"category_id":  categoryID,
		"feed_title":   feed.Title,
		"title":        e.Title,
		"url":          e.URL,
		"author":       e.Author,
		"published_at": e.PublishedAt,
		"created_at":   e.CreatedAt,
	}
}

// PublishFeedStatusChanged tells every subscriber; the subscriber list is
// read here because the status path (worker, admin actions) has no
// subscriptions at hand.
func (p *Publisher) PublishFeedStatusChanged(feed storage.Feed, err error) {
	if p == nil || p.hub == nil || p.subs == nil {
		return
	}
	subs, lerr := p.subs.ListFeedSubscribers(context.Background(), feed.ID)
	if lerr != nil {
		p.log.Warn("list feed subscribers failed", "feed_id", feed.ID, "err", lerr)
		return
	}
	msg := feed.LastError
	if err != nil {
		msg = err.Error()
	}
	for _, sub := range subs {
		p.hub.PublishToUser(sub.UserID, feedChannels(feed.ID, sub.CategoryID), Envelope{
			Event: "feed_status_changed",
			Data: map[string]any{
				"feed_id":               feed.ID,
				"success":               err == nil,
				"parsing_error_count":   feed.ParsingErrorCount,
				"parsing_error_message": msg,
				"poll_paused":           feed.PollPaused,
				"manual_paused":         feed.ManualPaused,
				"last_checked_at":       feed.LastCheckedAt,
				"next_check_at":         feed.NextCheckAt,
			},
		})
	}
}

func (p *Publisher) publishUnreadCounters(ctx context.Context, feed storage.Feed, sub storage.Subscription) {
	if p.entries == nil {
		return
	}
	feedUnread, err := p.entries.CountUnreadByFeed(ctx, sub.UserID, feed.ID)
	if err == nil {
		p.hub.PublishToUser(sub.UserID, []string{ChannelAll, "feed:" + strconv.FormatInt(feed.ID, 10)}, Envelope{
			Event: "unread_count_changed",
			Data: map[string]any{
				"scope":        "feed",
				"feed_id":      feed.ID,
				"unread_count": feedUnread,
			},
		})
	} else {
		p.log.Warn("count feed unread failed", "feed_id", feed.ID, "err", err)
	}

	if sub.CategoryID != nil && *sub.CategoryID > 0 {
		categoryUnread, err := p.entries.CountUnreadByCategory(ctx, sub.UserID, *sub.CategoryID)
		if err == nil {
			p.hub.PublishToUser(sub.UserID, []string{ChannelAll, "category:" + strconv.FormatInt(*sub.CategoryID, 10)}, Envelope{
				Event: "unread_count_changed",
				Data: map[string]any{
					"scope":        "category",
					"category_id":  *sub.CategoryID,
					"unread_count": categoryUnread,
				},
			})
		} else {
			p.log.Warn("count category unread failed", "category_id", *sub.CategoryID, "err", err)
		}
	}

	globalUnread, err := p.entries.CountUnreadGlobalForUser(ctx, sub.UserID)
	if err == nil {
		p.hub.PublishToUser(sub.UserID, []string{ChannelAll}, Envelope{
			Event: "unread_count_changed",
			Data: map[string]any{
				"scope":        "global",
				"unread_count": globalUnread,
			},
		})
	} else {
		p.log.Warn("count global unread failed", "err", err)
	}
}
