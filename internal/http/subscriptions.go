// Subscription endpoints: the shared catalog and a user's membership in it.
// Feeds themselves (catalog rows) are managed through /v1/feeds.
package httpserver

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"rssam/internal/storage"
)

type catalogFeedDTO struct {
	ID              int64      `json:"id"`
	FeedURL         string     `json:"feed_url"`
	FeedType        string     `json:"feed_type"`
	Title           string     `json:"title"`
	OwnerID         int64      `json:"owner_id,omitempty"`
	OwnerName       string     `json:"owner_name,omitempty"`
	SubscriberCount int        `json:"subscriber_count"`
	Subscribed      bool       `json:"subscribed"`
	LastEntryAt     *time.Time `json:"last_entry_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type subscriptionDTO struct {
	FeedID     int64     `json:"feed_id"`
	CategoryID *int64    `json:"category_id,omitempty"`
	WebhookID  *int64    `json:"webhook_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type subscriptionWriteRequest struct {
	FeedID     int64  `json:"feed_id"`
	CategoryID *int64 `json:"category_id"`
	WebhookID  *int64 `json:"webhook_id"`
}

func toSubscriptionDTO(s storage.Subscription) subscriptionDTO {
	return subscriptionDTO{FeedID: s.FeedID, CategoryID: s.CategoryID, WebhookID: s.WebhookID, CreatedAt: s.CreatedAt}
}

func (s *Server) handleListCatalog(w http.ResponseWriter, r *http.Request) {
	p, ok := requireStore(w, r, "", s.subscriptions != nil, "subscription storage is not configured")
	if !ok {
		return
	}
	limit, offset, err := parseLimitOffset(r, 100, 500)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter := storage.CatalogFilter{Query: strings.TrimSpace(r.URL.Query().Get("q")), Limit: limit, Offset: offset}
	switch r.URL.Query().Get("subscribed") {
	case "":
	case "true":
		filter.Subscribed = ptrTo(true)
	case "false":
		filter.Subscribed = ptrTo(false)
	default:
		writeError(w, http.StatusBadRequest, "subscribed must be true or false")
		return
	}
	feeds, total, err := s.subscriptions.ListCatalog(r.Context(), p.UserID, filter)
	if err != nil {
		s.log.ErrorContext(r.Context(), "list catalog failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out := make([]catalogFeedDTO, 0, len(feeds))
	for _, f := range feeds {
		out = append(out, catalogFeedDTO{
			ID: f.ID, FeedURL: f.FeedURL, FeedType: f.FeedType, Title: f.Title,
			OwnerID: f.OwnerID, OwnerName: f.OwnerName,
			SubscriberCount: f.SubscriberCount, Subscribed: f.Subscribed,
			LastEntryAt: f.LastEntryAt, LastError: f.LastError, CreatedAt: f.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, listResponse[[]catalogFeedDTO]{Data: out, Total: total})
}

func (s *Server) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	p, ok := requireStore(w, r, "", s.subscriptions != nil, "subscription storage is not configured")
	if !ok {
		return
	}
	subs, err := s.subscriptions.ListSubscriptions(r.Context(), p.UserID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "list subscriptions failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out := make([]subscriptionDTO, 0, len(subs))
	for _, sub := range subs {
		out = append(out, toSubscriptionDTO(sub))
	}
	writeJSON(w, http.StatusOK, listResponse[[]subscriptionDTO]{Data: out, Total: len(out)})
}

func (s *Server) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	p, ok := requireStore(w, r, "", s.subscriptions != nil, "subscription storage is not configured")
	if !ok {
		return
	}
	var req subscriptionWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.FeedID <= 0 {
		writeError(w, http.StatusBadRequest, "feed_id is required")
		return
	}
	sub, err := s.subscriptions.Subscribe(r.Context(), p.UserID, req.FeedID, storage.SubscriptionParams{CategoryID: req.CategoryID, WebhookID: req.WebhookID})
	if err != nil {
		s.subscriptionError(w, r, err, "subscribe failed")
		return
	}
	s.audit.Record(r, storage.AuditSubscriptionCreate, "feed", sub.FeedID, nil)
	writeJSON(w, http.StatusCreated, listResponse[subscriptionDTO]{Data: toSubscriptionDTO(sub), Total: 1})
}

func (s *Server) handleUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	p, feedID, ok := requireStoreID(w, r, "", s.subscriptions != nil, "subscription storage is not configured")
	if !ok {
		return
	}
	var req subscriptionWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sub, err := s.subscriptions.UpdateSubscription(r.Context(), p.UserID, feedID, storage.SubscriptionParams{CategoryID: req.CategoryID, WebhookID: req.WebhookID})
	if err != nil {
		s.subscriptionError(w, r, err, "update subscription failed")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[subscriptionDTO]{Data: toSubscriptionDTO(sub), Total: 1})
}

func (s *Server) handleDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	p, feedID, ok := requireStoreID(w, r, "", s.subscriptions != nil, "subscription storage is not configured")
	if !ok {
		return
	}
	if err := s.subscriptions.Unsubscribe(r.Context(), p.UserID, feedID); err != nil {
		s.storeError(w, r, err, "subscription not found", "unsubscribe failed")
		return
	}
	s.audit.Record(r, storage.AuditSubscriptionDelete, "feed", feedID, nil)
	writeJSON(w, http.StatusOK, listResponse[deletedDTO]{Data: deletedDTO{Deleted: true}, Total: 1})
}

func (s *Server) subscriptionError(w http.ResponseWriter, r *http.Request, err error, logMsg string) {
	switch {
	case errors.Is(err, storage.ErrAlreadySubscribed):
		writeError(w, http.StatusConflict, "already subscribed")
	case errors.Is(err, storage.ErrInvalidReference):
		writeError(w, http.StatusBadRequest, "invalid feed_id, category_id or webhook_id")
	default:
		s.storeError(w, r, err, "feed not found", logMsg)
	}
}

func ptrTo[T any](v T) *T { return &v }
