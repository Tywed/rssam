// Category endpoints: /v1/categories.
package httpserver

import (
	"errors"
	"net/http"
	"strings"

	"rssam/internal/storage"
)

type categoryDTO struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Color string `json:"color,omitempty"`
}

type categoryWriteRequest struct {
	Title string `json:"title"`
	Color string `json:"color"`
}

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.categories == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "category storage is not configured")
		}
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
			ID:    c.ID,
			Title: c.Title,
			Color: c.Color,
		})
	}
	writeJSON(w, http.StatusOK, listResponse[[]categoryDTO]{Data: out, Total: total})
}

func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.categories == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "category storage is not configured")
		}
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
	category, err := s.categories.CreateCategory(r.Context(), p.UserID, req.Title, strings.TrimSpace(req.Color))
	if err != nil {
		s.log.ErrorContext(r.Context(), "create category failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, listResponse[categoryDTO]{
		Data:  categoryDTO{ID: category.ID, Title: category.Title, Color: category.Color},
		Total: 1,
	})
}

func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.categories == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "category storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	category, err := s.categories.UpdateCategory(r.Context(), p.UserID, id, req.Title, strings.TrimSpace(req.Color))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "category not found")
			return
		}
		s.log.ErrorContext(r.Context(), "update category failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[categoryDTO]{
		Data:  categoryDTO{ID: category.ID, Title: category.Title, Color: category.Color},
		Total: 1,
	})
}

func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.categories == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "category storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.categories.DeleteCategory(r.Context(), p.UserID, id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "category not found")
			return
		}
		s.log.ErrorContext(r.Context(), "delete category failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[deletedDTO]{Data: deletedDTO{Deleted: true}, Total: 1})
}
