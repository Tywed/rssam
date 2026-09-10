// Entry read endpoints: /v1/entries and /v1/feeds/{feedID}/entries. Bulk
// updates, per-feed entry updates and full-content fetch are in entries_*.go.
package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rssam/internal/storage"
)

type entryDTO struct {
	ID              int64          `json:"id"`
	FeedID          int64          `json:"feed_id"`
	Title           string         `json:"title"`
	URL             string         `json:"url"`
	Content         string         `json:"content"`
	OriginalContent string         `json:"original_content,omitempty"`
	ContentFetched  bool           `json:"content_fetched"`
	Author          *string        `json:"author,omitempty"`
	PublishedAt     *time.Time     `json:"published_at,omitempty"`
	Hash            string         `json:"hash"`
	Status          string         `json:"status"`
	Starred         bool           `json:"starred"`
	Enclosures      []enclosureDTO `json:"enclosures,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

func toEntryDTO(e storage.Entry, encs []storage.Enclosure) entryDTO {
	return entryDTO{
		ID:              e.ID,
		FeedID:          e.FeedID,
		Title:           e.Title,
		URL:             e.URL,
		Content:         e.Content,
		OriginalContent: e.OriginalContent,
		ContentFetched:  e.ContentFetched,
		Author:          e.Author,
		PublishedAt:     e.PublishedAt,
		Hash:            e.Hash,
		Status:          e.Status,
		Starred:         e.Starred,
		Enclosures:      toEnclosureDTOs(encs),
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
	}
}

func (s *Server) handleListEntries(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.entries == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "entry storage is not configured")
		}
		return
	}
	limit, offset, err := parseLimitOffset(r, 100, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter, searchQuery, err := parseEntriesFilter(r, limit, offset, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var entries []storage.Entry
	var total int
	if searchQuery != "" {
		entries, total, err = s.entries.SearchEntries(r.Context(), p.UserID, storage.SearchEntriesFilter{
			Query:     searchQuery,
			FeedID:    filter.FeedID,
			Status:    filter.Status,
			Starred:   filter.Starred,
			Sort:      filter.Sort,
			Limit:     filter.Limit,
			Offset:    filter.Offset,
			Rank:      true,
			WithTotal: true,
		})
		if err != nil {
			s.log.ErrorContext(r.Context(), "search entries failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	} else {
		entries, total, err = s.entries.ListEntries(r.Context(), p.UserID, filter)
		if err != nil {
			s.log.ErrorContext(r.Context(), "list entries failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}
	out, err := s.entriesToDTOs(r.Context(), p.UserID, entries)
	if err != nil {
		s.log.ErrorContext(r.Context(), "load entry enclosures failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[[]entryDTO]{Data: out, Total: total})
}

func (s *Server) handleGetEntry(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.entries == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "entry storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry, err := s.entries.GetEntry(r.Context(), p.UserID, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "entry not found")
			return
		}
		s.log.ErrorContext(r.Context(), "get entry failed", "err", err)
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

func (s *Server) handleListFeedEntries(w http.ResponseWriter, r *http.Request) {
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
	limit, offset, err := parseLimitOffset(r, 100, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter, _, err := parseEntriesFilter(r, limit, offset, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entries, total, err := s.entries.ListFeedEntries(r.Context(), p.UserID, feedID, filter)
	if err != nil {
		s.log.ErrorContext(r.Context(), "list feed entries failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out, err := s.entriesToDTOs(r.Context(), p.UserID, entries)
	if err != nil {
		s.log.ErrorContext(r.Context(), "load entry enclosures failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[[]entryDTO]{Data: out, Total: total})
}

func parseEntriesFilter(r *http.Request, limit, offset int, allowFeedID bool) (storage.ListEntriesFilter, string, error) {
	q := r.URL.Query()
	filter := storage.ListEntriesFilter{Limit: limit, Offset: offset, WithTotal: true}
	searchQuery := strings.TrimSpace(q.Get("q"))

	if allowFeedID {
		if v := strings.TrimSpace(q.Get("feed_id")); v != "" {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil || id <= 0 {
				return storage.ListEntriesFilter{}, "", errors.New("invalid feed_id")
			}
			filter.FeedID = &id
		}
	}
	if v := strings.TrimSpace(q.Get("status")); v != "" {
		if !storage.IsValidEntryStatus(v) {
			return storage.ListEntriesFilter{}, "", errors.New("invalid status")
		}
		filter.Status = &v
	}
	if v := strings.TrimSpace(q.Get("starred")); v != "" {
		starred, err := strconv.ParseBool(v)
		if err != nil {
			return storage.ListEntriesFilter{}, "", errors.New("invalid starred")
		}
		filter.Starred = &starred
	}
	filter.Sort = storage.NormalizeEntrySort(q.Get("sort"))
	return filter, searchQuery, nil
}
