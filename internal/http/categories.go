// Category endpoints: /v1/categories.
package httpserver

import (
	"errors"
	"net/http"
	"strings"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type categoryDTO struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Color     string `json:"color,omitempty"`
	PollHours string `json:"poll_hours,omitempty"`
}

type categoryWriteRequest struct {
	Title string `json:"title"`
	Color string `json:"color"`
	// PollHours "HH:MM-HH:MM" (server local time); nil = keep, "" = always.
	PollHours *string `json:"poll_hours"`
}

func (s *Server) applyCategoryPollHours(r *http.Request, c *storage.Category, req categoryWriteRequest) error {
	if req.PollHours == nil || s.pollHours == nil {
		return nil
	}
	norm, err := storage.NormalizePollHours(*req.PollHours)
	if err != nil {
		return err
	}
	if err := s.pollHours.SetCategoryPollHours(r.Context(), c.ID, norm); err != nil {
		return err
	}
	c.PollHours = norm
	if s.refresher != nil {
		s.refresher.InvalidatePollHours(c.ID)
	}
	return nil
}

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireStore(w, r, "", s.categories != nil, "category storage is not configured"); !ok {
		return
	}
	limit, offset, err := parseLimitOffset(r, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	categories, total, err := s.categories.ListCategories(r.Context(), limit, offset)
	if err != nil {
		s.log.ErrorContext(r.Context(), "list categories failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out := make([]categoryDTO, 0, len(categories))
	for _, c := range categories {
		out = append(out, categoryDTO{
			ID:        c.ID,
			Title:     c.Title,
			Color:     c.Color,
			PollHours: c.PollHours,
		})
	}
	writeJSON(w, http.StatusOK, listResponse[[]categoryDTO]{Data: out, Total: total})
}

func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireStore(w, r, auth.RoleEditor, s.categories != nil, "category storage is not configured"); !ok {
		return
	}
	var req categoryWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.PollHours != nil {
		if _, err := storage.NormalizePollHours(*req.PollHours); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	category, err := s.categories.CreateCategory(r.Context(), req.Title, strings.TrimSpace(req.Color))
	if err != nil {
		s.categoryError(w, r, err, "create category failed")
		return
	}
	if err := s.applyCategoryPollHours(r, &category, req); err != nil {
		s.log.ErrorContext(r.Context(), "set category poll_hours failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	s.audit.Record(r, storage.AuditCategoryCreate, "category", category.ID, map[string]any{"title": category.Title})
	writeJSON(w, http.StatusCreated, listResponse[categoryDTO]{
		Data:  categoryDTO{ID: category.ID, Title: category.Title, Color: category.Color, PollHours: category.PollHours},
		Total: 1,
	})
}

func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	_, id, ok := requireStoreID(w, r, auth.RoleEditor, s.categories != nil, "category storage is not configured")
	if !ok {
		return
	}
	var req categoryWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.PollHours != nil {
		if _, err := storage.NormalizePollHours(*req.PollHours); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	category, err := s.categories.UpdateCategory(r.Context(), id, req.Title, strings.TrimSpace(req.Color))
	if err != nil {
		s.categoryError(w, r, err, "update category failed")
		return
	}
	if err := s.applyCategoryPollHours(r, &category, req); err != nil {
		s.log.ErrorContext(r.Context(), "set category poll_hours failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	s.audit.Record(r, storage.AuditCategoryUpdate, "category", category.ID, map[string]any{"title": category.Title})
	writeJSON(w, http.StatusOK, listResponse[categoryDTO]{
		Data:  categoryDTO{ID: category.ID, Title: category.Title, Color: category.Color, PollHours: category.PollHours},
		Total: 1,
	})
}

func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	_, id, ok := requireStoreID(w, r, auth.RoleEditor, s.categories != nil, "category storage is not configured")
	if !ok {
		return
	}
	if err := s.categories.DeleteCategory(r.Context(), id); err != nil {
		s.categoryError(w, r, err, "delete category failed")
		return
	}
	s.audit.Record(r, storage.AuditCategoryDelete, "category", id, nil)
	writeJSON(w, http.StatusOK, listResponse[deletedDTO]{Data: deletedDTO{Deleted: true}, Total: 1})
}

type followDTO struct {
	Following bool `json:"following"`
	// Feeds is how many subscriptions the call created or removed.
	Feeds int `json:"feeds"`
}

func (s *Server) handleFollowCategory(w http.ResponseWriter, r *http.Request) {
	p, id, ok := requireStoreID(w, r, "", s.categories != nil, "category storage is not configured")
	if !ok {
		return
	}
	n, err := s.categories.FollowCategory(r.Context(), p.UserID, id)
	if err != nil {
		s.categoryError(w, r, err, "follow category failed")
		return
	}
	s.audit.Record(r, storage.AuditCategoryFollow, "category", id, map[string]any{"feeds": n})
	writeJSON(w, http.StatusOK, listResponse[followDTO]{Data: followDTO{Following: true, Feeds: n}, Total: 1})
}

func (s *Server) handleUnfollowCategory(w http.ResponseWriter, r *http.Request) {
	p, id, ok := requireStoreID(w, r, "", s.categories != nil, "category storage is not configured")
	if !ok {
		return
	}
	n, err := s.categories.UnfollowCategory(r.Context(), p.UserID, id)
	if err != nil {
		s.categoryError(w, r, err, "unfollow category failed")
		return
	}
	s.audit.Record(r, storage.AuditCategoryUnfollow, "category", id, map[string]any{"feeds": n})
	writeJSON(w, http.StatusOK, listResponse[followDTO]{Data: followDTO{Following: false, Feeds: n}, Total: 1})
}

func (s *Server) categoryError(w http.ResponseWriter, r *http.Request, err error, logMsg string) {
	switch {
	case errors.Is(err, storage.ErrDuplicateCategory):
		writeError(w, http.StatusConflict, "category title already exists")
	case errors.Is(err, storage.ErrCategoryNotEmpty):
		writeError(w, http.StatusConflict, "category has feeds")
	default:
		s.storeError(w, r, err, "category not found", logMsg)
	}
}
