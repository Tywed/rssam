package worker

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
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
	CleanupInterval          time.Duration

	FeedPollDailyResetEnabled bool
	FeedPollDailyResetTZ      string
}

// FeedRefreshService refreshes a single feed (implemented by service.FeedRefresher).
type FeedRefreshService interface {
	RefreshFeed(ctx context.Context, feedID int64) (inserted int, err error)
	RefreshLoadedFeed(ctx context.Context, feed storage.Feed) (inserted int, err error)
}

// pollJobStore is the storage surface used by poll_feed job processing.
type pollJobStore interface {
	CompleteJob(ctx context.Context, jobID int64, lockedBy string) (deleted bool, err error)
	RescheduleJob(ctx context.Context, jobID int64, lockedBy string, runAt time.Time, lastError string) error
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
	paused            atomic.Bool
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

	metrics.Init()

	jobsCh := make(chan storage.Job, r.Cfg.PoolSize*2)
	webhookLogsCh := make(chan storage.WebhookLog, r.Cfg.WebhookPoolSize*2)

	// Scheduler: enqueue due feeds periodically (deduplicated by unique index).
	go r.schedulerLoop(ctx)

	// Retention cleanup: periodically purge old removed entries and logs.
	go r.cleanupLoop(ctx)

	// Daily reset of feed error backoff / circuit breaker (optional, timezone-aware).
	go r.dailyFeedResetLoop(ctx)

	// Dispatcher: claim jobs and feed to the worker pool.
	go r.dispatchLoop(ctx, jobsCh)

	// Worker pool.
	for i := 0; i < r.Cfg.PoolSize; i++ {
		go r.workerLoop(ctx, jobsCh)
	}

	// Webhooks dispatcher + workers.
	go r.webhookDispatchLoop(ctx, webhookLogsCh)
	for i := 0; i < r.Cfg.WebhookPoolSize; i++ {
		go r.webhookWorkerLoop(ctx, webhookLogsCh)
	}

	<-ctx.Done()
	return nil
}

func (r *Runner) schedulerLoop(ctx context.Context) {
	t := time.NewTicker(r.Cfg.SchedulerTick)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if r.Paused() {
				continue
			}
			ids, err := r.Store.ListFeedsDue(ctx, 1000)
			if err != nil {
				r.Log.Error("scheduler: list due feeds failed", "err", err)
				continue
			}
			if len(ids) == 0 {
				continue
			}
			now := time.Now().UTC()
			if err := r.Store.EnqueuePollFeedJobs(ctx, ids, now); err != nil {
				r.Log.Error("scheduler: enqueue poll_feed jobs failed", "err", err)
			}
		}
	}
}

func (r *Runner) dispatchLoop(ctx context.Context, out chan<- storage.Job) {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	var lastReclaim time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if r.Paused() {
				continue
			}
			now := time.Now()
			if lastReclaim.IsZero() || now.Sub(lastReclaim) >= 20*time.Second {
				staleAfter := max(r.Cfg.FetchTimeout*2, 2*time.Minute)
				n, err := r.Store.ReclaimStalePollJobs(ctx, r.Cfg.InstanceID, staleAfter)
				if err != nil {
					r.Log.Error("dispatcher: reclaim stale jobs failed", "err", err)
				} else if n > 0 {
					r.Log.Warn("dispatcher: reclaimed stale poll_feed locks", "count", n)
				}
				lastReclaim = now
			}

			room := cap(out) - len(out)
			if room <= 0 {
				continue
			}
			limit := min(r.Cfg.PoolSize, room)
			jobs, err := r.Store.ClaimDueJobs(ctx, limit, r.Cfg.InstanceID)
			if err != nil {
				r.Log.Error("dispatcher: claim due jobs failed", "err", err)
				continue
			}
			if len(jobs) == 0 {
				continue
			}
			metrics.ClaimedJobs.Add(float64(len(jobs)))
			claimedAt := time.Now().UTC()
			for _, j := range jobs {
				metrics.ObserveJobLag(j.RunAt, claimedAt)
				select {
				case out <- j:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

func (r *Runner) workerLoop(ctx context.Context, in <-chan storage.Job) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-in:
			r.processJob(ctx, j)
		}
	}
}

