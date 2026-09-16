// Category endpoints: /v1/categories.
package httpserver

import (
	"net/http"
	"strings"

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

func (s *Server) applyCategoryPollHours(r *http.Request, userID int64, c *storage.Category, req categoryWriteRequest) error {
	if req.PollHours == nil || s.pollHours == nil {
		return nil
	}
	norm, err := storage.NormalizePollHours(*req.PollHours)
	if err != nil {
		return err
	}
	if err := s.pollHours.SetCategoryPollHours(r.Context(), userID, c.ID, norm); err != nil {
		return err
	}
	c.PollHours = norm
	if s.refresher != nil {
		s.refresher.InvalidatePollHours(c.ID)
	}
	return nil
}

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	p, ok := requireStore(w, r, false, s.categories != nil, "category storage is not configured")
	if !ok {
		return
	}
	limit, offset, err := parseLimitOffset(r, 100, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	categories, total, err := s.categories.ListCategories(r.Context(), p.UserID, limit, offset)
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
	p, ok := requireStore(w, r, true, s.categories != nil, "category storage is not configured")
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
	category, err := s.categories.CreateCategory(r.Context(), p.UserID, req.Title, strings.TrimSpace(req.Color))
	if err != nil {
		s.log.ErrorContext(r.Context(), "create category failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := s.applyCategoryPollHours(r, p.UserID, &category, req); err != nil {
		s.log.ErrorContext(r.Context(), "set category poll_hours failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, listResponse[categoryDTO]{
		Data:  categoryDTO{ID: category.ID, Title: category.Title, Color: category.Color, PollHours: category.PollHours},
		Total: 1,
	})
}

func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	p, id, ok := requireStoreID(w, r, true, s.categories != nil, "category storage is not configured")
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
	category, err := s.categories.UpdateCategory(r.Context(), p.UserID, id, req.Title, strings.TrimSpace(req.Color))
	if err != nil {
		s.storeError(w, r, err, "category not found", "update category failed")
		return
	}
	if err := s.applyCategoryPollHours(r, p.UserID, &category, req); err != nil {
		s.log.ErrorContext(r.Context(), "set category poll_hours failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[categoryDTO]{
		Data:  categoryDTO{ID: category.ID, Title: category.Title, Color: category.Color, PollHours: category.PollHours},
		Total: 1,
	})
}

func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	p, id, ok := requireStoreID(w, r, true, s.categories != nil, "category storage is not configured")
	if !ok {
		return
	}
	if err := s.categories.DeleteCategory(r.Context(), p.UserID, id); err != nil {
		s.storeError(w, r, err, "category not found", "delete category failed")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[deletedDTO]{Data: deletedDTO{Deleted: true}, Total: 1})
}
