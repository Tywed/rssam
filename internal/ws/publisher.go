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
	CountUnreadGlobal(ctx context.Context) (int, error)
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
		p.hub.Publish(channels, Envelope{
			Event: "new_entry",
			Data: map[string]any{
				"entry": entry,
			},
		})
	}
	p.publishUnreadCounters(ctx, feed)
}

func (p *Publisher) PublishFeedStatusChanged(feed storage.Feed, err error) {
	if p == nil || p.hub == nil {
		return
	}
	channels := []string{ChannelAll, "feed:" + strconv.FormatInt(feed.ID, 10)}
	if feed.CategoryID != nil && *feed.CategoryID > 0 {
		channels = append(channels, "category:"+strconv.FormatInt(*feed.CategoryID, 10))
	}
	msg := ""
	parsingErrorCount := 0
	if err != nil {
		msg = err.Error()
		parsingErrorCount = 1
	}
	p.hub.Publish(channels, Envelope{
		Event: "feed_status_changed",
		Data: map[string]any{
			"feed_id":               feed.ID,
			"parsing_error_count":   parsingErrorCount,
			"parsing_error_message": msg,
			"success":               err == nil,
		},
	})
}

func (p *Publisher) publishUnreadCounters(ctx context.Context, feed storage.Feed) {
	if p.entries == nil {
		return
	}
	feedUnread, err := p.entries.CountUnreadByFeed(ctx, feed.ID)
	if err == nil {
		p.hub.Publish([]string{ChannelAll, "feed:" + strconv.FormatInt(feed.ID, 10)}, Envelope{
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
			p.hub.Publish([]string{ChannelAll, "category:" + strconv.FormatInt(*feed.CategoryID, 10)}, Envelope{
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

	globalUnread, err := p.entries.CountUnreadGlobal(ctx)
	if err == nil {
		p.hub.Publish([]string{ChannelAll}, Envelope{
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
