package storage

import (
	"context"
	"fmt"
	"time"
)

// Due channels the worker listens on; raised by the triggers of migration
// 0057 when a feed or delivery becomes due earlier than the loops expect.
const (
	NotifyFeedsDue    = "rssam_feeds_due"
	NotifyWebhooksDue = "rssam_webhooks_due"
)

// NextWebhookRetryAt is the earliest next_retry_at of a pending or failed
// row of a webhook not in excludeWebhooks.
func (s *PostgresStore) NextWebhookRetryAt(ctx context.Context, excludeWebhooks []int64) (time.Time, bool, error) {
	if excludeWebhooks == nil {
		excludeWebhooks = []int64{}
	}
	var at *time.Time
	if err := s.db.QueryRow(ctx, `SELECT min(next_retry_at) FROM webhook_logs WHERE status IN ('pending','failed') AND NOT (webhook_id = ANY($1::bigint[]))`, excludeWebhooks).Scan(&at); err != nil {
		return time.Time{}, false, fmt.Errorf("next webhook: %w", err)
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
	for _, ch := range []string{NotifyFeedsDue, NotifyWebhooksDue} {
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
