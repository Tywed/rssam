package storage

import (
	"context"
	"fmt"
	"time"
)

// SystemAlertFeed is a feed named in a system alert.
type SystemAlertFeed struct {
	ID    int64
	Title string
	Error string
}

// SystemAlertWebhook is a webhook named in a system alert.
type SystemAlertWebhook struct {
	ID        int64
	Name      string
	LastError string
}

// SystemAlertStore feeds the alerter with the current problem sets; every
// query is a filtered scan of a small table (feeds/webhooks), no history.
type SystemAlertStore interface {
	ListSystemAlertWebhooks(ctx context.Context) ([]Webhook, error)
	// ListPausedFeeds: feeds stopped by the circuit breaker or a 410.
	ListPausedFeeds(ctx context.Context) ([]SystemAlertFeed, error)
	// ListSilentFeeds: healthy feeds without a new item for silentAfter.
	ListSilentFeeds(ctx context.Context, silentAfter time.Duration) ([]SystemAlertFeed, error)
	// ListFailingWebhooks: enabled webhooks whose last final delivery died.
	ListFailingWebhooks(ctx context.Context) ([]SystemAlertWebhook, error)
	RecordAudit(ctx context.Context, ev AuditEvent) error
}

func (s *PostgresStore) ListSystemAlertWebhooks(ctx context.Context) ([]Webhook, error) {
	rows, err := s.db.Query(ctx, `SELECT `+webhookSQLColumns+` FROM webhooks WHERE system_alerts AND enabled ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list system alert webhooks: %w", err)
	}
	defer rows.Close()
	var out []Webhook
	for rows.Next() {
		var w Webhook
		if err := rows.Scan(webhookScanDest(&w)...); err != nil {
			return nil, fmt.Errorf("scan system alert webhook: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListPausedFeeds(ctx context.Context) ([]SystemAlertFeed, error) {
	// A 410 feed is manual_paused with last_error kept; a user pause clears
	// the error, so the error text separates the two.
	return s.queryAlertFeeds(ctx, `
SELECT id, title, last_error
FROM feeds
WHERE poll_paused OR (manual_paused AND COALESCE(last_error, '') <> '')
ORDER BY id`)
}

func (s *PostgresStore) ListSilentFeeds(ctx context.Context, silentAfter time.Duration) ([]SystemAlertFeed, error) {
	if silentAfter <= 0 {
		return nil, nil
	}
	return s.queryAlertFeeds(ctx, `
SELECT f.id, f.title, ''
FROM feeds f
WHERE `+adminFeedsSilentWhere(silentAfter)+`
ORDER BY f.id`)
}

func (s *PostgresStore) queryAlertFeeds(ctx context.Context, q string) ([]SystemAlertFeed, error) {
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list alert feeds: %w", err)
	}
	defer rows.Close()
	var out []SystemAlertFeed
	for rows.Next() {
		var f SystemAlertFeed
		if err := rows.Scan(&f.ID, &f.Title, &f.Error); err != nil {
			return nil, fmt.Errorf("scan alert feed: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListFailingWebhooks(ctx context.Context) ([]SystemAlertWebhook, error) {
	rows, err := s.db.Query(ctx, `
SELECT id, name, COALESCE(last_error, '')
FROM webhooks
WHERE enabled
  AND last_failed_at IS NOT NULL
  AND (last_sent_at IS NULL OR last_failed_at > last_sent_at)
ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list failing webhooks: %w", err)
	}
	defer rows.Close()
	var out []SystemAlertWebhook
	for rows.Next() {
		var w SystemAlertWebhook
		if err := rows.Scan(&w.ID, &w.Name, &w.LastError); err != nil {
			return nil, fmt.Errorf("scan failing webhook: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

var _ SystemAlertStore = (*PostgresStore)(nil)
