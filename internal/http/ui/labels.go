package ui

import (
	"net/http"
	"strconv"
	"strings"

	"rssam/internal/storage"
)

func (h *Handler) handleLabelsList(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	p, _ := principal(r)
	data := h.baseData(r, "settings")
	data.SettingsSection = "labels"
	labels, _, err := h.cfg.Labels.ListLabels(r.Context(), p.UserID, 1000, 0)
	if err != nil {
		http.Error(w, "list labels failed", http.StatusInternalServerError)
		return
	}
	data.Labels = labels
	if counts, err := h.cfg.Labels.EntryCountsByLabel(r.Context(), p.UserID); err == nil {
		data.LabelEntryCounts = counts
	}
	data.Title = "Метки"
	h.render(w, r, "labels_list", data)
}

func (h *Handler) handleLabelEntries(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	label, err := h.cfg.Labels.GetLabel(r.Context(), p.UserID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data := h.baseData(r, "label")
	data.Label = label
	data.LabelID = id
	limit, offset := parsePage(r)
	data.Limit = limit
	data.Offset = offset

	entries, total, err := h.cfg.Entries.ListEntries(r.Context(), p.UserID, storage.ListEntriesFilter{
		LabelID: &id,
		Limit:   limit,
		Offset:  offset,
		Sort:    data.EntrySort,
	})
	if err != nil {
		http.Error(w, "list entries failed", http.StatusInternalServerError)
		return
	}
	data.Entries = entries
	data.Total = total
	data.Title = label.Caption
	h.loadSelectedEntry(r, p.UserID, &data)
	h.render(w, r, "label_entries", data)
}

func (h *Handler) handleLabelMarkRead(w http.ResponseWriter, r *http.Request) {
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
	_, _ = h.cfg.Entries.MarkAllLabelEntriesRead(r.Context(), p.UserID, id)
	h.invalidateUnread(p.UserID)
	http.Redirect(w, r, "/ui/labels/"+strconv.FormatInt(id, 10), http.StatusFound)
}

func (h *Handler) handleLabelCreate(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	p, _ := principal(r)
	caption := strings.TrimSpace(r.FormValue("caption"))
	if caption == "" {
		http.Error(w, "caption required", http.StatusBadRequest)
		return
	}
	fg := strings.TrimSpace(r.FormValue("fg_color"))
	bg := strings.TrimSpace(r.FormValue("bg_color"))
	if _, err := h.cfg.Labels.CreateLabel(r.Context(), storage.CreateLabelParams{
		UserID:  p.UserID,
		Caption: caption,
		FgColor: fg,
		BgColor: bg,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ref := localUIPath(r.FormValue("redirect"), "")
	if ref == "" {
		ref = refererOr(r, "/ui/labels")
	}
	http.Redirect(w, r, ref, http.StatusFound)
}

func (h *Handler) handleLabelUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	caption := strings.TrimSpace(r.FormValue("caption"))
	if caption == "" {
		http.Error(w, "caption required", http.StatusBadRequest)
		return
	}
	_, err = h.cfg.Labels.UpdateLabel(r.Context(), storage.UpdateLabelParams{
		ID:      id,
		UserID:  p.UserID,
		Caption: caption,
		FgColor: strings.TrimSpace(r.FormValue("fg_color")),
		BgColor: strings.TrimSpace(r.FormValue("bg_color")),
	})
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/ui/labels", http.StatusFound)
}

func (h *Handler) handleLabelDelete(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.cfg.Labels.DeleteLabel(r.Context(), p.UserID, id); err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, refererOr(r, "/ui/labels"), http.StatusFound)
}
