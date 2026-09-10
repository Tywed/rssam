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

	filter := storage.ListEntriesFilter{Limit: limit, Offset: offset, Sort: data.EntrySort}
	q := make(map[string]string)
	if r.URL.Query().Get("all") == "1" {
		data.ShowAll = true
		q["all"] = "1"
	} else {
		st := storage.EntryStatusUnread
		filter.Status = &st
	}
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
	if data.ShowAll {
		data.Title = "Все записи"
	}
	h.loadSelectedEntry(r, p.UserID, &data)
	h.render(w, r, "unread", data)
}

func (h *Handler) handleStarred(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	data := h.baseData(r, "starred")
	limit, offset := parsePage(r)
	data.Limit = limit
	data.Offset = offset
	data.Query = map[string]string{}
	appendEntrySortQuery(data.Query, data.EntrySort)

	starred := true
	entries, total, err := h.cfg.Entries.ListEntries(r.Context(), p.UserID, storage.ListEntriesFilter{
		Starred: &starred,
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
	data.Title = "Избранное"
	h.loadSelectedEntry(r, p.UserID, &data)
	h.render(w, r, "starred", data)
}

func (h *Handler) handleUnreadMarkRead(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	_, _ = h.cfg.Entries.MarkAllEntriesRead(r.Context(), p.UserID)
	h.invalidateUnread(p.UserID)
	http.Redirect(w, r, refererOr(r, "/ui/unread"), http.StatusFound)
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
				h.log.ErrorContext(r.Context(), "ui search failed", "err", err, "q", q)
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
	http.Redirect(w, r, refererOr(r, "/ui/unread?entry_id="+strconv.FormatInt(id, 10)), http.StatusFound)
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
	http.Redirect(w, r, refererOr(r, "/ui/unread?entry_id="+strconv.FormatInt(id, 10)), http.StatusFound)
}

const maxBulkEntryIDs = 1000

// handleEntriesBulk backs the selection bar above the entry list: the same
// store call as PUT /v1/entries, with the form-encoded ids of the checked rows.
func (h *Handler) handleEntriesBulk(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	raw := r.Form["entry_ids"]
	if len(raw) == 0 || len(raw) > maxBulkEntryIDs {
		http.Error(w, "entry_ids required", http.StatusBadRequest)
		return
	}
	ids := make([]int64, 0, len(raw))
	for _, v := range raw {
		id, err := strconvAtoi(v)
		if err != nil || id <= 0 {
			http.Error(w, "invalid entry id", http.StatusBadRequest)
			return
		}
		ids = append(ids, int64(id))
	}
	var update storage.BulkEntryUpdate
	switch r.FormValue("action") {
	case "read":
		st := storage.EntryStatusRead
		update.Status = &st
	case "unread":
		st := storage.EntryStatusUnread
		update.Status = &st
	case "star":
		on := true
		update.Starred = &on
	case "unstar":
		off := false
		update.Starred = &off
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if _, err := h.cfg.Entries.BulkUpdateEntries(r.Context(), p.UserID, ids, update); err != nil {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}
	if update.Status != nil {
		h.invalidateUnread(p.UserID)
	}
	http.Redirect(w, r, refererOr(r, "/ui/unread"), http.StatusFound)
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
