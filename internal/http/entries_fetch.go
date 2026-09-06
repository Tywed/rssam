package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"rssam/internal/service"
	"rssam/internal/storage"
)

func (s *Server) handleFetchEntryContent(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok {
		return
	}
	if s.contentFetcher == nil {
		writeError(w, http.StatusServiceUnavailable, "content fetcher is not configured")
		return
	}
	if s.entries == nil {
		writeError(w, http.StatusServiceUnavailable, "entry storage is not configured")
		return
	}

	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	entry, err := s.contentFetcher.FetchEntryContent(ctx, p.UserID, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "entry not found")
			return
		}
		if errors.Is(err, service.ErrEmptyEntryURL) {
			writeError(w, http.StatusBadRequest, "entry has no url")
			return
		}
		if errors.Is(err, service.ErrScrapeEntry) {
			writeError(w, http.StatusBadGateway, "content fetch failed")
			return
		}
		s.log.ErrorContext(r.Context(), "fetch entry content failed", "err", err, "entry_id", id)
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
