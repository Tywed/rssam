package service

import (
	"sync"
	"time"
)

// ttlCache is a small per-id cache with a fixed TTL; the zero value is ready
// to use. Errors are never stored: callers put only successful loads.
type ttlCache[V any] struct {
	mu    sync.Mutex
	items map[int64]ttlCacheItem[V]
}

type ttlCacheItem[V any] struct {
	at  time.Time
	val V
}

func (c *ttlCache[V]) get(id int64, ttl time.Duration) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.items[id]
	if !ok {
		var zero V
		return zero, false
	}
	if time.Since(it.at) >= ttl {
		delete(c.items, id)
		var zero V
		return zero, false
	}
	return it.val, true
}

func (c *ttlCache[V]) put(id int64, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[int64]ttlCacheItem[V])
	}
	c.items[id] = ttlCacheItem[V]{at: time.Now(), val: v}
}

// invalidate drops one id, or everything when id <= 0.
func (c *ttlCache[V]) invalidate(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id <= 0 {
		c.items = nil
		return
	}
	delete(c.items, id)
}
