package worker

import (
	"sync"
	"time"
)

// inflightPolls is the set of feeds being polled by this process. It
// replaces the jobs table: with one poller per database (TryWorkerLock)
// nothing else needs to see it, and a process restart simply finds the
// interrupted feeds still due. A finished feed stays excluded for
// finishedGrace so that a poll which failed to move next_check_at (storage
// error) cannot be re-claimed in a tight loop; a poll older than staleAfter
// (a fetch that ignored its timeout) is reported as stale and no longer
// blocks the feed.
type inflightPolls struct {
	mu            sync.Mutex
	polls         map[int64]inflightPoll
	staleAfter    time.Duration
	finishedGrace time.Duration
}

type inflightPoll struct {
	startedAt time.Time
	doneAt    time.Time
}

func (p *inflightPolls) init(staleAfter time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.polls = make(map[int64]inflightPoll)
	p.staleAfter = staleAfter
	p.finishedGrace = time.Second
}

func (p *inflightPolls) start(feedID int64, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.polls == nil {
		p.polls = make(map[int64]inflightPoll)
	}
	p.polls[feedID] = inflightPoll{startedAt: at}
}

func (p *inflightPolls) done(feedID int64, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.polls[feedID]; ok && e.doneAt.IsZero() {
		e.doneAt = at
		p.polls[feedID] = e
	}
}

// exclude returns the feed ids the dispatcher must not claim at now and
// forgets finished polls past their grace.
func (p *inflightPolls) exclude(now time.Time) []int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int64, 0, len(p.polls))
	for id, e := range p.polls {
		switch {
		case !e.doneAt.IsZero():
			if now.Sub(e.doneAt) >= p.finishedGrace {
				delete(p.polls, id)
				continue
			}
		case p.staleAfter > 0 && now.Sub(e.startedAt) >= p.staleAfter:
			continue
		}
		out = append(out, id)
	}
	return out
}

func (p *inflightPolls) counts(now time.Time) (running, stale int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.polls {
		if !e.doneAt.IsZero() {
			continue
		}
		running++
		if p.staleAfter > 0 && now.Sub(e.startedAt) >= p.staleAfter {
			stale++
		}
	}
	return running, stale
}

func (p *inflightPolls) since(feedID int64) (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.polls[feedID]
	if !ok || !e.doneAt.IsZero() {
		return time.Time{}, false
	}
	return e.startedAt, true
}
