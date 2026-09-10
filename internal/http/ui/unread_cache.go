package ui

import (
	"context"
	"sync"
	"time"
)

const unreadCountsTTL = 3 * time.Second

type unreadCountsSnap struct {
	exp    time.Time
	feeds  map[int64]int
	cats   map[int64]int
	labels map[int64]int
	total  int
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
	if !ok {
		return unreadCountsSnap{}, false
	}
	if time.Now().After(s.exp) {
		delete(c.items, userID)
		return unreadCountsSnap{}, false
	}
	return s, true
}

func (c *unreadCountsCache) put(userID int64, snap unreadCountsSnap) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[int64]unreadCountsSnap)
	}
	snap.exp = time.Now().Add(unreadCountsTTL)
	c.items[userID] = snap
}

func (c *unreadCountsCache) invalidate(userID int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, userID)
}

func (h *Handler) unreadCounts(ctx context.Context, userID int64) (unreadCountsSnap, error) {
	if snap, ok := h.unreadCache.get(userID); ok {
		return snap, nil
	}
	var snap unreadCountsSnap
	if h.cfg.Entries == nil {
		return snap, nil
	}
	feeds, cats, err := h.cfg.Entries.UnreadCountsForUser(ctx, userID)
	if err != nil {
		return snap, err
	}
	snap.feeds, snap.cats = feeds, cats
	for _, n := range feeds {
		snap.total += n
	}
	if h.cfg.Labels != nil {
		if labels, err := h.cfg.Labels.UnreadCountsByLabel(ctx, userID); err == nil {
			snap.labels = labels
		}
	}
	h.unreadCache.put(userID, snap)
	return snap, nil
}

func (h *Handler) invalidateUnread(userID int64) {
	h.unreadCache.invalidate(userID)
}
