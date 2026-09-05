package ui

import (
	"net/http"
	"strconv"
	"strings"

	"rssam/internal/storage"
)

func (h *Handler) handleCategoriesList(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	data := h.baseData(r, "settings")
	data.SettingsSection = "categories"
	cats, _, err := h.cfg.Categories.ListCategories(r.Context(), p.UserID, 1000, 0)
	if err != nil {
		http.Error(w, "list categories failed", http.StatusInternalServerError)
		return
	}
	data.Categories = cats
	data.Title = "Категории"
	h.render(w, r, "categories_list", data)
}

func (h *Handler) handleCategoryCreate(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Error(w, "title required", http.StatusBadRequest)
		return
	}
	_, err := h.cfg.Categories.CreateCategory(r.Context(), p.UserID, title, strings.TrimSpace(r.FormValue("color")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/ui/categories", http.StatusFound)
}

func (h *Handler) handleCategoryUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
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
	_, err = h.cfg.Categories.UpdateCategory(r.Context(), p.UserID, id, title, strings.TrimSpace(r.FormValue("color")))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/ui/categories", http.StatusFound)
}

func (h *Handler) handleCategoryDelete(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.cfg.Categories.DeleteCategory(r.Context(), p.UserID, id); err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/ui/categories", http.StatusFound)
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
	_, _ = h.cfg.Entries.MarkAllCategoryEntriesRead(r.Context(), p.UserID, categoryID)
	h.invalidateUnread(p.UserID)
	http.Redirect(w, r, "/ui/unread?category_id="+strings.TrimSpace(r.PathValue("categoryID")), http.StatusFound)
}

func (h *Handler) handleCategoryReorder(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)

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

	if err := h.cfg.Categories.ReorderCategories(r.Context(), p.UserID, ids); err != nil {
		if err == storage.ErrInvalidReference {
			http.Error(w, "invalid order", http.StatusBadRequest)
			return
		}
		http.Error(w, "reorder failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
