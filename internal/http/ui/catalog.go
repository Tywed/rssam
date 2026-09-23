package ui

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"rssam/internal/storage"
)

const catalogPageSize = 50

func (h *Handler) handleCatalog(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Subscriptions == nil {
		http.NotFound(w, r)
		return
	}
	p, _ := principal(r)
	q := r.URL.Query()
	filter := storage.CatalogFilter{Query: strings.TrimSpace(q.Get("q")), Limit: catalogPageSize}
	if off, err := strconv.Atoi(q.Get("offset")); err == nil && off > 0 {
		filter.Offset = off
	}
	view := q.Get("view")
	switch view {
	case "mine":
		t := true
		filter.Subscribed = &t
	case "new":
		f := false
		filter.Subscribed = &f
	default:
		view = ""
	}
	feeds, total, err := h.cfg.Subscriptions.ListCatalog(r.Context(), p.UserID, filter)
	if err != nil {
		h.log.ErrorContext(r.Context(), "list catalog failed", "err", err)
		http.Error(w, "list catalog failed", http.StatusInternalServerError)
		return
	}
	data := h.baseData(r, "settings")
	data.SettingsSection = "catalog"
	data.Title = "Каталог лент"
	data.CatalogFeeds = feeds
	data.CatalogView = view
	data.Total = total
	data.Limit = catalogPageSize
	data.Offset = filter.Offset
	data.Query = map[string]string{"q": filter.Query, "view": view}
	data.SearchQuery = filter.Query
	if cats, _, err := h.cfg.Categories.ListCategories(r.Context(), p.UserID, 1000, 0); err == nil {
		data.Categories = cats
	}
	if n := q.Get("subscribed"); n != "" {
		data.FlashMsg = "Подписка оформлена: " + n
	}
	h.render(w, r, "catalog", data)
}

func (h *Handler) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.cfg.Subscriptions == nil {
		http.NotFound(w, r)
		return
	}
	p, _ := principal(r)
	feedID, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var params storage.SubscriptionParams
	if v := strings.TrimSpace(r.FormValue("category_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid category id", http.StatusBadRequest)
			return
		}
		params.CategoryID = &id
	}
	target := refererOr(r, "/ui/catalog")
	feed, err := h.cfg.Feeds.GetFeedByID(r.Context(), feedID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.cfg.Subscriptions.Subscribe(r.Context(), p.UserID, feedID, params); err != nil {
		switch {
		case errors.Is(err, storage.ErrAlreadySubscribed):
			http.Redirect(w, r, target, http.StatusFound)
		case errors.Is(err, storage.ErrNotFound), errors.Is(err, storage.ErrInvalidReference):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			h.failRedirect(w, r, target, "subscribe", err)
		}
		return
	}
	h.cfg.Audit.Record(r, storage.AuditSubscriptionCreate, "feed", feedID, nil)
	http.Redirect(w, r, withQueryParam(target, "subscribed", feed.Title), http.StatusFound)
}

func (h *Handler) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.cfg.Subscriptions == nil {
		http.NotFound(w, r)
		return
	}
	p, _ := principal(r)
	feedID, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.cfg.Subscriptions.Unsubscribe(r.Context(), p.UserID, feedID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		h.failRedirect(w, r, "/ui/feeds", "unsubscribe", err)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditSubscriptionDelete, "feed", feedID, nil)
	http.Redirect(w, r, "/ui/feeds", http.StatusFound)
}
