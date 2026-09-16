package service

import (
	"testing"
	"time"
)

func TestTTLCache(t *testing.T) {
	var c ttlCache[int]
	if _, ok := c.get(1, time.Minute); ok {
		t.Fatal("zero value must miss")
	}
	c.invalidate(1)
	c.put(1, 10)
	c.put(2, 20)
	if v, ok := c.get(1, time.Minute); !ok || v != 10 {
		t.Fatalf("get(1) = %d,%v", v, ok)
	}
	c.invalidate(1)
	if _, ok := c.get(1, time.Minute); ok {
		t.Fatal("invalidated id still cached")
	}
	if v, ok := c.get(2, time.Minute); !ok || v != 20 {
		t.Fatalf("get(2) after invalidate(1) = %d,%v", v, ok)
	}
	c.mu.Lock()
	it := c.items[2]
	it.at = time.Now().Add(-2 * time.Minute)
	c.items[2] = it
	c.mu.Unlock()
	if _, ok := c.get(2, time.Minute); ok {
		t.Fatal("expired item returned")
	}
	c.put(3, 30)
	c.invalidate(0)
	if _, ok := c.get(3, time.Minute); ok {
		t.Fatal("invalidate(0) must drop everything")
	}
	c.put(3, 31)
	if v, ok := c.get(3, time.Minute); !ok || v != 31 {
		t.Fatalf("put after invalidate(0) = %d,%v", v, ok)
	}
}
