package ui

import (
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"time"

	"rssam/internal/storage"
)

const adminFeedsLimit = 10000
const adminFeedsDetailLookupLimit = 10000

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

func filterAdminFeedRows(rows []adminFeedRowView, status string) []adminFeedRowView {
	if status == "" || status == "all" {
		return rows
	}
	out := make([]adminFeedRowView, 0, len(rows))
	for _, row := range rows {
		if row.Status == status {
			out = append(out, row)
		}
	}
	return out
}

var adminFeedSortKeys = map[string]struct{}{
	"name": {}, "id": {}, "status": {}, "last_checked": {}, "next_check": {},
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

func adminFeedsSortLink(status, sortKey, order, column string) string {
	nextOrder := "asc"
	if sortKey == column && order == "asc" {
		nextOrder = "desc"
	}
	q := fmt.Sprintf("/ui/admin/feeds?status=%s&sort=%s&order=%s&page=1", status, column, nextOrder)
	return q
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

func sortAdminFeedRows(rows []adminFeedRowView, sortKey, order string) {
	desc := order == "desc"
	sort.SliceStable(rows, func(i, j int) bool {
		less := compareAdminFeedRows(rows[i], rows[j], sortKey)
		if desc {
			return !less
		}
		return less
	})
}

func compareAdminFeedRows(a, b adminFeedRowView, sortKey string) bool {
	switch sortKey {
	case "id":
		return a.ID < b.ID
	case "status":
		if a.Status != b.Status {
			return a.Status < b.Status
		}
		return a.ID < b.ID
	case "last_checked":
		return timePtrBefore(a.LastCheckedAt, b.LastCheckedAt, a.ID, b.ID)
	case "next_check":
		return timePtrBefore(a.NextCheckAt, b.NextCheckAt, a.ID, b.ID)
	case "errors":
		if a.ParsingErrorCount != b.ParsingErrorCount {
			return a.ParsingErrorCount < b.ParsingErrorCount
		}
		return a.ID < b.ID
	case "entries":
		if a.EntryCount != b.EntryCount {
			return a.EntryCount < b.EntryCount
		}
		return a.ID < b.ID
	case "unread":
		if a.UnreadCount != b.UnreadCount {
			return a.UnreadCount < b.UnreadCount
		}
		return a.ID < b.ID
	default:
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.ID < b.ID
	}
}

func timePtrBefore(a, b *time.Time, idA, idB int64) bool {
	switch {
	case a == nil && b == nil:
		return idA < idB
	case a == nil:
		return true
	case b == nil:
		return false
	default:
		if !a.Equal(*b) {
			return a.Before(*b)
		}
		return idA < idB
	}
}

func (h *Handler) loadAdminFeedsDashboard(r *http.Request) (pageData, error) {
	data := h.baseData(r, "settings")
	data.SettingsSection = "admin_feeds"
	data.Title = "Состояние лент"

	if h.cfg.AdminFeeds == nil {
		return data, nil
	}

	ctx := r.Context()
	summary, err := h.cfg.AdminFeeds.AdminFeedSummary(ctx)
	if err != nil {
		return data, err
	}
	data.AdminFeedSummary = summary

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

	status := strings.TrimSpace(r.URL.Query().Get("status"))
	switch status {
	case "errors", "paused", "waiting", "ok", "all":
		data.AdminFeedsFilter = status
	default:
		data.AdminFeedsFilter = "all"
		status = "all"
	}

	sortKey, order := parseAdminFeedsSort(r)
	limit, offset, page := parseAdminFeedsPage(r)
	rows, total, err := h.cfg.AdminFeeds.ListAdminFeedsPage(ctx, storage.AdminFeedsListParams{
		Status:  status,
		SortKey: sortKey,
		Order:   order,
		Limit:   limit,
		Offset:  offset,
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
		rows, err := h.cfg.AdminFeeds.ListAdminFeeds(r.Context(), adminFeedsDetailLookupLimit)
		if err == nil {
			for _, row := range rows {
				if row.ID == id {
					detail.EntryCount = row.EntryCount
					detail.UnreadCount = row.UnreadCount
					detail.Status = classifyAdminFeedStatus(row, time.Now())
					break
				}
			}
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
	data.AdminFeedDetail = detail
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
		_, _ = h.cfg.Refresher.RefreshFeedManual(r.Context(), id)
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
	_ = h.cfg.Feeds.SetFeedManualPaused(r.Context(), id, false)
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
	_ = h.cfg.Feeds.SetFeedManualPaused(r.Context(), id, true)
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
	_ = h.cfg.Feeds.ResetFeedPollCircuit(r.Context(), id)
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

func adminFeedsRedirect(r *http.Request) string {
	ref := strings.TrimSpace(r.Header.Get("Referer"))
	if ref != "" {
		return ref
	}
	return "/ui/admin/feeds"
}
