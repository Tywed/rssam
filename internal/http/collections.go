// Collection endpoints: curated sets of catalog feeds (editor/admin) that a
// reader follows as a whole. Editing a set is for its owner or an admin.
package httpserver

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

const (
	maxCollectionTitle       = 200
	maxCollectionDescription = 2000
)

type collectionDTO struct {
	ID            int64     `json:"id"`
	OwnerID       int64     `json:"owner_id,omitempty"`
	OwnerName     string    `json:"owner_name,omitempty"`
	Title         string    `json:"title"`
	Description   string    `json:"description,omitempty"`
	FeedCount     int       `json:"feed_count"`
	FollowerCount int       `json:"follower_count"`
	Followed      bool      `json:"followed"`
	CategoryID    *int64    `json:"category_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type collectionDetailDTO struct {
	collectionDTO
	Feeds []catalogFeedDTO `json:"feeds"`
}

type collectionWriteRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type collectionFeedsRequest struct {
	FeedIDs []int64 `json:"feed_ids"`
}

type collectionFollowRequest struct {
	CategoryID *int64 `json:"category_id"`
}

type collectionFollowDTO struct {
	Subscribed int   `json:"subscribed"`
	CategoryID int64 `json:"category_id"`
}

type collectionFeedsAddedDTO struct {
	Added int `json:"added"`
}

type collectionUnfollowDTO struct {
	Unsubscribed int `json:"unsubscribed"`
}

func toCollectionDTO(c storage.Collection) collectionDTO {
	return collectionDTO{
		ID: c.ID, OwnerID: c.OwnerID, OwnerName: c.OwnerName, Title: c.Title, Description: c.Description,
		FeedCount: c.FeedCount, FollowerCount: c.FollowerCount, Followed: c.Followed, CategoryID: c.CategoryID,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

func toCatalogFeedDTO(f storage.CatalogFeed) catalogFeedDTO {
	return catalogFeedDTO{
		ID: f.ID, FeedURL: f.FeedURL, FeedType: f.FeedType, Title: f.Title,
		OwnerID: f.OwnerID, OwnerName: f.OwnerName,
		SubscriberCount: f.SubscriberCount, Subscribed: f.Subscribed,
		LastEntryAt: f.LastEntryAt, LastError: f.LastError, CreatedAt: f.CreatedAt,
	}
}

func validateCollectionWrite(req collectionWriteRequest) (storage.CollectionParams, error) {
	p := storage.CollectionParams{Title: strings.TrimSpace(req.Title), Description: strings.TrimSpace(req.Description)}
	switch {
	case p.Title == "":
		return p, errors.New("title is required")
	case len([]rune(p.Title)) > maxCollectionTitle:
		return p, errors.New("title is too long")
	case len([]rune(p.Description)) > maxCollectionDescription:
		return p, errors.New("description is too long")
	}
	return p, nil
}

func (s *Server) handleListCollections(w http.ResponseWriter, r *http.Request) {
	p, ok := requireStore(w, r, "", s.collections != nil, "collection storage is not configured")
	if !ok {
		return
	}
	list, err := s.collections.ListCollections(r.Context(), p.UserID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "list collections failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out := make([]collectionDTO, 0, len(list))
	for _, c := range list {
		out = append(out, toCollectionDTO(c))
	}
	writeJSON(w, http.StatusOK, listResponse[[]collectionDTO]{Data: out, Total: len(out)})
}

func (s *Server) handleGetCollection(w http.ResponseWriter, r *http.Request) {
	p, id, ok := requireStoreID(w, r, "", s.collections != nil, "collection storage is not configured")
	if !ok {
		return
	}
	c, err := s.collections.GetCollection(r.Context(), p.UserID, id)
	if err != nil {
		s.storeError(w, r, err, "collection not found", "get collection failed")
		return
	}
	feeds, err := s.collections.ListCollectionFeeds(r.Context(), p.UserID, id)
	if err != nil {
		s.log.ErrorContext(r.Context(), "list collection feeds failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out := collectionDetailDTO{collectionDTO: toCollectionDTO(c), Feeds: make([]catalogFeedDTO, 0, len(feeds))}
	for _, f := range feeds {
		out.Feeds = append(out.Feeds, toCatalogFeedDTO(f))
	}
	writeJSON(w, http.StatusOK, listResponse[collectionDetailDTO]{Data: out, Total: 1})
}

func (s *Server) handleCreateCollection(w http.ResponseWriter, r *http.Request) {
	p, ok := requireStore(w, r, auth.RoleEditor, s.collections != nil, "collection storage is not configured")
	if !ok {
		return
	}
	var req collectionWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params, err := validateCollectionWrite(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c, err := s.collections.CreateCollection(r.Context(), p.UserID, params)
	if err != nil {
		s.log.ErrorContext(r.Context(), "create collection failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	s.audit.Record(r, storage.AuditCollectionCreate, "collection", c.ID, map[string]any{"title": c.Title})
	writeJSON(w, http.StatusCreated, listResponse[collectionDTO]{Data: toCollectionDTO(c), Total: 1})
}

// ownedCollection loads the collection for an edit: 404 when missing, 403
// when the caller is neither its owner nor an admin.
func (s *Server) ownedCollection(w http.ResponseWriter, r *http.Request, p auth.Principal, id int64) (storage.Collection, bool) {
	c, err := s.collections.GetCollection(r.Context(), p.UserID, id)
	if err != nil {
		s.storeError(w, r, err, "collection not found", "get collection failed")
		return storage.Collection{}, false
	}
	if c.OwnerID != p.UserID && !p.IsAdmin() {
		writeError(w, http.StatusForbidden, "only the owner or an admin can change this collection")
		return storage.Collection{}, false
	}
	return c, true
}

func (s *Server) handleUpdateCollection(w http.ResponseWriter, r *http.Request) {
	p, id, ok := requireStoreID(w, r, auth.RoleEditor, s.collections != nil, "collection storage is not configured")
	if !ok {
		return
	}
	var req collectionWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params, err := validateCollectionWrite(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := s.ownedCollection(w, r, p, id); !ok {
		return
	}
	if err := s.collections.UpdateCollection(r.Context(), id, params); err != nil {
		s.storeError(w, r, err, "collection not found", "update collection failed")
		return
	}
	c, err := s.collections.GetCollection(r.Context(), p.UserID, id)
	if err != nil {
		s.storeError(w, r, err, "collection not found", "get collection failed")
		return
	}
	s.audit.Record(r, storage.AuditCollectionUpdate, "collection", id, map[string]any{"title": c.Title})
	writeJSON(w, http.StatusOK, listResponse[collectionDTO]{Data: toCollectionDTO(c), Total: 1})
}

func (s *Server) handleDeleteCollection(w http.ResponseWriter, r *http.Request) {
	p, id, ok := requireStoreID(w, r, auth.RoleEditor, s.collections != nil, "collection storage is not configured")
	if !ok {
		return
	}
	if _, ok := s.ownedCollection(w, r, p, id); !ok {
		return
	}
	if err := s.collections.DeleteCollection(r.Context(), id); err != nil {
		s.storeError(w, r, err, "collection not found", "delete collection failed")
		return
	}
	s.audit.Record(r, storage.AuditCollectionDelete, "collection", id, nil)
	writeJSON(w, http.StatusOK, listResponse[deletedDTO]{Data: deletedDTO{Deleted: true}, Total: 1})
}

func (s *Server) handleAddCollectionFeeds(w http.ResponseWriter, r *http.Request) {
	p, id, ok := requireStoreID(w, r, auth.RoleEditor, s.collections != nil, "collection storage is not configured")
	if !ok {
		return
	}
	var req collectionFeedsRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.FeedIDs) == 0 {
		writeError(w, http.StatusBadRequest, "feed_ids is required")
		return
	}
	for _, fid := range req.FeedIDs {
		if fid <= 0 {
			writeError(w, http.StatusBadRequest, "invalid feed id")
			return
		}
	}
	if _, ok := s.ownedCollection(w, r, p, id); !ok {
		return
	}
	added, err := s.collections.AddCollectionFeeds(r.Context(), id, req.FeedIDs)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidReference) {
			writeError(w, http.StatusBadRequest, "unknown feed id")
			return
		}
		s.storeError(w, r, err, "collection not found", "add collection feeds failed")
		return
	}
	s.audit.Record(r, storage.AuditCollectionUpdate, "collection", id, map[string]any{"feeds_added": req.FeedIDs})
	writeJSON(w, http.StatusOK, listResponse[collectionFeedsAddedDTO]{Data: collectionFeedsAddedDTO{Added: added}, Total: 1})
}

func (s *Server) handleRemoveCollectionFeed(w http.ResponseWriter, r *http.Request) {
	p, id, ok := requireStoreID(w, r, auth.RoleEditor, s.collections != nil, "collection storage is not configured")
	if !ok {
		return
	}
	feedID, err := parsePathInt64(r, "feedID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := s.ownedCollection(w, r, p, id); !ok {
		return
	}
	if err := s.collections.RemoveCollectionFeed(r.Context(), id, feedID); err != nil {
		s.storeError(w, r, err, "feed is not in the collection", "remove collection feed failed")
		return
	}
	s.audit.Record(r, storage.AuditCollectionUpdate, "collection", id, map[string]any{"feed_removed": feedID})
	writeJSON(w, http.StatusOK, listResponse[deletedDTO]{Data: deletedDTO{Deleted: true}, Total: 1})
}

func (s *Server) handleFollowCollection(w http.ResponseWriter, r *http.Request) {
	p, id, ok := requireStoreID(w, r, "", s.collections != nil, "collection storage is not configured")
	if !ok {
		return
	}
	var req collectionFollowRequest
	if r.ContentLength != 0 {
		if err := decodeJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	res, err := s.collections.FollowCollection(r.Context(), p.UserID, id, req.CategoryID)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrAlreadyFollowing):
			writeError(w, http.StatusConflict, "already following")
		case errors.Is(err, storage.ErrInvalidReference):
			writeError(w, http.StatusBadRequest, "invalid category_id")
		default:
			s.storeError(w, r, err, "collection not found", "follow collection failed")
		}
		return
	}
	s.audit.Record(r, storage.AuditSubscriptionCreate, "collection", id, map[string]any{"subscribed": res.Subscribed})
	writeJSON(w, http.StatusCreated, listResponse[collectionFollowDTO]{Data: collectionFollowDTO{Subscribed: res.Subscribed, CategoryID: res.CategoryID}, Total: 1})
}

func (s *Server) handleUnfollowCollection(w http.ResponseWriter, r *http.Request) {
	p, id, ok := requireStoreID(w, r, "", s.collections != nil, "collection storage is not configured")
	if !ok {
		return
	}
	n, err := s.collections.UnfollowCollection(r.Context(), p.UserID, id)
	if err != nil {
		s.storeError(w, r, err, "not following this collection", "unfollow collection failed")
		return
	}
	s.audit.Record(r, storage.AuditSubscriptionDelete, "collection", id, map[string]any{"unsubscribed": n})
	writeJSON(w, http.StatusOK, listResponse[collectionUnfollowDTO]{Data: collectionUnfollowDTO{Unsubscribed: n}, Total: 1})
}