func (r *Runner) processJob(ctx context.Context, j storage.Job) {
	switch j.Type {
	case "poll_feed":
		r.processPollFeed(ctx, j)
	default:
		r.Log.Warn("unknown job type, dropping", "job_id", j.ID, "type", j.Type)
		_, _ = r.Store.CompleteJob(ctx, j.ID, r.Cfg.InstanceID)
	}
}

func (r *Runner) processPollFeed(ctx context.Context, j storage.Job) {
	processPollFeedJob(ctx, j, r.Store, r.Refresher, r.Cfg, r.Log)
}

func processPollFeedJob(ctx context.Context, j storage.Job, store pollJobStore, refresher FeedRefreshService, cfg Config, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	if store == nil || refresher == nil {
		return
	}
	if j.FeedID == nil || *j.FeedID <= 0 {
		log.Warn("poll_feed job missing feed_id, dropping", "job_id", j.ID)
		_, _ = store.CompleteJob(ctx, j.ID, cfg.InstanceID)
		return
	}
	feedID := *j.FeedID

	feed, feedErr := store.GetFeedByID(ctx, feedID)
	if feedErr != nil {
		log.Warn("poll_feed feed lookup failed", "feed_id", feedID, "job_id", j.ID, "err", feedErr)
		_, _ = store.CompleteJob(ctx, j.ID, cfg.InstanceID)
		return
	}
	if storage.FeedPollingBlocked(feed) {
		log.Info("poll_feed skipped: paused", "feed_id", feedID, "job_id", j.ID, "manual", feed.ManualPaused, "circuit", feed.PollPaused, "errors", feed.ParsingErrorCount)
		_, _ = store.CompleteJob(ctx, j.ID, cfg.InstanceID)
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
		if at.Before(now.Add(50 * time.Millisecond)) {
			at = now.Add(time.Second)
		}
		if err := store.SetFeedNextCheckAt(ctx, feedID, at); err != nil {
			log.Warn("poll_feed: set next_check_at after retry-after failed", "feed_id", feedID, "err", err)
		}
		if _, err := store.CompleteJob(ctx, j.ID, cfg.InstanceID); err != nil {
			log.Warn("poll_feed: complete job failed", "job_id", j.ID, "err", err)
		}
		return
	}

	metrics.PollDuration.Observe(dur.Seconds())
	capacity.ObservePollDuration(dur)

	if err != nil {
		metrics.PollErrors.Inc()
		metrics.PollAttempts.WithLabelValues("error").Inc()

		feedAfter, feedErr := store.GetFeedByID(ctx, feedID)
		var next time.Time
		errCount := 0
		persisted := false
		switch {
		case feedErr != nil:
			next = now.Add(Backoff(j.Attempts+1, cfg.MinPollInterval, cfg.MaxPollInterval))
		case feedAfter.NextCheckAt != nil && feedAfter.NextCheckAt.After(now):
			// The refresher already persisted the error backoff (and published
			// it over WebSocket); reuse it so job.run_at == feeds.next_check_at.
			errCount = feedAfter.ParsingErrorCount
			next = *feedAfter.NextCheckAt
			persisted = true
		default:
			errCount = feedAfter.ParsingErrorCount
			next = storage.FeedNextCheckAfterError(now, feedAfter.IntervalMinutes, feedAfter.ParsingErrorCount, cfg.MinPollInterval, cfg.MaxPollInterval)
		}
		// If these fail the job may be reclaimed by the stale-job sweeper or,
		// worse, never rescheduled — that must not be silent.
		if rerr := store.RescheduleJob(ctx, j.ID, cfg.InstanceID, next, err.Error()); rerr != nil {
			log.Error("poll_feed: reschedule job failed", "job_id", j.ID, "feed_id", feedID, "err", rerr)
		}
		if !persisted {
			if serr := store.SetFeedNextCheckAt(ctx, feedID, next); serr != nil {
				log.Warn("poll_feed: set next_check_at failed", "feed_id", feedID, "err", serr)
			}
		}
		log.Warn("poll_feed failed", "feed_id", feedID, "job_id", j.ID, "attempts", j.Attempts+1, "error_count", errCount, "next_check_at", next, "err", err)
		return
	}

	metrics.PollAttempts.WithLabelValues("success").Inc()

	deleted, delErr := store.CompleteJob(ctx, j.ID, cfg.InstanceID)
	if delErr != nil {
		log.Error("complete job failed", "job_id", j.ID, "err", delErr)
	} else if !deleted {
		log.Warn("complete job: job was not deleted", "job_id", j.ID)
	}
}
