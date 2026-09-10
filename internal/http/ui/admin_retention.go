package ui

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rssam/internal/envfile"
	"rssam/internal/storage"
)

// Retention limits mirror internal/config validation.
const (
	retentionDaysMax   = 3650
	cleanupIntervalMin = time.Minute
	cleanupIntervalMax = 7 * 24 * time.Hour
)

// retentionForm is the parsed and validated admin form.
type retentionForm struct {
	RemovedRetentionDays     int
	WebhookLogRetentionDays  int
	FilterMatchRetentionDays int
	FeedPollLogRetentionDays int
	AuditLogRetentionDays    int
	CleanupInterval          time.Duration
}

// parseRetentionForm validates the retention form values. Rules match
// internal/config: removed/webhook-log retention 1–3650 days, filter-match,
// feed-poll-log and audit-log retention 0–3650 (0 disables), cleanup interval 1m–168h (a Go duration such
// as "24h", "12h30m" or "90m"; a bare number is treated as hours).
func parseRetentionForm(get func(string) string) (retentionForm, error) {
	var f retentionForm
	var err error

	if f.RemovedRetentionDays, err = parseRetentionDays(get("removed_retention_days"), 1); err != nil {
		return f, fmt.Errorf("REMOVED_RETENTION_DAYS: %w", err)
	}
	if f.WebhookLogRetentionDays, err = parseRetentionDays(get("webhook_log_retention_days"), 1); err != nil {
		return f, fmt.Errorf("WEBHOOK_LOG_RETENTION_DAYS: %w", err)
	}
	if f.FilterMatchRetentionDays, err = parseRetentionDays(get("filter_match_retention_days"), 0); err != nil {
		return f, fmt.Errorf("FILTER_MATCH_RETENTION_DAYS: %w", err)
	}
	if f.FeedPollLogRetentionDays, err = parseRetentionDays(get("feed_poll_log_retention_days"), 0); err != nil {
		return f, fmt.Errorf("FEED_POLL_LOG_RETENTION_DAYS: %w", err)
	}
	if f.AuditLogRetentionDays, err = parseRetentionDays(get("audit_log_retention_days"), 0); err != nil {
		return f, fmt.Errorf("AUDIT_LOG_RETENTION_DAYS: %w", err)
	}
	if f.CleanupInterval, err = parseCleanupInterval(get("cleanup_interval")); err != nil {
		return f, fmt.Errorf("CLEANUP_INTERVAL: %w", err)
	}
	return f, nil
}

func parseRetentionDays(raw string, minDays int) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("must be an integer number of days")
	}
	if n < minDays || n > retentionDaysMax {
		return 0, fmt.Errorf("must be between %d and %d", minDays, retentionDaysMax)
	}
	return n, nil
}

func parseCleanupInterval(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("must be set")
	}
	var d time.Duration
	if n, err := strconv.Atoi(raw); err == nil {
		d = time.Duration(n) * time.Hour
	} else {
		d, err = time.ParseDuration(raw)
		if err != nil {
			return 0, fmt.Errorf("must be a duration like 24h or 90m")
		}
	}
	if d < cleanupIntervalMin || d > cleanupIntervalMax {
		return 0, fmt.Errorf("must be between %s and %s", cleanupIntervalMin, cleanupIntervalMax)
	}
	return d, nil
}

// formatCleanupInterval renders a duration the way CLEANUP_INTERVAL expects
// it ("24h", "12h30m", "90m"), dropping zero components.
func formatCleanupInterval(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	d = d.Round(time.Minute)
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	switch {
	case m == 0:
		return fmt.Sprintf("%dh", h)
	case h == 0:
		return fmt.Sprintf("%dm", m)
	default:
		return fmt.Sprintf("%dh%dm", h, m)
	}
}

// handleAdminRetentionSave writes the retention keys into .env; the running
// process keeps its current values until restart (same contract as the
// worker pool form).
func (h *Handler) handleAdminRetentionSave(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	f, err := parseRetentionForm(r.FormValue)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	keys := map[string]string{
		"REMOVED_RETENTION_DAYS":       strconv.Itoa(f.RemovedRetentionDays),
		"WEBHOOK_LOG_RETENTION_DAYS":   strconv.Itoa(f.WebhookLogRetentionDays),
		"FILTER_MATCH_RETENTION_DAYS":  strconv.Itoa(f.FilterMatchRetentionDays),
		"FEED_POLL_LOG_RETENTION_DAYS": strconv.Itoa(f.FeedPollLogRetentionDays),
		"AUDIT_LOG_RETENTION_DAYS":     strconv.Itoa(f.AuditLogRetentionDays),
		"CLEANUP_INTERVAL":             formatCleanupInterval(f.CleanupInterval),
	}
	if err := envfile.SetKeys(h.envPath(), keys); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditEnvUpdate, "", 0, map[string]any{"keys": keys})
	http.Redirect(w, r, "/ui/admin/system?saved=1", http.StatusFound)
}

// handleAdminRetentionCleanupNow runs one retention pass right away with the
// values the process is currently running with.
func (h *Handler) handleAdminRetentionCleanupNow(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.cfg.RunRetentionCleanup == nil {
		http.Error(w, "очистка недоступна в этом процессе", http.StatusBadRequest)
		return
	}
	res, err := h.cfg.RunRetentionCleanup(r.Context())
	if err != nil {
		http.Error(w, "retention cleanup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	total := res.RemovedEntries + res.WebhookLogs + res.FilterMatches + res.FeedPollLog + res.AuditLog + res.FeedEntries + res.FeedEntryDedup + res.ExpiredSessions
	h.cfg.Audit.Record(r, storage.AuditRetentionCleanup, "", 0, map[string]any{"deleted": total})
	http.Redirect(w, r, "/ui/admin/system?cleaned="+strconv.FormatInt(total, 10), http.StatusFound)
}
