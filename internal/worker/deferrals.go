package worker

import (
	"sort"
	"sync"
)

// deferralCounts counts polls turned away without a fetch since the process
// started, by bridge and reason — the «Система» page shows them so that a
// dispatcher handing a bridge more feeds than it has slots is visible.
type deferralCounts struct {
	mu     sync.Mutex
	counts map[deferralKey]uint64
}

type deferralKey struct {
	FeedType string
	Reason   string
}

// Deferral is one row of the count: Bridge, Reason, Count.
type Deferral struct {
	Bridge string
	Reason string
	Count  uint64
}

func (d *deferralCounts) add(feedType, reason string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	if d.counts == nil {
		d.counts = make(map[deferralKey]uint64)
	}
	d.counts[deferralKey{feedType, reason}]++
	d.mu.Unlock()
}

func (d *deferralCounts) snapshot() []Deferral {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	out := make([]Deferral, 0, len(d.counts))
	for k, n := range d.counts {
		out = append(out, Deferral{Bridge: k.FeedType, Reason: k.Reason, Count: n})
	}
	d.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Bridge != out[j].Bridge {
			return out[i].Bridge < out[j].Bridge
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

// Deferrals reports the polls deferred since start, most frequent first.
func (r *Runner) Deferrals() []Deferral {
	if r == nil {
		return nil
	}
	return r.deferrals.snapshot()
}
