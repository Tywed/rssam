package max

import (
	"testing"
	"time"

	"rssam/internal/ssrf"
)

// FreeSlots answers from the same state tryReserveSlot decides by: the
// concurrency limit, the request interval and the rate-limit cool-down.
func TestFreeSlots_FollowsSlotState(t *testing.T) {
	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(nil, guard, Config{APIBaseURL: "http://127.0.0.1:1", ConcurrentSlots: 2, RequestInterval: 500 * time.Millisecond, RateLimitCooldown: time.Minute, AllowPrivateAPI: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if free, _ := h.FreeSlots(now, 0); free != 2 {
		t.Fatalf("idle: free=%d, want 2", free)
	}
	// A poll claimed but not yet started holds a slot for the dispatcher.
	if free, _ := h.FreeSlots(now, 1); free != 1 {
		t.Fatalf("one claimed: free=%d, want 1", free)
	}
	if free, until := h.FreeSlots(now, 2); free != 0 || until.IsZero() {
		t.Fatalf("two claimed: free=%d until=%s", free, until)
	}
	if _, ok := h.tryReserveSlot(); !ok {
		t.Fatal("reserve 1")
	}
	if free, _ := h.FreeSlots(now, 0); free != 1 {
		t.Fatalf("one in flight: free=%d, want 1", free)
	}
	if _, ok := h.tryReserveSlot(); !ok {
		t.Fatal("reserve 2")
	}
	if free, until := h.FreeSlots(now, 0); free != 0 || until.Before(now.Add(400*time.Millisecond)) {
		t.Fatalf("both busy: free=%d until=%s", free, until.Sub(now))
	}
	// Both released: the request interval holds the bridge, then it is free.
	h.releaseSlot(nil)
	h.releaseSlot(nil)
	if free, until := h.FreeSlots(time.Now(), 0); free != 0 || until.IsZero() {
		t.Fatalf("within the interval: free=%d until=%s", free, until)
	}
	if free, _ := h.FreeSlots(time.Now().Add(time.Second), 0); free != 2 {
		t.Fatalf("after the interval: free=%d, want 2", free)
	}
	// A rate limit holds every slot for the cool-down.
	h.slotMu.Lock()
	h.nextSlot = time.Time{}
	h.slotMu.Unlock()
	if _, ok := h.tryReserveSlot(); !ok {
		t.Fatal("reserve 3")
	}
	limited := true
	h.releaseSlot(&limited)
	if free, until := h.FreeSlots(time.Now().Add(time.Second), 0); free != 0 || time.Until(until) < 50*time.Second {
		t.Fatalf("rate limited: free=%d until=%s", free, time.Until(until))
	}
}
