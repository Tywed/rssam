package httpserver

import (
	"errors"
	"net/http"
	"strings"

	"rssam/internal/storage"
)

func (s *Server) handleMarkCategoryAllRead(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.entries == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "entry storage is not configured")
		}
		return
	}
	categoryID, err := parsePathInt64(r, "categoryID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	marked, err := s.entries.MarkAllCategoryEntriesRead(r.Context(), p.UserID, categoryID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "category not found")
			return
		}
		s.log.ErrorContext(r.Context(), "mark category all read failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[markAllReadResultDTO]{
		Data:  markAllReadResultDTO{Marked: marked},
		Total: 1,
	})
}

func (s *Server) handleGetFeedEntry(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.entries == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "entry storage is not configured")
		}
		return
	}
	feedID, err := parsePathInt64(r, "feedID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entryID, err := parsePathInt64(r, "entryID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entry, err := s.entries.GetFeedEntry(r.Context(), p.UserID, feedID, entryID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "entry not found")
			return
		}
		s.log.ErrorContext(r.Context(), "get feed entry failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	dto, err := s.entryToDTO(r.Context(), p.UserID, entry)
	if err != nil {
		s.log.ErrorContext(r.Context(), "load entry enclosures failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[entryDTO]{Data: dto, Total: 1})
}

type updateEntryRequest struct {
	Status  *string `json:"status"`
	Starred *bool   `json:"starred"`
}

func (s *Server) handleUpdateFeedEntry(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.entries == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "entry storage is not configured")
		}
		return
	}
	feedID, err := parsePathInt64(r, "feedID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entryID, err := parsePathInt64(r, "entryID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req updateEntryRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Status == nil && req.Starred == nil {
		writeError(w, http.StatusBadRequest, "status or starred is required")
		return
	}
	if req.Status != nil {
		status := strings.TrimSpace(*req.Status)
		if !storage.IsValidEntryStatus(status) {
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		req.Status = &status
	}

	entry, err := s.entries.UpdateEntry(r.Context(), p.UserID, feedID, entryID, storage.UpdateEntryParams{
		Status:  req.Status,
		Starred: req.Starred,
	})
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "entry not found")
			return
		}
		s.log.ErrorContext(r.Context(), "update feed entry failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	dto, err := s.entryToDTO(r.Context(), p.UserID, entry)
	if err != nil {
		s.log.ErrorContext(r.Context(), "load entry enclosures failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[entryDTO]{Data: dto, Total: 1})
}
