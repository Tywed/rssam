package httpserver

import (
	"strconv"
	"testing"
	"time"
)

func TestBadTokenCache_BatchEviction(t *testing.T) {
	var c badTokenCache
	for i := range badTokenMax {
		c.remember("h" + strconv.Itoa(i))
	}
	if !c.known("h0") || !c.known("h"+strconv.Itoa(badTokenMax-1)) {
		t.Fatal("entries must be remembered")
	}
	c.remember("overflow")
	if n := len(c.m); n != badTokenMax/2+1 {
		t.Fatalf("after overflow len=%d want %d", n, badTokenMax/2+1)
	}
	if c.known("h0") || !c.known("h"+strconv.Itoa(badTokenMax-1)) || !c.known("overflow") {
		t.Fatal("oldest half must go, newest must stay")
	}
	// Expired entries are dropped on lookup and make room without a sort.
	c.m["old"] = time.Now().Add(-2 * badTokenTTL)
	if c.known("old") {
		t.Fatal("expired entry must not be known")
	}
}

func BenchmarkBadTokenRememberFull(b *testing.B) {
	var c badTokenCache
	for i := range badTokenMax {
		c.remember("h" + strconv.Itoa(i))
	}
	b.ResetTimer()
	for i := range b.N {
		c.remember("n" + strconv.Itoa(i))
	}
}
