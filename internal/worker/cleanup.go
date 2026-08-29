package worker

import (
	"context"
	"time"

	"rssam/internal/metrics"
	"rssam/internal/storage"
)

func (r *Runner) cleanupLoop(ctx context.Context) {
	interval := r.Cfg.CleanupInterval
	if interval <= 0 {
		return
	}

	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.runRetentionCleanup(ctx)
		}
	}
}

func (r *Runner) runRetentionCleanup(ctx context.Context) {
	now := time.Now().UTC()

	opts := storage.RetentionCleanupOpts{
		RemovedEntriesBefore: storage.RetentionCutoff(now, r.Cfg.RemovedRetentionDays),
		WebhookLogsBefore:    storage.RetentionCutoff(now, r.Cfg.WebhookLogRetentionDays),
	}
	if r.Cfg.FilterMatchRetentionDays > 0 {
		opts.FilterMatchesBefore = storage.RetentionCutoff(now, r.Cfg.FilterMatchRetentionDays)
	}

	result, err := r.Store.RunRetentionCleanup(ctx, opts)
	if err != nil {
		r.Log.Error("retention cleanup failed", "err", err)
		return
	}

	if result.RemovedEntries > 0 {
		metrics.CleanupDeletedRows.WithLabelValues("entries").Add(float64(result.RemovedEntries))
	}
	if result.WebhookLogs > 0 {
		metrics.CleanupDeletedRows.WithLabelValues("webhook_logs").Add(float64(result.WebhookLogs))
	}
	if result.FilterMatches > 0 {
		metrics.CleanupDeletedRows.WithLabelValues("filter_matches").Add(float64(result.FilterMatches))
	}
	if result.FeedEntries > 0 {
		metrics.CleanupDeletedRows.WithLabelValues("feed_entries").Add(float64(result.FeedEntries))
	}
	if result.FeedEntryDedup > 0 {
		metrics.CleanupDeletedRows.WithLabelValues("feed_entry_dedup").Add(float64(result.FeedEntryDedup))
	}

	if result.RemovedEntries > 0 || result.WebhookLogs > 0 || result.FilterMatches > 0 ||
		result.FeedEntries > 0 || result.FeedEntryDedup > 0 {
		r.Log.Info("retention cleanup completed",
			"removed_entries", result.RemovedEntries,
			"webhook_logs", result.WebhookLogs,
			"filter_matches", result.FilterMatches,
			"feed_entries", result.FeedEntries,
			"feed_entry_dedup", result.FeedEntryDedup,
		)
	}
}
