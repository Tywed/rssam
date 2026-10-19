package worker

import (
	"sync"
	"time"
)

// inflightPolls is the set of feeds being polled (or webhook rows being
// delivered) by this process. It replaces a lease column: with one worker
// per database (TryWorkerLock) nothing else needs to see it, and a process
// restart simply finds the interrupted rows still due. A finished item stays
// excluded for finishedGrace so that one whose status update failed (storage
// error) cannot be re-claimed in a tight loop; an item older than staleAfter
// (a fetch that ignored its timeout) is reported as stale and no longer
// blocks the feed (0 = never).
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

func (p *inflightPolls) init(staleAfter, finishedGrace time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.polls = make(map[int64]inflightPoll)
	p.staleAfter = staleAfter
	p.finishedGrace = finishedGrace
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

// forget drops an item whose final status is known to be stored: nothing
// protects it from being claimed again, and nothing needs to.
func (p *inflightPolls) forget(id int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.polls, id)
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
