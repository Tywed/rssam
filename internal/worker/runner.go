package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"rssam/internal/bridge/v1"
	"strconv"
	"sync/atomic"
	"time"

	"rssam/internal/capacity"
	"rssam/internal/metrics"
	"rssam/internal/reader"
	"rssam/internal/ssrf"
	"rssam/internal/storage"
)

type Config struct {
	PoolSize        int
	WebhookPoolSize int
	SchedulerTick   time.Duration
	MinPollInterval time.Duration
	MaxPollInterval time.Duration

	FetchTimeout time.Duration
	InstanceID   string

	WebhookMaxAttempts int
	WebhookTimeout     time.Duration
	WebhookRetryBase   time.Duration
	WebhookRetryMax    time.Duration
	DedupOnlyStorage   bool

	RemovedRetentionDays     int
	WebhookLogRetentionDays  int
	FilterMatchRetentionDays int
	FeedPollLogRetentionDays int
	AuditLogRetentionDays    int
	DedupRetentionDays       int
	CleanupInterval          time.Duration

	FeedPollDailyResetEnabled bool
	FeedPollDailyResetTZ      string
}

// FeedRefreshService refreshes a single feed (implemented by service.FeedRefresher).
type FeedRefreshService interface {
	RefreshFeed(ctx context.Context, feedID int64) (inserted int, err error)
	RefreshLoadedFeed(ctx context.Context, feed storage.Feed) (inserted int, err error)
}

// pollStore is the storage surface used by a single feed poll.
type pollStore interface {
	GetFeedByID(ctx context.Context, id int64) (storage.Feed, error)
	SetFeedNextCheckAt(ctx context.Context, feedID int64, nextCheckAt time.Time) error
}

type Runner struct {
	Log       *slog.Logger
	Store     *storage.PostgresStore
	Refresher FeedRefreshService
	Cfg       Config

	WebhookHTTPClient *http.Client
	SSRFGuard         *ssrf.Guard
	WebhookDelivery   webhookDeliveryStore
	WebhookDigest     webhookDigestStore
	SystemAlerts      SystemAlertsConfig
	paused            atomic.Bool
	wake              dueWakers
	inflight          inflightPolls
	inflightHooks     inflightPolls
	pausedHooks       webhookPauses
}

func (r *Runner) Pause() {
	if r == nil {
		return
	}
	r.paused.Store(true)
	if r.Log != nil {
		r.Log.Info("workers paused")
	}
}

func (r *Runner) Resume() {
	if r == nil {
		return
	}
	r.paused.Store(false)
	if r.Log != nil {
		r.Log.Info("workers resumed")
	}
}

func (r *Runner) Paused() bool {
	return r != nil && r.paused.Load()
}

func DefaultInstanceID() string {
	host, _ := os.Hostname()
	pid := os.Getpid()
	if host == "" {
		host = "unknown"
	}
	return host + "-" + strconv.Itoa(pid)
}

// ErrWorkerLockHeld: another rssam process already runs the workers against
// this database.
var ErrWorkerLockHeld = errors.New("another rssam instance holds the worker lock on this database; this process serves HTTP only (one instance per database polls feeds)")

