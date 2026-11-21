package worker

import (
	"log/slog"
	"sync"
	"time"
)

// dbOutage folds the failures the loops see while PostgreSQL is unreachable
// into one line per outageLogEvery: the first failure is logged with its
// error, later ones are counted, and recovery reports the downtime. Without
// it a restart of PostgreSQL costs ~30 lines a minute (listener, poll-state
// repair and both claim loops, each on its own retry cadence).
type dbOutage struct {
	mu         sync.Mutex
	since      time.Time
	lastLog    time.Time
	suppressed int
}

const outageLogEvery = time.Minute

func (o *dbOutage) down(log *slog.Logger, msg string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now()
	if o.since.IsZero() {
		o.since, o.lastLog = now, now
		log.Error(msg, "err", err)
		return
	}
	if now.Sub(o.lastLog) < outageLogEvery {
		o.suppressed++
		return
	}
	log.Warn("database still unavailable", "since", o.since.Format(time.RFC3339), "suppressed", o.suppressed, "last", msg, "err", err)
	o.lastLog, o.suppressed = now, 0
}

func (o *dbOutage) up(log *slog.Logger, who string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.since.IsZero() {
		return
	}
	log.Warn("database available again", "down", time.Since(o.since).Round(time.Second).String(), "suppressed", o.suppressed, "by", who)
	o.since, o.lastLog, o.suppressed = time.Time{}, time.Time{}, 0
}
