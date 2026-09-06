package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ListWebhooks(ctx context.Context, userID int64, limit, offset int) ([]Webhook, int, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
	}
	if offset < 0 {
		offset = 0
	}
	q := `
SELECT ` + webhookSQLColumns + `, count(*) OVER()
FROM webhooks
WHERE user_id = $1
ORDER BY id DESC
LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, q, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list webhooks: %w", err)
	}
	defer rows.Close()

	out := make([]Webhook, 0, limit)
	total := 0
	for rows.Next() {
		var w Webhook
		dest := append(webhookScanDest(&w), &total)
		if err := rows.Scan(dest...); err != nil {
			return nil, 0, fmt.Errorf("scan webhooks: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate webhooks: %w", err)
	}
	return out, total, nil
}

func (s *PostgresStore) ListEnabledWebhooks(ctx context.Context, userID int64, limit int) ([]Webhook, error) {
	if limit <= 0 {
		limit = 1000
	}
	q := `
SELECT ` + webhookSQLColumns + `
FROM webhooks
WHERE user_id = $1 AND enabled = TRUE
ORDER BY id DESC
LIMIT $2`
	rows, err := s.db.Query(ctx, q, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list enabled webhooks: %w", err)
	}
	defer rows.Close()

	out := make([]Webhook, 0, 32)
	for rows.Next() {
		var w Webhook
		if err := rows.Scan(webhookScanDest(&w)...); err != nil {
			return nil, fmt.Errorf("scan enabled webhooks: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled webhooks: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) CreateWebhook(ctx context.Context, params CreateWebhookParams) (Webhook, error) {
	kind, displayURL, cfg, err := ResolveWebhookWrite(params.Kind, params.URL, params.ProviderConfig)
	if err != nil {
		return Webhook{}, err
	}

	method := strings.ToUpper(strings.TrimSpace(params.Method))
	if method == "" {
		method = "POST"
	}
	headers := params.Headers
	if len(headers) == 0 {
		headers = []byte(`{}`)
	}
	if !json.Valid(headers) {
		return Webhook{}, errors.New("headers must be valid JSON object")
	}
	onSuccess, err := NormalizeWebhookOnSuccess(params.OnSuccessEntry)
	if err != nil {
		return Webhook{}, err
	}
	name, err := NormalizeWebhookName(params.Name)
	if err != nil {
		return Webhook{}, err
	}
	secret := params.Secret
	if kind != WebhookKindHTTP {
		secret = ""
	}

	var out Webhook
	q := `
INSERT INTO webhooks(user_id, filter_id, name, url, method, headers, body_template, secret, enabled, on_success_entry, kind, provider_config)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11, $12::jsonb)
RETURNING ` + webhookSQLColumns
	if err := s.db.QueryRow(ctx, q,
		params.UserID,
		params.FilterID,
		name,
		displayURL,
		method,
		headers,
		params.BodyTemplate,
		secret,
		params.Enabled,
		onSuccess,
		kind,
		cfg,
	).Scan(webhookScanDest(&out)...); err != nil {
		return Webhook{}, fmt.Errorf("create webhook: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) GetWebhook(ctx context.Context, userID int64, id int64) (Webhook, error) {
	q := `