func (r *Runner) Run(ctx context.Context) error {
	if r.Log == nil {
		r.Log = slog.Default()
	}
	if r.Store == nil || r.Refresher == nil {
		return errors.New("worker runner is not configured")
	}
	if r.Cfg.PoolSize <= 0 {
		return errors.New("PoolSize must be > 0")
	}
	if r.Cfg.WebhookPoolSize <= 0 {
		r.Cfg.WebhookPoolSize = r.Cfg.PoolSize
	}
	if r.Cfg.SchedulerTick <= 0 {
		return errors.New("SchedulerTick must be > 0")
	}
	if r.Cfg.FetchTimeout <= 0 {
		r.Cfg.FetchTimeout = 30 * time.Second
	}
	if r.Cfg.InstanceID == "" {
		r.Cfg.InstanceID = DefaultInstanceID()
	}

	// Two processes polling the same database would double every fetch;
	// the second one keeps serving HTTP but runs no workers. The lock is
	// also what lets the in-flight set live in memory instead of a table.
	release, ok, err := r.Store.TryWorkerLock(ctx)
	if err != nil {
		return fmt.Errorf("worker lock: %w", err)
	}
	if !ok {
		return ErrWorkerLockHeld
	}
	defer release()

	metrics.Init()

	feedsCh := make(chan storage.DueFeed, r.Cfg.PoolSize*2)
	webhookLogsCh := make(chan storage.WebhookLog, r.Cfg.WebhookPoolSize*2)

	r.wake = newDueWakers()
	r.inflight.init(max(r.Cfg.FetchTimeout*2, 2*time.Minute), time.Second)
	// A delivery whose status update failed is retried after the same
	// 30 s the old lease column gave it.
	r.inflightHooks.init(0, 30*time.Second)
	go r.listenDueLoop(ctx)

	// Retention cleanup: periodically purge old removed entries and logs.
	go r.cleanupLoop(ctx)

	// Daily reset of feed error backoff / circuit breaker (optional, timezone-aware).
	go r.dailyFeedResetLoop(ctx)
	go r.systemAlertsLoop(ctx)

	// Dispatcher: hand due feeds to the worker pool.
	go r.dispatchLoop(ctx, feedsCh)

	// Worker pool.
	for i := 0; i < r.Cfg.PoolSize; i++ {
		go r.workerLoop(ctx, feedsCh)
	}

	// Webhooks dispatcher + workers.
	go r.webhookDispatchLoop(ctx, webhookLogsCh)
	for i := 0; i < r.Cfg.WebhookPoolSize; i++ {
		go r.webhookWorkerLoop(ctx, webhookLogsCh)
	}

	<-ctx.Done()
	return nil
}

// dispatchLoop hands due feeds to the worker pool, then sleeps until the
// earliest next_check_at, a feeds-due notification, or SchedulerTick at
// most. A feed stays due in the table while its poll runs, so the in-flight
// set is excluded from both queries.
func (r *Runner) dispatchLoop(ctx context.Context, out chan<- storage.DueFeed) {
	for ctx.Err() == nil {
		if r.Paused() {
			idleWait(ctx, r.wake.feeds, time.Time{}, false, r.Cfg.SchedulerTick)
			continue
		}
		now := time.Now()
		room := cap(out) - len(out)
		if room <= 0 {
			// Workers are saturated; a short nap instead of a due-time
			// lookup nobody can act on.
			idleWait(ctx, r.wake.feeds, now.Add(time.Second), true, time.Second)
			continue
		}
		exclude := r.inflight.exclude(now)
		due, err := r.Store.ListFeedsDue(ctx, min(r.Cfg.PoolSize, room), exclude)
		if err != nil {
			r.Log.Error("dispatcher: list due feeds failed", "err", err)
			idleWait(ctx, r.wake.feeds, time.Time{}, false, r.Cfg.SchedulerTick)
			continue
		}
		if len(due) > 0 {
			metrics.ClaimedJobs.Add(float64(len(due)))
			claimedAt := time.Now().UTC()
			for _, d := range due {
				metrics.ObserveJobLag(d.DueAt, claimedAt)
				r.inflight.start(d.ID, claimedAt)
				select {
				case out <- d:
				case <-ctx.Done():
					return
				}
			}
			continue
		}
		next, known, err := r.Store.NextFeedDueAt(ctx, exclude)
		if err != nil {
			r.Log.Error("dispatcher: next feed due failed", "err", err)
		}
		idleWait(ctx, r.wake.feeds, next, known, r.Cfg.SchedulerTick)
	}
}

