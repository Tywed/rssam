package ui

import (
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"rssam/internal/storage"
)

type adminFeedRowView struct {
	storage.AdminFeedRow
	Status string
}

type adminFeedDetailView struct {
	Feed        storage.Feed
	EntryCount  int
	UnreadCount int
	Status      string
	Job         *storage.AdminFeedJob
	OwnerName   string
	// PollLog is the last adminFeedPollLogLimit poll attempts, newest first;
	// PollLogEnabled is false when history is not wired at all.
	PollLog        []storage.FeedPollLogEntry
	PollLogEnabled bool
}

// adminFeedPollLogLimit is how many poll attempts the admin feed page shows.
const adminFeedPollLogLimit = 30

// silentAfter converts FEED_SILENT_DAYS to the window used by the store.
func (h *Handler) silentAfter() time.Duration {
	if h.cfg.FeedSilentDays <= 0 {
		return 0
	}
	return time.Duration(h.cfg.FeedSilentDays) * 24 * time.Hour
}

func classifyAdminFeedStatus(row storage.AdminFeedRow, now time.Time) string {
	if row.ManualPaused || row.PollPaused {
		return "paused"
	}
	if row.LastError != "" || row.ParsingErrorCount > 0 {
		return "errors"
	}
	if row.HasQueuedJob || row.NextCheckAt == nil || !row.NextCheckAt.After(now) {
		return "waiting"
	}
	return "ok"
}

func adminFeedStatusLabel(status string) string {
	switch status {
	case "ok":
		return "В норме"
	case "errors":
		return "Ошибка"
	case "paused":
		return "На паузе"
	case "waiting":
		return "Ждёт обновления"
	default:
		return status
	}
}

func adminFeedStatusClass(status string) string {
	switch status {
	case "ok":
		return "feed-status-ok"
	case "errors":
		return "feed-status-error"
	case "paused":
		return "feed-status-paused"
	case "waiting":
		return "feed-status-waiting"
	default:
		return "feed-status-muted"
	}
}

func truncateStr(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	const unit = 1024
	f := float64(n)
	for _, suffix := range []string{"KiB", "MiB", "GiB", "TiB"} {
		f /= unit
		if f < unit {
			return fmt.Sprintf("%.1f %s", f, suffix)
		}
	}
	return fmt.Sprintf("%.1f PiB", f/unit)
}

var adminFeedSortKeys = map[string]struct{}{
	"name": {}, "id": {}, "status": {}, "last_checked": {}, "next_check": {}, "last_entry": {},
	"errors": {}, "entries": {}, "unread": {},
}

func parseAdminFeedsSort(r *http.Request) (sortKey, order string) {
	sortKey = strings.TrimSpace(r.URL.Query().Get("sort"))
	if _, ok := adminFeedSortKeys[sortKey]; !ok {
		sortKey = "name"
	}
	order = strings.TrimSpace(r.URL.Query().Get("order"))
	if order != "desc" {
		order = "asc"
	}
	return sortKey, order
}

func parseAdminFeedsStatus(v string) string {
	switch strings.TrimSpace(v) {
	case "errors", "paused", "waiting", "ok", "silent", "all":
		return strings.TrimSpace(v)
	default:
		return "all"
	}
}

func adminFeedsListURL(status, sortKey, order string, page int) string {
	status = parseAdminFeedsStatus(status)
	if _, ok := adminFeedSortKeys[sortKey]; !ok {
		sortKey = "name"
	}
	if order != "desc" {
		order = "asc"
	}
	if page < 1 {
		page = 1
	}
	return fmt.Sprintf("/ui/admin/feeds?status=%s&sort=%s&order=%s&page=%d", status, sortKey, order, page)
}

func adminFeedsSortLink(status, sortKey, order, column string) string {
	nextOrder := "asc"
	if sortKey == column && order == "asc" {
		nextOrder = "desc"
	}
	return adminFeedsListURL(status, column, nextOrder, 1)
}

func adminFeedsSortIndicator(sortKey, order, column string) string {
	if sortKey != column {
		return ""
	}
	if order == "desc" {
		return " ↓"
	}
	return " ↑"
}

