package ws

import (
	"context"
	"log/slog"
	"strconv"

	"rssam/internal/storage"
)

type UnreadCounter interface {
	CountUnreadByFeed(ctx context.Context, feedID int64) (int, error)
	CountUnreadByCategory(ctx context.Context, categoryID int64) (int, error)
	CountUnreadGlobalForUser(ctx context.Context, userID int64) (int, error)
}

type Publisher struct {
	log     *slog.Logger
	hub     *Hub
	entries UnreadCounter
}

func NewPublisher(log *slog.Logger, hub *Hub, entries UnreadCounter) *Publisher {
	if log == nil {
		log = slog.Default()
	}
	return &Publisher{
		log:     log,
		hub:     hub,
		entries: entries,
	}
}

func (p *Publisher) PublishNewEntries(ctx context.Context, feed storage.Feed, newEntries []storage.Entry) {
	if p == nil || p.hub == nil || len(newEntries) == 0 {
		return
	}

	channels := []string{ChannelAll, "feed:" + strconv.FormatInt(feed.ID, 10)}
	if feed.CategoryID != nil && *feed.CategoryID > 0 {
		channels = append(channels, "category:"+strconv.FormatInt(*feed.CategoryID, 10))
	}
	for _, entry := range newEntries {
		p.hub.PublishToUser(feed.UserID, channels, Envelope{
			Event: "new_entry",
			Data: map[string]any{
				"entry": newEntryPayload(feed, entry),
			},
		})
	}
	p.publishUnreadCounters(ctx, feed)
}

// newEntryPayload is the minimal, explicitly whitelisted projection of an entry
// pushed over WebSocket. storage.Entry has no json tags; publishing it directly
// would expose every column (content, raw HTML, hashes) with Go field names.
func newEntryPayload(feed storage.Feed, e storage.Entry) map[string]any {
	return map[string]any{
		"id":           e.ID,
		"feed_id":      e.FeedID,
		"category_id":  feed.CategoryID,
		"feed_title":   feed.Title,
		"title":        e.Title,
		"url":          e.URL,
		"author":       e.Author,
		"published_at": e.PublishedAt,
		"created_at":   e.CreatedAt,
	}
}

func (p *Publisher) PublishFeedStatusChanged(feed storage.Feed, err error) {
	if p == nil || p.hub == nil {
		return
	}
	channels := []string{ChannelAll, "feed:" + strconv.FormatInt(feed.ID, 10)}
	if feed.CategoryID != nil && *feed.CategoryID > 0 {
		channels = append(channels, "category:"+strconv.FormatInt(*feed.CategoryID, 10))
	}
	msg := feed.LastError
	if err != nil {
		msg = err.Error()
	}
	p.hub.PublishToUser(feed.UserID, channels, Envelope{
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

func (p *Publisher) publishUnreadCounters(ctx context.Context, feed storage.Feed) {
	if p.entries == nil {
		return
	}
	feedUnread, err := p.entries.CountUnreadByFeed(ctx, feed.ID)
	if err == nil {
		p.hub.PublishToUser(feed.UserID, []string{ChannelAll, "feed:" + strconv.FormatInt(feed.ID, 10)}, Envelope{
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

	if feed.CategoryID != nil && *feed.CategoryID > 0 {
		categoryUnread, err := p.entries.CountUnreadByCategory(ctx, *feed.CategoryID)
		if err == nil {
			p.hub.PublishToUser(feed.UserID, []string{ChannelAll, "category:" + strconv.FormatInt(*feed.CategoryID, 10)}, Envelope{
				Event: "unread_count_changed",
				Data: map[string]any{
					"scope":        "category",
					"category_id":  *feed.CategoryID,
					"unread_count": categoryUnread,
				},
			})
		} else {
			p.log.Warn("count category unread failed", "category_id", *feed.CategoryID, "err", err)
		}
	}

	globalUnread, err := p.entries.CountUnreadGlobalForUser(ctx, feed.UserID)
	if err == nil {
		p.hub.PublishToUser(feed.UserID, []string{ChannelAll}, Envelope{
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
