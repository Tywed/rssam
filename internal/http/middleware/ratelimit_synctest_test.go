package middleware

import (
	"testing"
	"testing/synctest"
	"time"
)

// Token refill and stale-bucket eviction depend on time.Now; inside a
// synctest bubble the clock is fake, so this runs instantly and exactly.
func TestLimiter_RefillsOverTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := NewLimiter(true, 0.2, 3) // 3 burst, then one token every 5 s

		for i := range 3 {
			if !l.Allow("ip") {
				t.Fatalf("burst request %d denied", i)
			}
		}
		if l.Allow("ip") {
			t.Fatal("4th request must be denied")
		}

		time.Sleep(4 * time.Second)
		if l.Allow("ip") {
			t.Fatal("token refilled too early (4 s at 0.2/s)")
		}
		time.Sleep(1 * time.Second)
		if !l.Allow("ip") {
			t.Fatal("expected one token after 5 s")
		}
		if l.Allow("ip") {
			t.Fatal("only one token should have refilled")
		}

		// Long idle: bucket refills to burst, never above it.
		time.Sleep(time.Hour)
		for i := range 3 {
			if !l.Allow("ip") {
				t.Fatalf("after idle, request %d denied", i)
			}
		}
		if l.Allow("ip") {
			t.Fatal("bucket exceeded burst after idle")
		}
	})
}

func TestLimiter_EvictionUsesWallClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := NewLimiter(true, 10, 20)
		l.Allow("old")
		time.Sleep(bucketTTL + cleanupInterval + time.Second)
		l.Allow("new")

		l.mu.Lock()
		defer l.mu.Unlock()
		if _, ok := l.buckets["old"]; ok {
			t.Fatal("bucket idle longer than bucketTTL was not evicted")
		}
		if _, ok := l.buckets["new"]; !ok {
			t.Fatal("fresh bucket missing")
		}
	})
}
