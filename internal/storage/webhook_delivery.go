package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// WebhookFeed is the feed identity included in webhook payloads (not the full Feed row).
type WebhookFeed struct {
	ID    int64
	Title string
}

type WebhookDeliveryContext struct {
	Webhook      Webhook
	Entry        Entry
	Feed         WebhookFeed
	Filter       *Filter
	MatchDetails []byte
}

// LoadWebhookDeliveryContext loads the webhook config + entry + (optional) filter match context for a delivery log.
func (s *PostgresStore) LoadWebhookDeliveryContext(ctx context.Context, logID int64) (WebhookDeliveryContext, error) {
	const q = `
SELECT
  w.id, w.user_id, w.name, w.url, w.method, w.headers, w.body_template, w.secret, w.enabled, w.on_success_entry, w.kind, w.provider_config, w.system_alerts, w.digest_minutes, w.created_at, w.updated_at,
  e.id, e.feed_id, e.title, e.url, e.content, e.author, e.published_at, e.hash, COALESCE(ue.status, 'unread'), e.created_at, e.updated_at,
  fd.id, fd.title,
  f.id, f.user_id, f.name, f.enabled, f.created_at, f.updated_at,
  fm.details
FROM webhook_logs wl
JOIN webhooks w ON w.id = wl.webhook_id
JOIN entries e ON e.id = wl.entry_id
JOIN feeds fd ON fd.id = e.feed_id
LEFT JOIN user_entries ue ON ue.entry_id = e.id AND ue.user_id = w.user_id
LEFT JOIN LATERAL (
  SELECT fm.filter_id, fm.details
  FROM filter_matches fm
  WHERE fm.entry_id = wl.entry_id
  ORDER BY fm.matched_at DESC, fm.id DESC
  LIMIT 1
) fm ON TRUE
LEFT JOIN filters f ON f.id = fm.filter_id
WHERE wl.id = $1`

	var out WebhookDeliveryContext
	var (
		fID      *int64
		fUserID  *int64
		fName    *string
		fEnabled *bool
		fCreated *time.Time
		fUpdated *time.Time
		details  []byte
	)
	err := s.db.QueryRow(ctx, q, logID).Scan(
		&out.Webhook.ID,
		&out.Webhook.UserID,
		&out.Webhook.Name,
		&out.Webhook.URL,
		&out.Webhook.Method,
		&out.Webhook.Headers,
		&out.Webhook.BodyTemplate,
		&out.Webhook.Secret,
		&out.Webhook.Enabled,
		&out.Webhook.OnSuccessEntry,
		&out.Webhook.Kind,
		&out.Webhook.ProviderConfig,
		&out.Webhook.SystemAlerts,
		&out.Webhook.DigestMinutes,
		&out.Webhook.CreatedAt,
		&out.Webhook.UpdatedAt,
		&out.Entry.ID,
		&out.Entry.FeedID,
		&out.Entry.Title,
		&out.Entry.URL,
		&out.Entry.Content,
		&out.Entry.Author,
		&out.Entry.PublishedAt,
		&out.Entry.Hash,
		&out.Entry.Status,
		&out.Entry.CreatedAt,
		&out.Entry.UpdatedAt,
		&out.Feed.ID,
		&out.Feed.Title,
		&fID,
		&fUserID,
		&fName,
		&fEnabled,
		&fCreated,
		&fUpdated,
		&details,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WebhookDeliveryContext{}, ErrNotFound
		}
		return WebhookDeliveryContext{}, fmt.Errorf("load webhook delivery context: %w", err)
	}

	if fID != nil {
		f := Filter{
			ID:      *fID,
			UserID:  derefInt64(fUserID),
			Name:    derefString(fName),
			Enabled: derefBool(fEnabled),
		}
		if fCreated != nil {
			f.CreatedAt = *fCreated
		}
		if fUpdated != nil {
			f.UpdatedAt = *fUpdated
		}
		out.Filter = &f
	}
	out.MatchDetails = details
	return out, nil
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func derefBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}
func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func (s *PostgresStore) CountIncompleteWebhookLogs(ctx context.Context, entryID, excludeLogID int64) (int, error) {
	const q = `
SELECT COUNT(*)::int
FROM webhook_logs
WHERE entry_id = $1
  AND id <> $2
  AND status IN ('pending', 'failed')`
	var n int
	if err := s.db.QueryRow(ctx, q, entryID, excludeLogID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count incomplete webhook logs: %w", err)
	}
	return n, nil
}

// MarkEntryRemovedKeepPayload removes the entry for the webhook owner only;
// other subscribers keep it.
func (s *PostgresStore) MarkEntryRemovedKeepPayload(ctx context.Context, userID, entryID int64) error {
	const q = `
UPDATE user_entries
SET status = $3,
    updated_at = now()
WHERE user_id = $1 AND entry_id = $2 AND status <> $3`
	_, err := s.db.Exec(ctx, q, userID, entryID, EntryStatusRemoved)
	if err != nil {
		return fmt.Errorf("mark entry removed after webhook: %w", err)
	}
	return nil
}

func (s *PostgresStore) MarkEntryReadIfActive(ctx context.Context, userID, entryID int64) error {
	const q = `
UPDATE user_entries
SET status = $3,
    updated_at = now()
WHERE user_id = $1 AND entry_id = $2 AND status = $4`
	_, err := s.db.Exec(ctx, q, userID, entryID, EntryStatusRead, EntryStatusUnread)
	if err != nil {
		return fmt.Errorf("mark entry read after webhook: %w", err)
	}
	return nil
}

func (s *PostgresStore) EntryOnSuccessAction(ctx context.Context, entryID int64) (string, error) {
	const q = `
SELECT w.on_success_entry
FROM webhook_logs wl
JOIN webhooks w ON w.id = wl.webhook_id
WHERE wl.entry_id = $1`
	rows, err := s.db.Query(ctx, q, entryID)
	if err != nil {
		return WebhookOnSuccessNone, fmt.Errorf("entry on_success actions: %w", err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return WebhookOnSuccessNone, fmt.Errorf("scan on_success: %w", err)
		}
		actions = append(actions, a)
	}
	if err := rows.Err(); err != nil {
		return WebhookOnSuccessNone, err
	}
	return MergeOnSuccessActions(actions), nil
}
