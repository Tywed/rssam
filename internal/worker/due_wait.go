package worker

import (
	"context"
	"time"

	"rssam/internal/storage"
)

// dueWakers are the wake-up channels of the three claim loops; a buffered
// slot per loop so a notification that arrives while the loop is busy is
// kept, and repeats collapse into it.
type dueWakers struct {
	feeds, jobs, webhooks chan struct{}
}

func newDueWakers() dueWakers {
	return dueWakers{
		feeds:    make(chan struct{}, 1),
		jobs:     make(chan struct{}, 1),
		webhooks: make(chan struct{}, 1),
	}
}

func (w dueWakers) wake(channel string) {
	var ch chan struct{}
	switch channel {
	case storage.NotifyFeedsDue:
		ch = w.feeds
	case storage.NotifyJobsDue:
		ch = w.jobs
	case storage.NotifyWebhooksDue:
		ch = w.webhooks
	default:
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

// idleWait blocks until wake fires, until next (when known), or for maxWait
// at most. The cap bounds the damage of a missed notification: the loop
// still runs on its own at least every maxWait. The floor keeps a row the
// claim cannot take (due in the past, yet not claimable) from turning the
// loop into a busy one: never more than one round per second, as before.
func idleWait(ctx context.Context, wake <-chan struct{}, next time.Time, known bool, maxWait time.Duration) {
	d := maxWait
	if known {
		d = min(d, time.Until(next))
	}
	d = max(d, min(maxWait, time.Second))
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-wake:
	case <-t.C:
	}
}

// listenDueLoop keeps one LISTEN connection and reconnects after a failure.
func (r *Runner) listenDueLoop(ctx context.Context) {
	for ctx.Err() == nil {
		err := r.Store.ListenDue(ctx, r.wake.wake)
		if ctx.Err() != nil {
			return
		}
		r.Log.Warn("due listener disconnected; retrying", "err", err)
		idleWait(ctx, nil, time.Time{}, false, 5*time.Second)
	}
}
