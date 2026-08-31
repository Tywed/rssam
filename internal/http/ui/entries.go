package ui

import (
	"html/template"
	"net/http"
	"strconv"

	"rssam/internal/storage"
)

func (h *Handler) handleUnread(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	data := h.baseData(r, "unread")
	limit, offset := parsePage(r)

	st := storage.EntryStatusUnread
	filter := storage.ListEntriesFilter{Status: &st, Limit: limit, Offset: offset, Sort: data.EntrySort}

	q := make(map[string]string)
	if v := r.URL.Query().Get("feed_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			filter.FeedID = &id
			data.FeedID = id
			q["feed_id"] = v
			if data.SidebarLazyFeeds && h.cfg.Feeds != nil {
				if feed, err := h.cfg.Feeds.GetFeed(r.Context(), p.UserID, id); err == nil {
					if feed.CategoryID != nil {
						data.SidebarExpandCategoryID = *feed.CategoryID
					} else {
						data.SidebarExpandUncategorized = true
					}
				}
			}
		}
	}
	if v := r.URL.Query().Get("category_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			filter.CategoryID = &id
			data.CategoryID = id
			q["category_id"] = v
			if data.SidebarLazyFeeds {
				data.SidebarExpandCategoryID = id
			}
		}
	}
	data.Query = q
	appendEntrySortQuery(data.Query, data.EntrySort)
	data.Limit = limit
	data.Offset = offset

	entries, total, err := h.cfg.Entries.ListEntries(r.Context(), p.UserID, filter)
	if err != nil {
		http.Error(w, "list entries failed", http.StatusInternalServerError)
		return
	}
	data.Entries = entries
	data.Total = total
	data.Title = "Непрочитанные"
	h.loadSelectedEntry(r, p.UserID, &data)
	h.render(w, r, "unread", data)
}

func (h *Handler) handleUnreadMarkRead(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	_, _ = h.cfg.Entries.MarkAllEntriesRead(r.Context(), p.UserID)
	h.invalidateUnread(p.UserID)
	ref := r.Header.Get("Referer")
	if ref == "" {
		ref = "/ui/unread"
	}
	http.Redirect(w, r, ref, http.StatusFound)
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	data := h.baseData(r, "search")
	q := stringsTrim(r.URL.Query().Get("q"))
	data.SearchQuery = q
	limit, offset := parsePage(r)
	data.Limit = limit
	data.Offset = offset
	data.Query = map[string]string{}
	if q != "" {
		data.Query["q"] = q
		appendEntrySortQuery(data.Query, data.EntrySort)

		entries, total, err := h.cfg.Entries.SearchEntries(r.Context(), p.UserID, storage.SearchEntriesFilter{
			Query:  q,
			Sort:   data.EntrySort,
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			if h.log != nil {
				h.log.Error("ui search failed", "err", err, "q", q)
			}
			data.FlashErr = "Поиск не выполнен. Попробуйте другой запрос."
		} else {
			data.Entries = entries
			data.Total = total
		}
	}
	data.Title = "Поиск"
	h.loadSelectedEntry(r, p.UserID, &data)
	h.render(w, r, "search", data)
}

func (h *Handler) handleEntryGet(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/ui/unread?entry_id="+strconv.FormatInt(id, 10), http.StatusFound)
}

func (h *Handler) handleEntryPreview(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	entry, err := h.cfg.Entries.GetEntry(r.Context(), p.UserID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data := pageData{
		Entry:        entry,
		EntryContent: template.HTML(entry.Content),
		CSRFToken:    h.csrfToken(r),
	}
	h.renderPartial(w, r, "entry_preview", data)
}

func (h *Handler) loadSelectedEntry(r *http.Request, userID int64, data *pageData) {
	v := stringsTrim(r.URL.Query().Get("entry_id"))
	if v == "" {
		return
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		return
	}
	entry, err := h.cfg.Entries.GetEntry(r.Context(), userID, id)
	if err != nil {
		return
	}
	data.SelectedEntryID = id
	data.Entry = entry
	data.EntryContent = template.HTML(entry.Content)
}

func (h *Handler) handleEntryRead(w http.ResponseWriter, r *http.Request) {
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
	st := storage.EntryStatusRead
	_, err = h.cfg.Entries.BulkUpdateEntries(r.Context(), p.UserID, []int64{id}, storage.BulkEntryUpdate{Status: &st})
	if err != nil {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}
	h.invalidateUnread(p.UserID)
	if isHX(r) {
		entry, err := h.cfg.Entries.GetEntry(r.Context(), p.UserID, id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		h.renderPartial(w, r, "entry_row", pageData{
			Entry:           entry,
			CSRFToken:       h.csrfToken(r),
			SelectedEntryID: id,
		})
		return
	}
	ref := r.Header.Get("Referer")
	if ref == "" {
		ref = "/ui/unread?entry_id=" + strconv.FormatInt(id, 10)
	}
	http.Redirect(w, r, ref, http.StatusFound)
}

func (h *Handler) handleEntryStar(w http.ResponseWriter, r *http.Request) {
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
	starred := r.FormValue("starred") == "1"
	_, err = h.cfg.Entries.BulkUpdateEntries(r.Context(), p.UserID, []int64{id}, storage.BulkEntryUpdate{Starred: &starred})
	if err != nil {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}
	ref := r.Header.Get("Referer")
	if ref == "" {
		ref = "/ui/unread?entry_id=" + strconv.FormatInt(id, 10)
	}
	http.Redirect(w, r, ref, http.StatusFound)
}

func stringsTrim(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}
