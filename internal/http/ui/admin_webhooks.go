package ui

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"rssam/internal/storage"
)

type adminWebhookRowView struct {
	storage.AdminWebhookRow
	Status string
}

type adminWebhookStatusCounts struct {
	OK       int
	Queue    int
	Retrying int
	Error    int
	Disabled int
	Idle     int
}

// adminWebhookStatuses lists the dashboard filter values in display order.
var adminWebhookStatuses = []string{"ok", "queue", "retrying", "error", "disabled", "idle"}

func isAdminWebhookStatus(status string) bool {
	for _, s := range adminWebhookStatuses {
		if s == status {
			return true
		}
	}
	return false
}

func webhookLabel(name, url string, id int64) string {
	if s := strings.TrimSpace(name); s != "" {
		return s
	}
	if s := strings.TrimSpace(url); s != "" {
		return s
	}
	if id > 0 {
		return fmt.Sprintf("#%d", id)
	}
	return "webhook"
}

func webhookKindLabel(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case storage.WebhookKindTelegram:
		return "Telegram"
	case storage.WebhookKindMax:
		return "Max"
	default:
		return "HTTP"
	}
}

// classifyAdminWebhookStatus derives the dashboard status of a webhook.
//
//	disabled  — paused by the user
//	error     — the most recent final outcome was a failure (a delivery gave
//	            up) and no successful delivery happened after it
//	queue     — deliveries are due right now
//	retrying  — deliveries failed transiently and wait for the next attempt
//	idle      — nothing was ever delivered (or the counters were reset)
//	ok        — the last final outcome was a success
//
// A success after a failure therefore returns the webhook to "ok"; the
// failure stays visible in FailedCount/LastError but no longer defines the
// status.
func classifyAdminWebhookStatus(row storage.AdminWebhookRow) string {
	if !row.Enabled {
		return "disabled"
	}
	if webhookLastOutcomeFailed(row) {
		return "error"
	}
	if row.QueueDueCount > 0 {
		return "queue"
	}
	if row.RetryingCount > 0 {
		return "retrying"
	}
	if row.SentCount == 0 {
		return "idle"
	}
	return "ok"
}

// webhookLastOutcomeFailed reports whether the last *final* delivery outcome
// (sent vs dead) of the webhook was a failure.
func webhookLastOutcomeFailed(row storage.AdminWebhookRow) bool {
	if row.LastFailedAt == nil {
		return false
	}
	if row.LastSentAt == nil {
		return true
	}
	return row.LastFailedAt.After(*row.LastSentAt)
}

func countAdminWebhookStatuses(rows []adminWebhookRowView) adminWebhookStatusCounts {
	var counts adminWebhookStatusCounts
	for _, row := range rows {
		switch row.Status {
		case "ok":
			counts.OK++
		case "queue":
			counts.Queue++
		case "retrying":
			counts.Retrying++
		case "error":
			counts.Error++
		case "disabled":
			counts.Disabled++
		case "idle":
			counts.Idle++
		}
	}
	return counts
}

func filterAdminWebhookRows(rows []adminWebhookRowView, status string) []adminWebhookRowView {
	if status == "" || status == "all" {
		return rows
	}
	out := make([]adminWebhookRowView, 0, len(rows))
	for _, row := range rows {
		if row.Status == status {
			out = append(out, row)
		}
	}
	return out
}

func adminWebhookStatusLabel(status string) string {
	switch status {
	case "ok":
		return "В норме"
	case "queue":
		return "В очереди"
	case "retrying":
		return "Повторы"
	case "error":
		return "Ошибки"
	case "disabled":
		return "На паузе"
	case "idle":
		return "Без отправок"
	default:
		return status
	}
}

func adminWebhookStatusClass(status string) string {
	switch status {
	case "ok":
		return "feed-status-ok"
	case "queue", "retrying":
		return "feed-status-waiting"
	case "error":
		return "feed-status-error"
	case "disabled", "idle":
		return "feed-status-muted"
	default:
		return "feed-status-muted"
	}
}

func webhookLogStatusLabel(status string) string {
	switch status {
	case "pending":
		return "Ожидает"
	case "sent":
		return "Отправлено"
	case "failed":
		return "Ошибка, будет повтор"
	case "dead":
		return "Не доставлено"
	default:
		return status
	}
}

func webhookLogStatusClass(status string) string {
	switch status {
	case "sent":
		return "feed-status-ok"
	case "pending":
		return "feed-status-waiting"
	case "failed":
		return "feed-status-waiting"
	case "dead":
		return "feed-status-error"
	default:
		return "feed-status-muted"
	}
}

func webhookTriggerLabel(source, filterName string) string {
	switch source {
	case "filter":
		if strings.TrimSpace(filterName) != "" {
			return "Фильтр: " + filterName
		}
		return "Фильтр"
	case "feed":
		return "Лента"
	default:
		return "—"
	}
}

func classifyWebhookError(errMsg string) string {
	msg := strings.ToLower(strings.TrimSpace(errMsg))
	if msg == "" {
		return ""
	}
	switch {
	case strings.Contains(msg, "non-2xx"):
		return "HTTP"
	case strings.Contains(msg, "deadline exceeded"), strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"), strings.Contains(msg, "timeout"):
		return "Сеть"
	case strings.Contains(msg, "ssrf"), strings.Contains(msg, "private network"):
		return "SSRF"
	case strings.Contains(msg, "body_template"), strings.Contains(msg, "template"):
		return "Шаблон"
	case strings.Contains(msg, "disabled"):
		return "Выключен"
	default:
		return "Другое"
	}
}

func parseWebhookLogsFilter(r *http.Request) (status, period string) {
	status = strings.TrimSpace(r.URL.Query().Get("status"))
	switch status {
	case "pending", "sent", "failed", "dead":
	default:
		status = ""
	}
	period = strings.TrimSpace(r.URL.Query().Get("period"))
	switch period {
	case "24h", "7d":
	default:
		period = ""
	}
	return status, period
}

func webhookLogsSince(period string) *time.Time {
	switch period {
	case "24h":
		t := time.Now().UTC().Add(-24 * time.Hour)
		return &t
	case "7d":
		t := time.Now().UTC().Add(-7 * 24 * time.Hour)
		return &t
	default:
		return nil
	}
}

func webhooksFilterLink(status string) string {
	if status == "" || status == "all" {
		return "/ui/webhooks"
	}
	return "/ui/webhooks?status=" + status
}

func webhookLogsFilterLink(webhookID int64, status, period string) string {
	var parts []string
	if status != "" {
		parts = append(parts, "status="+status)
	}
	if period != "" {
		parts = append(parts, "period="+period)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("/ui/webhooks/%d/logs", webhookID)
	}
	return fmt.Sprintf("/ui/webhooks/%d/logs?%s", webhookID, strings.Join(parts, "&"))
}

// webhookSuccessRate renders sent/(sent+failed) from the persistent counters.
func webhookSuccessRate(sent, failed int) string {
	total := sent + failed
	if total == 0 {
		return "—"
	}
	rate := float64(sent) * 100 / float64(total)
	return fmt.Sprintf("%.0f%%", rate)
}
