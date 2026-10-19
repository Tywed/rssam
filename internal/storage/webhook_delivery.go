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
	ID        int64
	Title     string
	StoreMode string
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
  e.id, e.feed_id, e.title, e.url, e.content, e.author, e.published_at, e.hash, ` + entryStatusExpr + `, e.created_at, e.updated_at,
  fd.id, fd.title, fd.store_mode,
  f.id, f.user_id, f.name, f.enabled, f.created_at, f.updated_at,
  fm.details
FROM webhook_logs wl
JOIN webhooks w ON w.id = wl.webhook_id
JOIN entries e ON e.id = wl.entry_id
JOIN feeds fd ON fd.id = e.feed_id
LEFT JOIN user_entries ue ON ue.entry_id = e.id AND ue.user_id = w.user_id
LEFT JOIN user_unread uu ON uu.entry_id = e.id AND uu.user_id = w.user_id
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
		&out.Feed.StoreMode,
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

// EntryWebhookOutcome reports, in one query, how many other deliveries of
// the entry are still pending or failed and the merged on_success action of
// every webhook that received it.
func (s *PostgresStore) EntryWebhookOutcome(ctx context.Context, entryID, excludeLogID int64) (incomplete int, action string, err error) {
	const q = `
SELECT count(*) FILTER (WHERE wl.id <> $2 AND wl.status IN ('pending', 'failed'))::int,
       COALESCE(array_agg(w.on_success_entry), '{}')
FROM webhook_logs wl
JOIN webhooks w ON w.id = wl.webhook_id
WHERE wl.entry_id = $1`
	var actions []string
	if err := s.db.QueryRow(ctx, q, entryID, excludeLogID).Scan(&incomplete, &actions); err != nil {
		return 0, WebhookOnSuccessNone, fmt.Errorf("entry webhook outcome: %w", err)
	}
	return incomplete, MergeOnSuccessActions(actions), nil
}

// DeleteRelayEntryAfterWebhook ends the life of a relay entry: once no other
// delivery of it is pending the row is deleted and its hash kept in
// feed_entry_dedup. The log rows keep their entry_id as a bare number.
// Returns false when another delivery still holds the entry.
func (s *PostgresStore) DeleteRelayEntryAfterWebhook(ctx context.Context, entryID, excludeLogID int64) (bool, error) {
	const q = `
WITH e AS (
  DELETE FROM entries
  WHERE id = $1
    AND NOT EXISTS (
      SELECT 1 FROM webhook_logs
      WHERE entry_id = $1 AND id <> $2 AND status IN ('pending', 'failed')
    )
  RETURNING feed_id, hash
), d AS (
  INSERT INTO feed_entry_dedup(feed_id, hash)
  SELECT feed_id, rssam_dedup_key(hash) FROM e WHERE hash <> ''
  ON CONFLICT (feed_id, hash) DO NOTHING
)
SELECT count(*) FROM e`
	var n int
	if err := s.db.QueryRow(ctx, q, entryID, excludeLogID).Scan(&n); err != nil {
		return false, fmt.Errorf("delete relay entry after webhook: %w", err)
	}
	return n > 0, nil
}

// MarkEntryRemovedKeepPayload removes the entry for the webhook owner only;
// other subscribers keep it.
func (s *PostgresStore) MarkEntryRemovedKeepPayload(ctx context.Context, userID, entryID int64) error {
	const q = `
WITH un AS (
  DELETE FROM user_unread WHERE user_id = $1 AND entry_id = $2
)
INSERT INTO user_entries(entry_id, user_id, status, sort_at)
SELECT e.id, $1, '` + EntryStatusRemoved + `', e.sort_at FROM entries e WHERE e.id = $2
ON CONFLICT (entry_id, user_id) DO UPDATE SET status = '` + EntryStatusRemoved + `', updated_at = now()
WHERE user_entries.status <> '` + EntryStatusRemoved + `'`
	_, err := s.db.Exec(ctx, q, userID, entryID)
	if err != nil {
		return fmt.Errorf("mark entry removed after webhook: %w", err)
	}
	return nil
}

func (s *PostgresStore) MarkEntryReadIfActive(ctx context.Context, userID, entryID int64) error {
	const q = `DELETE FROM user_unread WHERE user_id = $1 AND entry_id = $2`
	_, err := s.db.Exec(ctx, q, userID, entryID)
	if err != nil {
		return fmt.Errorf("mark entry read after webhook: %w", err)
	}
	return nil
}
