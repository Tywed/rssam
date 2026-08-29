package ui

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"rssam/internal/storage"
)

func parseCategoryPathID(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(r.PathValue("categoryID"))
	if raw == "" {
		return 0, fmt.Errorf("category id required")
	}
	var id int64
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid category id")
		}
		id = id*10 + int64(c-'0')
	}
	return id, nil
}

func feedsFilterFromForm(r *http.Request) string {
	filter := strings.TrimSpace(r.FormValue("filter"))
	if filter != "errors" && filter != "inactive" {
		return ""
	}
	return filter
}

func (h *Handler) renderFeedsListFlash(w http.ResponseWriter, r *http.Request, flash string) {
	p, _ := principal(r)
	data := h.baseData(r, "settings")
	data.SettingsSection = "feeds"
	data.FlashMsg = flash
	filter := feedsFilterFromForm(r)
	feeds, total, _ := h.cfg.Feeds.ListFeeds(r.Context(), p.UserID, storage.NoLimit, 0)
	h.populateFeedsListData(r.Context(), p.UserID, &data, feeds, filter, total)
	if p.IsAdmin {
		h.loadFeedFormWebhooks(r, &data)
	}
	data.Title = "Ленты"
	h.render(w, r, "feeds_list", data)
}

func (h *Handler) handleCategoryBulkInterval(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	categoryID, err := parseCategoryPathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = r.ParseForm()
	interval, err := strconv.Atoi(strings.TrimSpace(r.FormValue("interval_minutes")))
	if err != nil || interval < storage.MinFeedIntervalMinutes || interval > storage.MaxFeedIntervalMinutes {
		http.Error(w, fmt.Sprintf("interval_minutes must be between %d and %d", storage.MinFeedIntervalMinutes, storage.MaxFeedIntervalMinutes), http.StatusBadRequest)
		return
	}
	ids, count, err := h.cfg.Feeds.BulkUpdateFeedsByCategory(r.Context(), p.UserID, categoryID, storage.BulkFeedUpdate{
		IntervalMinutes: &interval,
	})
	if err != nil {
		if err == storage.ErrNotFound {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.cfg.Refresher != nil {
		for _, id := range ids {
			_ = h.cfg.Refresher.RescheduleFeed(r.Context(), id, interval)
		}
	}
	h.renderFeedsListFlash(w, r, fmt.Sprintf("Интервал опроса обновлён для %d лент", count))
}

func (h *Handler) handleCategoryBulkWebhook(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	categoryID, err := parseCategoryPathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = r.ParseForm()
	update := storage.BulkFeedUpdate{WebhookSet: true}
	if whID := parseFeedWebhookID(r.FormValue("webhook_id")); whID != nil {
		update.WebhookID = whID
	}
	_, count, err := h.cfg.Feeds.BulkUpdateFeedsByCategory(r.Context(), p.UserID, categoryID, update)
	if err != nil {
		if err == storage.ErrNotFound {
			http.NotFound(w, r)
			return
		}
		if err == storage.ErrInvalidReference {
			http.Error(w, "webhook not found", http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.renderFeedsListFlash(w, r, fmt.Sprintf("Вебхук назначен для %d лент", count))
}

func (h *Handler) handleCategoryBulkHashOnly(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	categoryID, err := parseCategoryPathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = r.ParseForm()
	action := strings.TrimSpace(r.FormValue("action"))
	var enabled bool
	switch action {
	case "enable":
		enabled = true
	case "disable":
		enabled = false
	default:
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}
	_, count, err := h.cfg.Feeds.BulkUpdateFeedsByCategory(r.Context(), p.UserID, categoryID, storage.BulkFeedUpdate{
		StoreHashOnly: &enabled,
	})
	if err != nil {
		if err == storage.ErrNotFound {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	label := "выключен"
	if enabled {
		label = "включён"
	}
	h.renderFeedsListFlash(w, r, fmt.Sprintf("Режим «хеш до фильтра» %s для %d лент", label, count))
}

func (h *Handler) handleCategoryBulkRefresh(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	categoryID, err := parseCategoryPathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = r.ParseForm()
	feeds, _, _ := h.cfg.Feeds.ListFeeds(r.Context(), p.UserID, storage.NoLimit, 0)
	ids := feedIDsInCategory(feeds, categoryID)
	if h.cfg.Refresher != nil {
		for _, id := range ids {
			_, _ = h.cfg.Refresher.RefreshFeedManual(r.Context(), id)
		}
	}
	h.renderFeedsListFlash(w, r, fmt.Sprintf("Обновление запущено для %d лент", len(ids)))
}

func (h *Handler) handleCategoryBulkPause(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	categoryID, err := parseCategoryPathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = r.ParseForm()
	action := strings.TrimSpace(r.FormValue("action"))
	var paused bool
	switch action {
	case "pause":
		paused = true
	case "unpause":
		paused = false
	default:
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}
	_, count, err := h.cfg.Feeds.BulkUpdateFeedsByCategory(r.Context(), p.UserID, categoryID, storage.BulkFeedUpdate{
		ManualPaused: &paused,
	})
	if err != nil {
		if err == storage.ErrNotFound {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	label := "снята"
	if paused {
		label = "поставлена"
	}
	h.renderFeedsListFlash(w, r, fmt.Sprintf("Ручная пауза %s для %d лент", label, count))
}

func (h *Handler) handleCategoryBulkMove(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	categoryID, err := parseCategoryPathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = r.ParseForm()
	update := storage.BulkFeedUpdate{MoveCategory: true}
	if v := strings.TrimSpace(r.FormValue("target_category_id")); v != "" {
		targetID, err := strconv.ParseInt(v, 10, 64)
		if err != nil || targetID <= 0 {
			http.Error(w, "invalid target category", http.StatusBadRequest)
			return
		}
		if targetID == categoryID {
			http.Error(w, "target category must differ", http.StatusBadRequest)
			return
		}
		update.MoveToCategoryID = &targetID
	}
	_, count, err := h.cfg.Feeds.BulkUpdateFeedsByCategory(r.Context(), p.UserID, categoryID, update)
	if err != nil {
		if err == storage.ErrNotFound {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.renderFeedsListFlash(w, r, fmt.Sprintf("Перенесено %d лент", count))
}
