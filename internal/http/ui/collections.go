package ui

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"rssam/internal/storage"
)

const (
	maxCollectionTitle       = 200
	maxCollectionDescription = 2000
	// collectionCandidateLimit caps the add-feed select on the collection
	// page (ListCatalog's hard maximum).
	collectionCandidateLimit = 500
)

func (h *Handler) handleCollections(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Collections == nil {
		http.NotFound(w, r)
		return
	}
	p, _ := principal(r)
	list, err := h.cfg.Collections.ListCollections(r.Context(), p.UserID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "list collections failed", "err", err)
		http.Error(w, "list collections failed", http.StatusInternalServerError)
		return
	}
	data := h.baseData(r, "settings")
	data.SettingsSection = "collections"
	data.Title = "Подборки"
	data.Collections = list
	q := r.URL.Query()
	if n := q.Get("unfollowed"); n != "" {
		data.FlashMsg = "Подписка на подборку снята, лент убрано: " + n
	}
	h.render(w, r, "collections_list", data)
}

func parseCollectionForm(r *http.Request) (storage.CollectionParams, string) {
	p := storage.CollectionParams{Title: strings.TrimSpace(r.FormValue("title")), Description: strings.TrimSpace(r.FormValue("description"))}
	switch {
	case p.Title == "":
		return p, "Укажите название подборки"
	case len([]rune(p.Title)) > maxCollectionTitle:
		return p, "Название длиннее 200 символов"
	case len([]rune(p.Description)) > maxCollectionDescription:
		return p, "Описание длиннее 2000 символов"
	}
	return p, ""
}

func (h *Handler) handleCollectionCreate(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.cfg.Collections == nil {
		http.NotFound(w, r)
		return
	}
	p, _ := principal(r)
	params, msg := parseCollectionForm(r)
	if msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	c, err := h.cfg.Collections.CreateCollection(r.Context(), p.UserID, params)
	if err != nil {
		h.failRedirect(w, r, "/ui/collections", "create collection", err)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditCollectionCreate, "collection", c.ID, map[string]any{"title": c.Title})
	http.Redirect(w, r, "/ui/collections/"+strconv.FormatInt(c.ID, 10), http.StatusFound)
}

func (h *Handler) handleCollectionShow(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Collections == nil {
		http.NotFound(w, r)
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	c, err := h.cfg.Collections.GetCollection(r.Context(), p.UserID, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "get collection failed", "err", err)
		http.Error(w, "get collection failed", http.StatusInternalServerError)
		return
	}
	feeds, err := h.cfg.Collections.ListCollectionFeeds(r.Context(), p.UserID, id)
	if err != nil {
		h.log.ErrorContext(r.Context(), "list collection feeds failed", "err", err)
		http.Error(w, "list collection feeds failed", http.StatusInternalServerError)
		return
	}
	data := h.baseData(r, "settings")
	data.SettingsSection = "collections"
	data.Title = c.Title
	data.Collection = c
	data.CollectionFeeds = feeds
	data.CanEditCollection = p.CanEdit() && (c.OwnerID == p.UserID || p.IsAdmin())
	if data.CanEditCollection && h.cfg.Subscriptions != nil {
		in := make(map[int64]bool, len(feeds))
		for _, f := range feeds {
			in[f.ID] = true
		}
		all, _, err := h.cfg.Subscriptions.ListCatalog(r.Context(), p.UserID, storage.CatalogFilter{Limit: collectionCandidateLimit})
		if err != nil {
			h.log.ErrorContext(r.Context(), "list catalog failed", "err", err)
			http.Error(w, "list catalog failed", http.StatusInternalServerError)
			return
		}
		for _, f := range all {
			if !in[f.ID] {
				data.CollectionCandidates = append(data.CollectionCandidates, f)
			}
		}
	}
	q := r.URL.Query()
	switch {
	case q.Get("followed") != "":
		data.FlashMsg = "Подписка на подборку оформлена, новых лент: " + q.Get("followed")
	case q.Get("added") != "":
		data.FlashMsg = "Лент добавлено: " + q.Get("added")
	case q.Get("saved") != "":
		data.FlashMsg = "Подборка сохранена"
	}
	h.render(w, r, "collections_show", data)
}

// ownedCollection loads the collection for an edit: 404 when missing, 403
// when the caller is neither its owner nor an admin.
func (h *Handler) ownedCollection(w http.ResponseWriter, r *http.Request) (storage.Collection, int64, bool) {
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return storage.Collection{}, 0, false
	}
	c, err := h.cfg.Collections.GetCollection(r.Context(), p.UserID, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return storage.Collection{}, 0, false
		}
		h.log.ErrorContext(r.Context(), "get collection failed", "err", err)
		http.Error(w, "get collection failed", http.StatusInternalServerError)
		return storage.Collection{}, 0, false
	}
	if c.OwnerID != p.UserID && !p.IsAdmin() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return storage.Collection{}, 0, false
	}
	return c, id, true
}

