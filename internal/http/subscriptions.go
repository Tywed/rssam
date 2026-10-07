// Subscription endpoints: a user's membership in the shared catalog. Feeds
// themselves are managed through /v1/feeds, whole categories are followed
// through /v1/categories/{id}/follow.
package httpserver

import (
	"errors"
	"net/http"
	"time"

	"rssam/internal/storage"
)

type subscriptionDTO struct {
	FeedID    int64     `json:"feed_id"`
	WebhookID *int64    `json:"webhook_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type subscriptionWriteRequest struct {
	FeedID    int64  `json:"feed_id"`
	WebhookID *int64 `json:"webhook_id"`
}

func toSubscriptionDTO(s storage.Subscription) subscriptionDTO {
	return subscriptionDTO{FeedID: s.FeedID, WebhookID: s.WebhookID, CreatedAt: s.CreatedAt}
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
	sub, err := s.subscriptions.Subscribe(r.Context(), p.UserID, req.FeedID, storage.SubscriptionParams{WebhookID: req.WebhookID})
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
	sub, err := s.subscriptions.UpdateSubscription(r.Context(), p.UserID, feedID, storage.SubscriptionParams{WebhookID: req.WebhookID})
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
		if errors.Is(err, storage.ErrFollowsCategory) {
			writeError(w, http.StatusConflict, "feed is read through a followed category; unfollow the category instead")
			return
		}
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
		writeError(w, http.StatusBadRequest, "invalid feed_id or webhook_id")
	default:
		s.storeError(w, r, err, "feed not found", logMsg)
	}
}