func (h *Handler) loadAdminFeedsDashboard(r *http.Request) (pageData, error) {
	data := h.baseData(r, "settings")
	data.SettingsSection = "admin_feeds"
	data.Title = "Состояние лент"

	if h.cfg.AdminFeeds == nil {
		return data, nil
	}

	ctx := r.Context()
	summary, err := h.cfg.AdminFeeds.AdminFeedSummary(ctx, h.silentAfter())
	if err != nil {
		return data, err
	}
	data.AdminFeedSummary = summary
	data.FeedSilentDays = h.cfg.FeedSilentDays

	jobs, err := h.cfg.AdminFeeds.PollFeedJobCounts(ctx)
	if err != nil {
		return data, err
	}
	data.PollFeedJobCounts = jobs
	data.WorkerAdvice = h.loadWorkerAdvice(ctx, summary, jobs)

	if size, err := h.cfg.AdminFeeds.EstimateDatabaseSize(ctx); err == nil {
		data.DBSizeBytes = size
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	data.MemAllocBytes = int64(ms.Alloc)

	status := parseAdminFeedsStatus(r.URL.Query().Get("status"))
	data.AdminFeedsFilter = status

	sortKey, order := parseAdminFeedsSort(r)
	limit, offset, page := parseAdminFeedsPage(r)
	rows, total, err := h.cfg.AdminFeeds.ListAdminFeedsPage(ctx, storage.AdminFeedsListParams{
		Status:      status,
		SortKey:     sortKey,
		Order:       order,
		Limit:       limit,
		Offset:      offset,
		SilentAfter: h.silentAfter(),
	})
	if err != nil {
		return data, err
	}

	now := time.Now()
	views := make([]adminFeedRowView, 0, len(rows))
	for _, row := range rows {
		views = append(views, adminFeedRowView{
			AdminFeedRow: row,
			Status:       classifyAdminFeedStatus(row, now),
		})
	}

	data.AdminFeedRows = views
	data.AdminFeedsSort = sortKey
	data.AdminFeedsOrder = order
	data.AdminFeedsTotal = total
	data.AdminFeedsPage = page
	data.AdminFeedsPageCount = adminFeedsPageCount(total, limit)
	return data, nil
}

func (h *Handler) handleAdminFeedsList(w http.ResponseWriter, r *http.Request) {
	data, err := h.loadAdminFeedsDashboard(r)
	if err != nil {
		http.Error(w, "admin feeds failed", http.StatusInternalServerError)
		return
	}
	if n := strings.TrimSpace(r.URL.Query().Get("hashed")); n != "" {
		data.FlashMsg = "Свёрнуто в хеш: " + n + " записей"
	}
	h.render(w, r, "admin_feeds", data)
}

func (h *Handler) handleAdminFeedShow(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.cfg.Feeds == nil {
		http.NotFound(w, r)
		return
	}

	feed, err := h.cfg.Feeds.GetFeedByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	data := h.baseData(r, "settings")
	data.SettingsSection = "admin_feeds"
	data.Title = feed.Title

	detail := adminFeedDetailView{Feed: feed}
	if h.cfg.AdminFeeds != nil {
		if row, err := h.cfg.AdminFeeds.GetAdminFeedRow(r.Context(), id); err == nil {
			detail.EntryCount = row.EntryCount
			detail.UnreadCount = row.UnreadCount
			detail.Status = classifyAdminFeedStatus(row, time.Now())
		}
		if job, err := h.cfg.AdminFeeds.GetPollFeedJob(r.Context(), id); err == nil {
			detail.Job = job
		}
	}
	if detail.Status == "" {
		detail.Status = classifyAdminFeedStatus(storage.AdminFeedRow{Feed: feed}, time.Now())
	}
	if h.cfg.Users != nil {
		if u, err := h.cfg.Users.GetUser(r.Context(), feed.UserID); err == nil {
			detail.OwnerName = u.Username
		}
	}
	if h.cfg.FeedPollLog != nil {
		detail.PollLogEnabled = true
		if log, err := h.cfg.FeedPollLog.ListFeedPollLog(r.Context(), id, adminFeedPollLogLimit); err == nil {
			detail.PollLog = log
		} else {
			h.log.Warn("list feed poll log failed", "feed_id", id, "err", err)
		}
	}
	data.AdminFeedDetail = detail
	if n := strings.TrimSpace(r.URL.Query().Get("hashed")); n != "" {
		data.FlashMsg = "Свёрнуто в хеш: " + n + " записей"
	}
	h.render(w, r, "admin_feed_detail", data)
}

func (h *Handler) handleAdminFeedRefresh(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := h.cfg.Feeds.GetFeedByID(r.Context(), id); err != nil {
		http.NotFound(w, r)
		return
	}
	if h.cfg.Refresher != nil {
		if _, err := h.cfg.Refresher.RefreshFeedManual(r.Context(), id); err != nil {
			h.failRedirect(w, r, adminFeedsRedirect(r), "admin feed refresh", err)
			return
		}
	}
	http.Redirect(w, r, adminFeedsRedirect(r), http.StatusFound)
}

func (h *Handler) handleAdminFeedUnpause(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := h.cfg.Feeds.GetFeedByID(r.Context(), id); err != nil {
		http.NotFound(w, r)
		return
	}
	// Clears manual_paused only; circuit breaker counters and poll_paused stay unchanged.
	if err := h.cfg.Feeds.SetFeedManualPaused(r.Context(), id, false); err != nil {
		h.failRedirect(w, r, adminFeedsRedirect(r), "feed unpause", err)
		return
	}
	http.Redirect(w, r, adminFeedsRedirect(r), http.StatusFound)
}

// handleAdminFeedPause sets manual_paused without resetting parsing_error_count or poll_paused.
func (h *Handler) handleAdminFeedPause(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := h.cfg.Feeds.GetFeedByID(r.Context(), id); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.cfg.Feeds.SetFeedManualPaused(r.Context(), id, true); err != nil {
		h.failRedirect(w, r, adminFeedsRedirect(r), "feed pause", err)
		return
	}
	http.Redirect(w, r, adminFeedsRedirect(r), http.StatusFound)
}

// handleAdminFeedResetCircuit clears circuit breaker state without polling.
func (h *Handler) handleAdminFeedResetCircuit(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := h.cfg.Feeds.GetFeedByID(r.Context(), id); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.cfg.Feeds.ResetFeedPollCircuit(r.Context(), id); err != nil {
		h.failRedirect(w, r, adminFeedsRedirect(r), "feed circuit reset", err)
		return
	}
	http.Redirect(w, r, adminFeedsRedirect(r), http.StatusFound)
}

func (h *Handler) handleAdminFeedsResetCircuits(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if _, err := h.cfg.Feeds.ResetErrorFeedPollCircuits(r.Context()); err != nil {
		h.failRedirect(w, r, adminFeedsRedirect(r), "feeds circuit reset", err)
		return
	}
	http.Redirect(w, r, adminFeedsRedirect(r), http.StatusFound)
}

func (h *Handler) handleAdminFeedDelete(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	feed, err := h.cfg.Feeds.GetFeedByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.cfg.Feeds.DeleteFeed(r.Context(), feed.UserID, id); err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, adminFeedsRedirect(r), http.StatusFound)
}

func (h *Handler) handleAdminFeedHashEntries(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	feed, err := h.cfg.Feeds.GetFeedByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	feedID := feed.ID
	n, err := h.collapseEntries(r, feed.UserID, &feedID, nil, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ref := adminFeedsRedirect(r)
	sep := "?"
	if strings.Contains(ref, "?") {
		sep = "&"
	}
	http.Redirect(w, r, ref+sep+"hashed="+fmt.Sprint(n), http.StatusFound)
}

func adminFeedsRedirect(r *http.Request) string {
	status := strings.TrimSpace(r.FormValue("status"))
	sortKey := strings.TrimSpace(r.FormValue("sort"))
	order := strings.TrimSpace(r.FormValue("order"))
	pageStr := strings.TrimSpace(r.FormValue("page"))
	if status != "" || sortKey != "" || order != "" || pageStr != "" {
		page := 1
		if n, err := strconv.Atoi(pageStr); err == nil && n > 0 {
			page = n
		}
		return adminFeedsListURL(status, sortKey, order, page)
	}
	return refererOr(r, "/ui/admin/feeds")
}