func (h *Handler) handleCollectionUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.cfg.Collections == nil {
		http.NotFound(w, r)
		return
	}
	params, msg := parseCollectionForm(r)
	if msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	_, id, ok := h.ownedCollection(w, r)
	if !ok {
		return
	}
	target := "/ui/collections/" + strconv.FormatInt(id, 10)
	if err := h.cfg.Collections.UpdateCollection(r.Context(), id, params); err != nil {
		h.failRedirect(w, r, target, "update collection", err)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditCollectionUpdate, "collection", id, map[string]any{"title": params.Title})
	http.Redirect(w, r, withQueryParam(target, "saved", "1"), http.StatusFound)
}

func (h *Handler) handleCollectionDelete(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.cfg.Collections == nil {
		http.NotFound(w, r)
		return
	}
	_, id, ok := h.ownedCollection(w, r)
	if !ok {
		return
	}
	if err := h.cfg.Collections.DeleteCollection(r.Context(), id); err != nil {
		h.failRedirect(w, r, "/ui/collections", "delete collection", err)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditCollectionDelete, "collection", id, nil)
	http.Redirect(w, r, "/ui/collections", http.StatusFound)
}

func (h *Handler) handleCollectionAddFeeds(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.cfg.Collections == nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	feedIDs := make([]int64, 0, len(r.Form["feed_id"]))
	for _, v := range r.Form["feed_id"] {
		id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid feed id", http.StatusBadRequest)
			return
		}
		feedIDs = append(feedIDs, id)
	}
	if len(feedIDs) == 0 {
		http.Error(w, "выберите хотя бы одну ленту", http.StatusBadRequest)
		return
	}
	_, id, ok := h.ownedCollection(w, r)
	if !ok {
		return
	}
	target := "/ui/collections/" + strconv.FormatInt(id, 10)
	added, err := h.cfg.Collections.AddCollectionFeeds(r.Context(), id, feedIDs)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidReference) {
			http.Error(w, "такой ленты нет в каталоге", http.StatusBadRequest)
			return
		}
		h.failRedirect(w, r, target, "add collection feeds", err)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditCollectionUpdate, "collection", id, map[string]any{"feeds_added": feedIDs})
	http.Redirect(w, r, withQueryParam(target, "added", strconv.Itoa(added)), http.StatusFound)
}

func (h *Handler) handleCollectionRemoveFeed(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.cfg.Collections == nil {
		http.NotFound(w, r)
		return
	}
	feedID, err := parsePathID(r, "feedID")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, id, ok := h.ownedCollection(w, r)
	if !ok {
		return
	}
	target := "/ui/collections/" + strconv.FormatInt(id, 10)
	if err := h.cfg.Collections.RemoveCollectionFeed(r.Context(), id, feedID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		h.failRedirect(w, r, target, "remove collection feed", err)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditCollectionUpdate, "collection", id, map[string]any{"feed_removed": feedID})
	http.Redirect(w, r, target, http.StatusFound)
}

func (h *Handler) handleCollectionFollow(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.cfg.Collections == nil {
		http.NotFound(w, r)
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var categoryID *int64
	if v := strings.TrimSpace(r.FormValue("category_id")); v != "" {
		cid, err := strconv.ParseInt(v, 10, 64)
		if err != nil || cid <= 0 {
			http.Error(w, "invalid category id", http.StatusBadRequest)
			return
		}
		categoryID = &cid
	}
	target := "/ui/collections/" + strconv.FormatInt(id, 10)
	res, err := h.cfg.Collections.FollowCollection(r.Context(), p.UserID, id, categoryID)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrAlreadyFollowing):
			http.Redirect(w, r, target, http.StatusFound)
		case errors.Is(err, storage.ErrNotFound):
			http.NotFound(w, r)
		case errors.Is(err, storage.ErrInvalidReference):
			http.Error(w, "invalid category id", http.StatusBadRequest)
		default:
			h.failRedirect(w, r, target, "follow collection", err)
		}
		return
	}
	h.cfg.Audit.Record(r, storage.AuditSubscriptionCreate, "collection", id, map[string]any{"subscribed": res.Subscribed})
	http.Redirect(w, r, withQueryParam(target, "followed", strconv.Itoa(res.Subscribed)), http.StatusFound)
}

func (h *Handler) handleCollectionUnfollow(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.cfg.Collections == nil {
		http.NotFound(w, r)
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	n, err := h.cfg.Collections.UnfollowCollection(r.Context(), p.UserID, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		h.failRedirect(w, r, "/ui/collections", "unfollow collection", err)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditSubscriptionDelete, "collection", id, map[string]any{"unsubscribed": n})
	http.Redirect(w, r, withQueryParam("/ui/collections", "unfollowed", strconv.Itoa(n)), http.StatusFound)
}