SELECT ` + webhookSQLColumns + `
FROM webhooks
WHERE id = $1 AND user_id = $2`
	var out Webhook
	if err := s.db.QueryRow(ctx, q, id, userID).Scan(webhookScanDest(&out)...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Webhook{}, ErrNotFound
		}
		return Webhook{}, fmt.Errorf("get webhook: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) UpdateWebhook(ctx context.Context, params UpdateWebhookParams) (Webhook, error) {
	method := strings.ToUpper(strings.TrimSpace(params.Method))
	if method == "" {
		method = "POST"
	}

	headers := params.Headers
	if len(headers) == 0 {
		headers = []byte(`{}`)
	}
	if !json.Valid(headers) {
		return Webhook{}, errors.New("headers must be valid JSON object")
	}
	onSuccess, err := NormalizeWebhookOnSuccess(params.OnSuccessEntry)
	if err != nil {
		return Webhook{}, err
	}

	var out Webhook
	err = withTx(ctx, s.db, func(tx pgx.Tx) error {
		var existingKind string
		var existingCfg []byte
		var existingName string
		secret := ""
		if err := tx.QueryRow(ctx, `SELECT kind, provider_config, secret, name FROM webhooks WHERE id = $1 AND user_id = $2`, params.ID, params.UserID).Scan(&existingKind, &existingCfg, &secret, &existingName); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("load webhook: %w", err)
		}
		cfgIn := params.ProviderConfig
		kindHint := params.Kind
		if strings.TrimSpace(kindHint) == "" {
			kindHint = existingKind
		}
		kindNorm, err := NormalizeWebhookKind(kindHint)
		if err != nil {
			return err
		}
		if kindNorm == WebhookKindTelegram {
			cfgIn = MergeTelegramToken(cfgIn, existingCfg)
		}
		kind, displayURL, cfg, err := ResolveWebhookWrite(kindNorm, params.URL, cfgIn)
		if err != nil {
			return err
		}
		if params.Secret != nil {
			secret = *params.Secret
		}
		if kind != WebhookKindHTTP {
			secret = ""
		}
		name := existingName
		if params.Name != nil {
			n, nerr := NormalizeWebhookName(*params.Name)
			if nerr != nil {
				return nerr
			}
			name = n
		}

		q := `
UPDATE webhooks
SET filter_id = $2,
    name = $3,
    url = $4,
    method = $5,
    headers = $6::jsonb,
    body_template = $7,
    secret = $8,
    enabled = $9,
    on_success_entry = $10,
    kind = $11,
    provider_config = $12::jsonb,
    updated_at = now()
WHERE id = $1 AND user_id = $13
RETURNING ` + webhookSQLColumns
		if err := tx.QueryRow(ctx, q,
			params.ID,
			params.FilterID,
			name,
			displayURL,
			method,
			headers,
			params.BodyTemplate,
			secret,
			params.Enabled,
			onSuccess,
			kind,
			cfg,
			params.UserID,
		).Scan(webhookScanDest(&out)...); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("update webhook: %w", err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Webhook{}, ErrNotFound
		}
		return Webhook{}, fmt.Errorf("update webhook: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) SetWebhookEnabled(ctx context.Context, userID, id int64, enabled bool) error {
	const q = `
UPDATE webhooks
SET enabled = $3,
    updated_at = now()
WHERE id = $1 AND user_id = $2`
	cmd, err := s.db.Exec(ctx, q, id, userID, enabled)
	if err != nil {
		return fmt.Errorf("set webhook enabled: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) DeleteWebhook(ctx context.Context, userID int64, id int64) error {
	cmd, err := s.db.Exec(ctx, `DELETE FROM webhooks WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete webhook: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) EnqueueWebhookLogs(ctx context.Context, webhookIDs []int64, entryID int64) error {
	if entryID <= 0 || len(webhookIDs) == 0 {
		return nil
	}
	const q = `
INSERT INTO webhook_logs(webhook_id, entry_id, status, attempt, next_retry_at)
SELECT x.webhook_id, $2, 'pending', 0, now()
FROM unnest($1::bigint[]) AS x(webhook_id)
ON CONFLICT (webhook_id, entry_id) DO NOTHING`
	_, err := s.db.Exec(ctx, q, webhookIDs, entryID)
	if err != nil {
		return fmt.Errorf("enqueue webhook logs: %w", err)
	}
	return nil
}

