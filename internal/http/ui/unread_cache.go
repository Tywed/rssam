package ui

import (
	"context"
	"sync"
	"time"
)

const unreadCountsTTL = 3 * time.Second

type unreadCountsSnap struct {
	exp   time.Time
	feeds map[int64]int
	cats  map[int64]int
	total int
}

type unreadCountsCache struct {
	mu    sync.Mutex
	items map[int64]unreadCountsSnap
}

func newUnreadCountsCache() *unreadCountsCache {
	return &unreadCountsCache{items: make(map[int64]unreadCountsSnap)}
}

func (c *unreadCountsCache) get(userID int64) (unreadCountsSnap, bool) {
	if c == nil {
		return unreadCountsSnap{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.items[userID]
	if !ok || time.Now().After(s.exp) {
		return unreadCountsSnap{}, false
	}
	return s, true
}

func (c *unreadCountsCache) put(userID int64, feeds, cats map[int64]int, total int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[int64]unreadCountsSnap)
	}
	c.items[userID] = unreadCountsSnap{
		exp:   time.Now().Add(unreadCountsTTL),
		feeds: feeds,
		cats:  cats,
		total: total,
	}
}

func (c *unreadCountsCache) invalidate(userID int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, userID)
}

func (h *Handler) unreadCounts(ctx context.Context, userID int64) (feeds, cats map[int64]int, total int, err error) {
	if snap, ok := h.unreadCache.get(userID); ok {
		return snap.feeds, snap.cats, snap.total, nil
	}
	if h.cfg.Entries == nil {
		return nil, nil, 0, nil
	}
	feeds, cats, err = h.cfg.Entries.UnreadCountsForUser(ctx, userID)
	if err != nil {
		return nil, nil, 0, err
	}
	for _, n := range feeds {
		total += n
	}
	h.unreadCache.put(userID, feeds, cats, total)
	return feeds, cats, total, nil
}

func (h *Handler) invalidateUnread(userID int64) {
	h.unreadCache.invalidate(userID)
}
