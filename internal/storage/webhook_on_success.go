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

const webhookSQLColumns = `id, user_id, filter_id, name, url, method, headers, body_template, secret, enabled, on_success_entry, kind, provider_config, system_alerts, digest_minutes, created_at, updated_at`

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
		&w.SystemAlerts,
		&w.DigestMinutes,
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

// MergeOnSuccessActions picks the strongest on_success_entry among several webhooks.
// delete > hash > mark_read > none.
func MergeOnSuccessActions(actions []string) string {
	hasDelete, hasHash, hasRead := false, false, false
	for _, raw := range actions {
		a, err := NormalizeWebhookOnSuccess(raw)
		if err != nil {
			continue
		}
		switch a {
		case WebhookOnSuccessDelete:
			hasDelete = true
		case WebhookOnSuccessHash:
			hasHash = true
		case WebhookOnSuccessMarkRead:
			hasRead = true
		}
	}
	if hasDelete {
		return WebhookOnSuccessDelete
	}
	if hasHash {
		return WebhookOnSuccessHash
	}
	if hasRead {
		return WebhookOnSuccessMarkRead
	}
	return WebhookOnSuccessNone
}

// WebhookDigestMaxMinutes bounds digest_minutes (one day).
const WebhookDigestMaxMinutes = 1440

// NormalizeDigestMinutes validates digest_minutes: 0 (off) or 1..1440.
func NormalizeDigestMinutes(n int) (int, error) {
	if n < 0 || n > WebhookDigestMaxMinutes {
		return 0, fmt.Errorf("digest_minutes must be between 0 and %d", WebhookDigestMaxMinutes)
	}
	return n, nil
}