func (s *PostgresStore) ClaimDueWebhookLogs(ctx context.Context, limit int) ([]WebhookLog, error) {
	if limit <= 0 {
		return nil, nil
	}
	const q = `
WITH due AS (
  SELECT id
  FROM webhook_logs
  WHERE status IN ('pending','failed')
    AND (next_retry_at IS NULL OR next_retry_at <= now())
  ORDER BY COALESCE(next_retry_at, created_at) ASC, id ASC
  LIMIT $1
  FOR UPDATE SKIP LOCKED
)
UPDATE webhook_logs
SET next_retry_at = now() + interval '30 seconds',
    updated_at = now()
FROM due
WHERE webhook_logs.id = due.id
RETURNING
  webhook_logs.id,
  webhook_logs.webhook_id,
  webhook_logs.entry_id,
  webhook_logs.status,
  webhook_logs.attempt,
  webhook_logs.next_retry_at,
  webhook_logs.last_status_code,
  webhook_logs.last_error,
  webhook_logs.response_snippet,
  webhook_logs.created_at,
  webhook_logs.updated_at`
	rows, err := s.db.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("claim due webhook logs: %w", err)
	}
	defer rows.Close()

	out := make([]WebhookLog, 0, limit)
	for rows.Next() {
		var l WebhookLog
		if err := rows.Scan(
			&l.ID,
			&l.WebhookID,
			&l.EntryID,
			&l.Status,
			&l.Attempt,
			&l.NextRetryAt,
			&l.LastStatusCode,
			&l.LastError,
			&l.ResponseSnippet,
			&l.CreatedAt,
			&l.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan claimed webhook log: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimed webhook logs: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) MarkWebhookLogSent(ctx context.Context, logID int64, attempt int, statusCode int, responseSnippet string) error {
	const q = `
UPDATE webhook_logs
SET status = 'sent',
    last_status_code = $2,
    last_error = NULL,
    response_snippet = $3,
    attempt = $4,
    next_retry_at = NULL,
    updated_at = now()
WHERE id = $1`
	_, err := s.db.Exec(ctx, q, logID, statusCode, responseSnippet, attempt)
	if err != nil {
		return fmt.Errorf("mark webhook log sent: %w", err)
	}
	return nil
}

func (s *PostgresStore) MarkWebhookLogFailed(ctx context.Context, logID int64, statusCode *int, errMsg string, responseSnippet string, attempt int, nextRetryAt *time.Time, dead bool) error {
	status := "failed"
	if dead {
		status = "dead"
	}
	const q = `
UPDATE webhook_logs
SET status = $2,
    last_status_code = $3,
    last_error = $4,
    response_snippet = $5,
    attempt = $6,
    next_retry_at = $7,
    updated_at = now()
WHERE id = $1`
	_, err := s.db.Exec(ctx, q, logID, status, statusCode, errMsg, responseSnippet, attempt, nextRetryAt)
	if err != nil {
		return fmt.Errorf("mark webhook log failed: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListWebhookLogs(ctx context.Context, webhookID int64, limit, offset int) ([]WebhookLog, int, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
	}
	if offset < 0 {
		offset = 0
	}
	const q = `
SELECT id, webhook_id, entry_id, status, attempt, next_retry_at, last_status_code, last_error, response_snippet, created_at, updated_at, count(*) OVER()
FROM webhook_logs
WHERE webhook_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, q, webhookID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list webhook logs: %w", err)
	}
	defer rows.Close()

	out := make([]WebhookLog, 0, limit)
	total := 0
	for rows.Next() {
		var l WebhookLog
		if err := rows.Scan(
			&l.ID,
			&l.WebhookID,
			&l.EntryID,
			&l.Status,
			&l.Attempt,
			&l.NextRetryAt,
			&l.LastStatusCode,
			&l.LastError,
			&l.ResponseSnippet,
			&l.CreatedAt,
			&l.UpdatedAt,
			&total,
		); err != nil {
			return nil, 0, fmt.Errorf("scan webhook log: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate webhook logs: %w", err)
	}
	return out, total, nil
}

func (s *PostgresStore) RetryWebhookLogNow(ctx context.Context, userID, logID int64) error {
	const q = `
UPDATE webhook_logs l
SET status = 'pending',
    next_retry_at = now(),
    updated_at = now()
FROM webhooks w
WHERE l.id = $1 AND l.webhook_id = w.id AND w.user_id = $2 AND l.status <> 'sent'`
	cmd, err := s.db.Exec(ctx, q, logID, userID)
	if err != nil {
		return fmt.Errorf("retry webhook log: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Ensure PostgresStore implements interfaces at compile time.
var _ WebhookStore = (*PostgresStore)(nil)
var _ WebhookLogStore = (*PostgresStore)(nil)
