package ui

import (
	"errors"
	"net/http"
	"strconv"

	"rssam/internal/storage"
)

// Flash codes of the subscriptions page (query parameter feedsMsgParam).
const (
	feedsMsgParam       = "msg"
	feedsMsgCount       = "n"
	feedsMsgSubscribed  = "subscribed"
	feedsMsgUnsubscribe = "unsubscribed"
	feedsMsgFollowed    = "followed"
	feedsMsgUnfollowed  = "unfollowed"
	feedsMsgFollowsCat  = "follows_category"
	feedsMsgFeedAdded   = "feed_added"
	feedsMsgCatCreated  = "category_created"
	feedsMsgCatDup      = "category_duplicate"
	feedsMsgCatNotEmpty = "category_not_empty"
	feedsMsgCatDeleted  = "category_deleted"
)

func feedsFlash(r *http.Request) (msg, errMsg string) {
	q := r.URL.Query()
	n, _ := strconv.Atoi(q.Get(feedsMsgCount))
	switch q.Get(feedsMsgParam) {
	case feedsMsgSubscribed:
		return "Лента добавлена в ваши подписки.", ""
	case feedsMsgUnsubscribe:
		return "Лента убрана из ваших подписок.", ""
	case feedsMsgFollowed:
		if n > 0 {
			return "Вы читаете всю категорию: " + pluralRu(n, "канал добавлен", "канала добавлено", "каналов добавлено") + ", новые ленты категории будут приходить сами.", ""
		}
		return "Вы читаете всю категорию: новые ленты будут приходить сами.", ""
	case feedsMsgUnfollowed:
		return "Категория убрана из ваших подписок: " + pluralRu(n, "канал убран", "канала убрано", "каналов убрано") + ".", ""
	case feedsMsgFollowsCat:
		return "", "Эта лента приходит через категорию, которую вы читаете целиком. Отпишитесь от категории."
	case feedsMsgFeedAdded:
		if n > 0 {
			return "Лента добавлена и выдана " + pluralReaders(n) + " категории.", ""
		}
		return "Лента добавлена.", ""
	case feedsMsgCatCreated:
		return "Категория создана.", ""
	case feedsMsgCatDup:
		return "", "Категория с таким названием уже есть."
	case feedsMsgCatNotEmpty:
		return "", "В категории есть ленты — сначала перенесите или удалите их."
	case feedsMsgCatDeleted:
		return "Категория удалена.", ""
	}
	return "", ""
}

func feedsRedirect(w http.ResponseWriter, r *http.Request, target, code string, n int) {
	target = withQueryParam(target, feedsMsgParam, code)
	if n > 0 {
		target = withQueryParam(target, feedsMsgCount, strconv.Itoa(n))
	}
	http.Redirect(w, r, target, http.StatusFound)
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
	target := refererOr(r, "/ui/feeds")
	if _, err := h.cfg.Subscriptions.Subscribe(r.Context(), p.UserID, feedID, storage.SubscriptionParams{}); err != nil {
		switch {
		case errors.Is(err, storage.ErrAlreadySubscribed):
			http.Redirect(w, r, target, http.StatusFound)
		case errors.Is(err, storage.ErrNotFound), errors.Is(err, storage.ErrInvalidReference):
			http.NotFound(w, r)
		default:
			h.failRedirect(w, r, target, "subscribe", err)
		}
		return
	}
	h.cfg.Audit.Record(r, storage.AuditSubscriptionCreate, "feed", feedID, nil)
	h.invalidateUnread(p.UserID)
	feedsRedirect(w, r, target, feedsMsgSubscribed, 0)
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
	target := refererOr(r, "/ui/feeds")
	if err := h.cfg.Subscriptions.Unsubscribe(r.Context(), p.UserID, feedID); err != nil {
		switch {
		case errors.Is(err, storage.ErrFollowsCategory):
			feedsRedirect(w, r, target, feedsMsgFollowsCat, 0)
		case errors.Is(err, storage.ErrNotFound):
			http.NotFound(w, r)
		default:
			h.failRedirect(w, r, target, "unsubscribe", err)
		}
		return
	}
	h.cfg.Audit.Record(r, storage.AuditSubscriptionDelete, "feed", feedID, nil)
	h.invalidateUnread(p.UserID)
	feedsRedirect(w, r, target, feedsMsgUnsubscribe, 0)
}

func (h *Handler) handleCategoryFollow(w http.ResponseWriter, r *http.Request) {
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
	target := refererOr(r, "/ui/feeds")
	n, err := h.cfg.Categories.FollowCategory(r.Context(), p.UserID, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		h.failRedirect(w, r, target, "follow category", err)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditCategoryFollow, "category", id, map[string]any{"feeds": n})
	h.invalidateUnread(p.UserID)
	feedsRedirect(w, r, target, feedsMsgFollowed, n)
}

func (h *Handler) handleCategoryUnfollow(w http.ResponseWriter, r *http.Request) {
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
	target := refererOr(r, "/ui/feeds")
	n, err := h.cfg.Categories.UnfollowCategory(r.Context(), p.UserID, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		h.failRedirect(w, r, target, "unfollow category", err)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditCategoryUnfollow, "category", id, map[string]any{"feeds": n})
	h.invalidateUnread(p.UserID)
	feedsRedirect(w, r, target, feedsMsgUnfollowed, n)
}
