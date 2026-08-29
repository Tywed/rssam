package storage

import (
	"fmt"
	"strings"
)

const (
	WebhookOnSuccessNone     = "none"
	WebhookOnSuccessHash     = "hash"
	WebhookOnSuccessDelete   = "delete"
	WebhookOnSuccessMarkRead = "mark_read"
)

const webhookSQLColumns = `id, user_id, filter_id, name, url, method, headers, body_template, secret, enabled, on_success_entry, kind, provider_config, created_at, updated_at`

const webhookNameMaxLen = 80

func NormalizeWebhookName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len([]rune(s)) > webhookNameMaxLen {
		return "", fmt.Errorf("name must be at most %d characters", webhookNameMaxLen)
	}
	return s, nil
}

func webhookScanDest(w *Webhook) []any {
	return []any{
		&w.ID,
		&w.UserID,
		&w.FilterID,
		&w.Name,
		&w.URL,
		&w.Method,
		&w.Headers,
		&w.BodyTemplate,
		&w.Secret,
		&w.Enabled,
		&w.OnSuccessEntry,
		&w.Kind,
		&w.ProviderConfig,
		&w.CreatedAt,
		&w.UpdatedAt,
	}
}

// NormalizeWebhookOnSuccess returns a valid on_success_entry value.
func NormalizeWebhookOnSuccess(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return WebhookOnSuccessNone, nil
	}
	switch s {
	case WebhookOnSuccessNone, WebhookOnSuccessHash, WebhookOnSuccessDelete, WebhookOnSuccessMarkRead:
		return s, nil
	default:
		return "", fmt.Errorf("on_success_entry must be none, hash, delete, or mark_read")
	}
}
