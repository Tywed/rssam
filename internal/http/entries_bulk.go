package httpserver

import (
	"errors"
	"net/http"
	"strings"

	"rssam/internal/storage"
)

const maxBulkEntryIDs = 1000

type bulkUpdateEntriesRequest struct {
	EntryIDs []int64 `json:"entry_ids"`
	Status   string  `json:"status"`
	Starred  *bool   `json:"starred"`
}

type bulkUpdateEntriesResultDTO struct {
	Updated int `json:"updated"`
}

type markAllReadResultDTO struct {
	Marked int `json:"marked"`
}

func (s *Server) handleBulkUpdateEntries(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.entries == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "entry storage is not configured")
		}
		return
	}
	var req bulkUpdateEntriesRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.EntryIDs) == 0 {
		writeError(w, http.StatusBadRequest, "entry_ids is required")
		return
	}
	if len(req.EntryIDs) > maxBulkEntryIDs {
		writeError(w, http.StatusBadRequest, "too many entry_ids")
		return
	}

	update := storage.BulkEntryUpdate{Starred: req.Starred}
	req.Status = strings.TrimSpace(req.Status)
	if req.Status != "" {
		if !storage.IsValidEntryStatus(req.Status) || req.Status == storage.EntryStatusRemoved {
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		update.Status = &req.Status
	}
	if update.Status == nil && update.Starred == nil {
		writeError(w, http.StatusBadRequest, "status or starred is required")
		return
	}

	updated, err := s.entries.BulkUpdateEntries(r.Context(), p.UserID, req.EntryIDs, update)
	if err != nil {
		s.log.ErrorContext(r.Context(), "bulk update entries failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[bulkUpdateEntriesResultDTO]{
		Data:  bulkUpdateEntriesResultDTO{Updated: updated},
		Total: 1,
	})
}

func (s *Server) handleMarkFeedAllRead(w http.ResponseWriter, r *http.Request) {
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

	marked, err := s.entries.MarkAllFeedEntriesRead(r.Context(), p.UserID, feedID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "feed not found")
			return
		}
		s.log.ErrorContext(r.Context(), "mark feed all read failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[markAllReadResultDTO]{
		Data:  markAllReadResultDTO{Marked: marked},
		Total: 1,
	})
}
