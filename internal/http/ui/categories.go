package ui

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"rssam/internal/storage"
)

func (h *Handler) handleCategoriesList(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r, "settings")
	data.SettingsSection = "categories"
	cats, _, err := h.cfg.Categories.ListCategories(r.Context(), 1000, 0)
	if err != nil {
		http.Error(w, "list categories failed", http.StatusInternalServerError)
		return
	}
	data.Categories = cats
	data.CategoryPollHours = h.cfg.CategoryPollHours != nil
	data.FlashMsg, data.FlashErr = feedsFlash(r)
	data.Title = "Категории"
	h.render(w, r, "categories_list", data)
}

func (h *Handler) handleCategoryCreate(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Error(w, "title required", http.StatusBadRequest)
		return
	}
	pollHours, err := storage.NormalizePollHours(r.FormValue("poll_hours"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// The quick form on the subscriptions page comes back there.
	target := refererOr(r, "/ui/categories")
	cat, err := h.cfg.Categories.CreateCategory(r.Context(), title, strings.TrimSpace(r.FormValue("color")))
	if err != nil {
		if errors.Is(err, storage.ErrDuplicateCategory) {
			feedsRedirect(w, r, target, feedsMsgCatDup, 0)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.saveCategoryPollHours(r, cat.ID, pollHours); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditCategoryCreate, "category", cat.ID, map[string]any{"title": cat.Title})
	feedsRedirect(w, r, target, feedsMsgCatCreated, 0)
}

func (h *Handler) handleCategoryUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Error(w, "title required", http.StatusBadRequest)
		return
	}
	pollHours, err := storage.NormalizePollHours(r.FormValue("poll_hours"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, err = h.cfg.Categories.UpdateCategory(r.Context(), id, title, strings.TrimSpace(r.FormValue("color")))
	if err != nil {
		if errors.Is(err, storage.ErrDuplicateCategory) {
			feedsRedirect(w, r, "/ui/categories", feedsMsgCatDup, 0)
			return
		}
		http.NotFound(w, r)
		return
	}
	if err := h.saveCategoryPollHours(r, id, pollHours); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditCategoryUpdate, "category", id, map[string]any{"title": title})
	http.Redirect(w, r, "/ui/categories", http.StatusFound)
}

// saveCategoryPollHours persists the window (no-op without a store or when
// the form did not send the field) and drops the refresher cache entry.
func (h *Handler) saveCategoryPollHours(r *http.Request, categoryID int64, pollHours string) error {
	if h.cfg.CategoryPollHours == nil || !r.Form.Has("poll_hours") {
		return nil
	}
	if err := h.cfg.CategoryPollHours.SetCategoryPollHours(r.Context(), categoryID, pollHours); err != nil {
		return err
	}
	if h.cfg.Refresher != nil {
		h.cfg.Refresher.InvalidatePollHours(categoryID)
	}
	return nil
}

func (h *Handler) handleCategoryDelete(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target := refererOr(r, "/ui/categories")
	if err := h.cfg.Categories.DeleteCategory(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrCategoryNotEmpty) {
			feedsRedirect(w, r, target, feedsMsgCatNotEmpty, 0)
			return
		}
		http.NotFound(w, r)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditCategoryDelete, "category", id, nil)
	feedsRedirect(w, r, target, feedsMsgCatDeleted, 0)
}

func (h *Handler) handleCategoryMarkRead(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	categoryID, err := parsePathID(r, "categoryID")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target := "/ui/unread?category_id=" + strings.TrimSpace(r.PathValue("categoryID"))
	if _, err := h.cfg.Entries.MarkAllCategoryEntriesRead(r.Context(), p.UserID, categoryID); err != nil {
		h.failRedirect(w, r, target, "mark category read", err)
		return
	}
	h.invalidateUnread(p.UserID)
	http.Redirect(w, r, target, http.StatusFound)
}

func (h *Handler) handleCategoryReorder(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var ids []int64
	for _, raw := range r.Form["order"] {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid order", http.StatusBadRequest)
			return
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		http.Error(w, "order required", http.StatusBadRequest)
		return
	}

	if err := h.cfg.Categories.ReorderCategories(r.Context(), ids); err != nil {
		if errors.Is(err, storage.ErrInvalidReference) {
			http.Error(w, "invalid order", http.StatusBadRequest)
			return
		}
		http.Error(w, "reorder failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
