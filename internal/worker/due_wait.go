package worker

import (
	"context"
	"time"

	"rssam/internal/storage"
)

// dueWakers are the wake-up channels of the two claim loops; a buffered
// slot per loop so a notification that arrives while the loop is busy is
// kept, and repeats collapse into it.
type dueWakers struct {
	feeds, webhooks chan struct{}
}

func newDueWakers() dueWakers {
	return dueWakers{
		feeds:    make(chan struct{}, 1),
		webhooks: make(chan struct{}, 1),
	}
}

func (w dueWakers) wake(channel string) {
	var ch chan struct{}
	switch channel {
	case storage.NotifyFeedsDue:
		ch = w.feeds
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
// still runs on its own at least every maxWait. The floor of one second
// keeps a row the claim cannot take (due in the past, yet not claimable)
// from turning the loop into a busy one and batches feeds due within the
// same second into one claim.
func idleWait(ctx context.Context, wake <-chan struct{}, next time.Time, known bool, maxWait time.Duration) {
	waitUntil(ctx, wake, next, known, maxWait, time.Second)
}

// slotWaitFloor is the floor for a wait on a bridge slot: the slot frees up
// at a known instant (sampled before the claim query ran, so it may already
// be past) and rounding it up to a second would cost the bridge a part of
// its request rate.
const slotWaitFloor = 20 * time.Millisecond

func waitUntil(ctx context.Context, wake <-chan struct{}, next time.Time, known bool, maxWait, floor time.Duration) {
	d := maxWait
	if known {
		d = min(d, time.Until(next))
	}
	d = max(d, min(maxWait, floor))
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
		r.ensureFeedPollStateRows(ctx)
		err := r.Store.ListenDue(ctx, r.wake.wake)
		if ctx.Err() != nil {
			return
		}
		r.Log.Warn("due listener disconnected; retrying", "err", err)
		idleWait(ctx, nil, time.Time{}, false, 5*time.Second)
	}
}

// ensureFeedPollStateRows runs on start and whenever the listener connection
// was lost: a PostgreSQL crash empties the UNLOGGED feed_poll_state.
func (r *Runner) ensureFeedPollStateRows(ctx context.Context) {
	n, err := r.Store.EnsureFeedPollStateRows(ctx)
	if err != nil {
		r.Log.Error("ensure feed poll state rows failed", "err", err)
		return
	}
	if n > 0 {
		r.Log.Warn("feed poll state rows re-created; those feeds are fetched in full once", "feeds", n)
	}
}
