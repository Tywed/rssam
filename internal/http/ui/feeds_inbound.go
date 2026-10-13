package ui

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"rssam/internal/http/middleware"
	"rssam/internal/reader"
	"rssam/internal/storage"
)

func (h *Handler) handleInboundFeedNew(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r, "settings")
	data.SettingsSection = "feeds"
	data.Title = "Новый входящий вебхук"
	h.render(w, r, "feeds_inbound_form", data)
}

func (h *Handler) handleInboundFeedCreate(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Error(w, errRequired("title").Error(), http.StatusBadRequest)
		return
	}
	var catID *int64
	if v := strings.TrimSpace(r.FormValue("category_id")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			catID = &n
		}
	}
	retention, err := parseEntryRetentionDays(r.FormValue("entry_retention_days"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.feedQuota(r, p, 60, true); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	feed, _, err := h.cfg.InboundFeeds.CreateInboundFeed(r.Context(), p.UserID, storage.CreateInboundFeedParams{
		Title:                 title,
		CategoryID:            catID,
		EntryRetentionDays:    retention,
		SkipOwnerSubscription: r.FormValue("subscribe_self") == "0",
	})
	if err != nil {
		if errors.Is(err, storage.ErrInvalidReference) {
			http.Error(w, "Категория не найдена", http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditFeedCreate, "feed", feed.ID, map[string]any{"url": feed.FeedURL})
	http.Redirect(w, r, "/ui/feeds/"+strconv.FormatInt(feed.ID, 10), http.StatusFound)
}

func (h *Handler) handleInboundSecretRotate(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := h.cfg.InboundFeeds.RotateInboundSecret(r.Context(), id); err != nil {
		http.NotFound(w, r)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditFeedUpdate, "feed", id, map[string]any{"inbound_secret": "rotated"})
	http.Redirect(w, r, "/ui/feeds/"+strconv.FormatInt(id, 10), http.StatusFound)
}

// loadInboundDelivery fills the delivery address and secret of an inbound
// feed for editors; the address is built from the request so it matches
// what the reverse proxy exposes.
func (h *Handler) loadInboundDelivery(r *http.Request, data *pageData, feed storage.Feed) {
	if feed.FeedType != reader.FeedTypeInbound || !data.CanEdit || h.cfg.InboundFeeds == nil {
		return
	}
	data.InboundFeed = true
	scheme := "http"
	if middleware.RequestIsSecure(r, h.cfg.HSTSEnabled) {
		scheme = "https"
	}
	data.InboundHookURL = scheme + "://" + r.Host + "/hooks/" + strconv.FormatInt(feed.ID, 10)
	if secret, err := h.cfg.InboundFeeds.InboundSecret(r.Context(), feed.ID); err == nil {
		data.InboundSecret = secret
	}
}
