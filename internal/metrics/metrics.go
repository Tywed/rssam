package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	initOnce sync.Once

	PollAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "rssam_poll_attempts_total",
		Help: "Number of feed poll attempts.",
	}, []string{"result"})

	PollErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "rssam_poll_errors_total",
		Help: "Number of feed poll errors.",
	})

	PollDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "rssam_poll_duration_seconds",
		Help:    "Feed poll duration in seconds.",
		Buckets: prometheus.DefBuckets,
	})

	ClaimedJobs = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "rssam_jobs_claimed_total",
		Help: "Number of jobs claimed from the queue.",
	})

	JobLag = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "rssam_job_lag_seconds",
		Help:    "Lag between job run_at and claim time.",
		Buckets: prometheus.DefBuckets,
	})

	WebhookDeliveriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "rssam_webhook_deliveries_total",
		Help: "Number of webhook deliveries by final status.",
	}, []string{"status"})

	WebhookDeliveryDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "rssam_webhook_delivery_duration_seconds",
		Help:    "Webhook delivery HTTP duration in seconds.",
		Buckets: prometheus.DefBuckets,
	})

	WebhookRetriesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "rssam_webhook_retries_total",
		Help: "Number of webhook delivery retries scheduled.",
	})

	CleanupDeletedRows = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "rssam_cleanup_deleted_rows_total",
		Help: "Number of rows deleted by the retention cleanup job.",
	}, []string{"table"})

	FilterMatchesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "rssam_filter_matches_total",
		Help: "Number of new filter match rows recorded.",
	})

	FilterProcessingDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "rssam_filter_processing_seconds",
		Help:    "Time to run filters for one entry.",
		Buckets: []float64{.00001, .0001, .001, .005, .01, .05, .1},
	})
)

func Init() {
	initOnce.Do(func() {
		prometheus.MustRegister(
			PollAttempts,
			PollErrors,
			PollDuration,
			ClaimedJobs,
			JobLag,
			WebhookDeliveriesTotal,
			WebhookDeliveryDuration,
			WebhookRetriesTotal,
			CleanupDeletedRows,
			FilterMatchesTotal,
			FilterProcessingDuration,
		)
	})
}

func ObserveJobLag(runAt time.Time, now time.Time) {
	lag := now.Sub(runAt).Seconds()
	if lag < 0 {
		lag = 0
	}
	JobLag.Observe(lag)
}