func (r *Runner) workerLoop(ctx context.Context, in <-chan storage.DueFeed) {
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-in:
			processPollFeed(ctx, d.ID, r.Store, r.Refresher, r.Cfg, r.Log)
			r.inflight.done(d.ID, time.Now())
		}
	}
}

// Inflight reports the polls running right now and how many of them have
// run longer than the stale threshold (a fetch that ignored its timeout).
func (r *Runner) Inflight() (running, stale int) {
	if r == nil {
		return 0, 0
	}
	return r.inflight.counts(time.Now())
}

// PollingSince is when the poll of feedID started, if one is running.
func (r *Runner) PollingSince(feedID int64) (time.Time, bool) {
	if r == nil {
		return time.Time{}, false
	}
	return r.inflight.since(feedID)
}

// processPollFeed polls one feed. Every exit leaves the feed not due any
// more: the refresher persists next_check_at on success and on error, a
// retry-after hint is written here, and a paused or deleted feed is not
// listed by ListFeedsDue.
func processPollFeed(ctx context.Context, feedID int64, store pollStore, refresher FeedRefreshService, cfg Config, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	if store == nil || refresher == nil {
		return
	}

	feed, feedErr := store.GetFeedByID(ctx, feedID)
	if feedErr != nil {
		log.Warn("poll_feed feed lookup failed", "feed_id", feedID, "err", feedErr)
		return
	}
	if storage.FeedPollingBlocked(feed) {
		log.Info("poll_feed skipped: paused", "feed_id", feedID, "manual", feed.ManualPaused, "circuit", feed.PollPaused, "errors", feed.ParsingErrorCount)
		return
	}

	timeout := cfg.FetchTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	_, err := refresher.RefreshLoadedFeed(runCtx, feed)
	dur := time.Since(start)
	now := time.Now().UTC()
	if at, ok := reader.RetryAt(err); ok {
		metrics.PollAttempts.WithLabelValues("deferred").Inc()
		metrics.PollDeferred.WithLabelValues(bridge.DeferReason(err)).Inc()
		if at.Before(now.Add(50 * time.Millisecond)) {
			at = now.Add(time.Second)
		}
		var persisted interface{ NextCheckPersisted() bool }
		if errors.As(err, &persisted) && persisted.NextCheckPersisted() {
			return
		}
		if err := store.SetFeedNextCheckAt(ctx, feedID, at); err != nil {
			log.Warn("poll_feed: set next_check_at after retry-after failed", "feed_id", feedID, "err", err)
		}
		return
	}

	metrics.PollDuration.Observe(dur.Seconds())
	capacity.ObservePollDuration(dur)

	if err != nil {
		metrics.PollErrors.Inc()
		metrics.PollAttempts.WithLabelValues("error").Inc()

		feedAfter, feedErr := store.GetFeedByID(ctx, feedID)
		if feedErr != nil {
			log.Warn("poll_feed failed", "feed_id", feedID, "err", err, "lookup_err", feedErr)
			return
		}
		var next time.Time
		if feedAfter.NextCheckAt != nil && feedAfter.NextCheckAt.After(now) {
			// The refresher already persisted the error backoff (and
			// published it over WebSocket).
			next = *feedAfter.NextCheckAt
		} else {
			next = storage.FeedNextCheckAfterError(now, feedAfter.IntervalMinutes, feedAfter.ParsingErrorCount, cfg.MinPollInterval, cfg.MaxPollInterval)
			// A feed left due would be picked up again at once; that must
			// not be silent.
			if serr := store.SetFeedNextCheckAt(ctx, feedID, next); serr != nil {
				log.Error("poll_feed: set next_check_at failed", "feed_id", feedID, "err", serr)
			}
		}
		log.Warn("poll_feed failed", "feed_id", feedID, "error_count", feedAfter.ParsingErrorCount, "next_check_at", next, "err", err)
		return
	}

	metrics.PollAttempts.WithLabelValues("success").Inc()
}
