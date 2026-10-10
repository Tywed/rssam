package worker

import (
	"context"
	"testing"
	"time"

	"rssam/internal/storage"
)

func TestIdleWait(t *testing.T) {
	ctx := context.Background()
	w := newDueWakers()

	// A wake-up ends the wait at once, and repeats collapse into one slot.
	w.wake(storage.NotifyJobsDue)
	w.wake(storage.NotifyJobsDue)
	w.wake("unknown")
	start := time.Now()
	idleWait(ctx, w.jobs, time.Time{}, false, time.Minute)
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("wake must end the wait immediately")
	}
	select {
	case <-w.jobs:
		t.Fatal("second wake must have collapsed into the first")
	default:
	}

	// A known due time inside the cap bounds the wait; a floor of one
	// second (or the cap, if smaller) stops busy loops on a past due time.
	start = time.Now()
	idleWait(ctx, w.feeds, time.Now().Add(50*time.Millisecond), true, 20*time.Millisecond)
	if d := time.Since(start); d < 15*time.Millisecond || d > 200*time.Millisecond {
		t.Fatalf("cap: waited %v", d)
	}
	start = time.Now()
	idleWait(ctx, w.feeds, time.Now().Add(-time.Hour), true, 30*time.Millisecond)
	if d := time.Since(start); d < 25*time.Millisecond {
		t.Fatalf("floor: waited %v", d)
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	start = time.Now()
	idleWait(cctx, w.webhooks, time.Time{}, false, time.Minute)
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("cancelled context must end the wait")
	}
}
