package storage

import (
	"context"
	"fmt"
	"time"
)

// Due channels the worker listens on; raised by the triggers of migration
// 0057 when a row becomes due earlier than the loops expect.
const (
	NotifyFeedsDue    = "rssam_feeds_due"
	NotifyJobsDue     = "rssam_jobs_due"
	NotifyWebhooksDue = "rssam_webhooks_due"
)

// NextFeedDueAt is the earliest next_check_at among pollable feeds without a
// queued job; ok=false when there is none.
func (s *PostgresStore) NextFeedDueAt(ctx context.Context) (time.Time, bool, error) {
	const q = `
SELECT min(COALESCE(f.next_check_at, now()))
FROM feeds f
LEFT JOIN jobs j ON j.type = 'poll_feed' AND j.feed_id = f.id
WHERE f.poll_paused = FALSE
  AND f.manual_paused = FALSE
  AND j.id IS NULL
  AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = f.id)`
	return s.nextAt(ctx, q, "next feed due")
}

// NextJobRunAt is the earliest run_at of an unclaimed job.
func (s *PostgresStore) NextJobRunAt(ctx context.Context) (time.Time, bool, error) {
	return s.nextAt(ctx, `SELECT min(run_at) FROM jobs WHERE locked_at IS NULL`, "next job")
}

// NextWebhookRetryAt is the earliest next_retry_at of a pending or failed
// delivery.
func (s *PostgresStore) NextWebhookRetryAt(ctx context.Context) (time.Time, bool, error) {
	return s.nextAt(ctx, `SELECT min(next_retry_at) FROM webhook_logs WHERE status IN ('pending','failed')`, "next webhook")
}

func (s *PostgresStore) nextAt(ctx context.Context, q, what string) (time.Time, bool, error) {
	var at *time.Time
	if err := s.db.QueryRow(ctx, q).Scan(&at); err != nil {
		return time.Time{}, false, fmt.Errorf("%s: %w", what, err)
	}
	if at == nil {
		return time.Time{}, false, nil
	}
	return *at, true, nil
}

// ListenDue holds one connection on the due channels and calls onNotify with
// the channel name for every notification until ctx ends or the connection
// fails; the caller reconnects.
func (s *PostgresStore) ListenDue(ctx context.Context, onNotify func(channel string)) error {
	pooled, err := s.db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("listen: acquire: %w", err)
	}
	// A connection that was listening must not go back to the pool.
	conn := pooled.Hijack()
	defer func() { _ = conn.Close(context.Background()) }()
	for _, ch := range []string{NotifyFeedsDue, NotifyJobsDue, NotifyWebhooksDue} {
		if _, err := conn.Exec(ctx, "LISTEN "+ch); err != nil {
			return fmt.Errorf("listen %s: %w", ch, err)
		}
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("wait for notification: %w", err)
		}
		onNotify(n.Channel)
	}
}
