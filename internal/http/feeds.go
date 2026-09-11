// Feed endpoints: /v1/feeds CRUD and single-feed refresh. OPML import/export,
// icons, refresh-all and the import job manager live in their own files.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rssam/internal/reader"
	"rssam/internal/service"
	"rssam/internal/ssrf"
	"rssam/internal/storage"
)

type feedDTO struct {
	ID                 int64  `json:"id"`
	FeedURL            string `json:"feed_url"`
	FeedType           string `json:"feed_type,omitempty"`
	Title              string `json:"title"`
	CategoryID         *int64 `json:"category_id,omitempty"`
	IntervalMinutes    int    `json:"interval_minutes"`
	ScraperRules       string `json:"scraper_rules,omitempty"`
	RewriteRules       string `json:"rewrite_rules,omitempty"`
	BlockedRules       string `json:"blocked_rules,omitempty"`
	KeepRules          string `json:"keep_rules,omitempty"`
	FetchViaProxy      bool   `json:"fetch_via_proxy"`
	TLSInsecure        bool   `json:"tls_insecure"`
	Crawler            bool   `json:"crawler"`
	UserAgent          string `json:"user_agent,omitempty"`
	StoreHashOnly      bool   `json:"store_hash_only,omitempty"`
	EntryRetentionDays *int   `json:"entry_retention_days,omitempty"`
}

type feedWriteRequest struct {
	FeedURL            string `json:"feed_url"`
	Title              string `json:"title"`
	CategoryID         *int64 `json:"category_id"`
	IntervalMinutes    int    `json:"interval_minutes"`
	ScraperRules       string `json:"scraper_rules"`
	RewriteRules       string `json:"rewrite_rules"`
	BlockedRules       string `json:"blocked_rules"`
	KeepRules          string `json:"keep_rules"`
	FetchViaProxy      bool   `json:"fetch_via_proxy"`
	TLSInsecure        bool   `json:"tls_insecure"`
	Crawler            bool   `json:"crawler"`
	UserAgent          string `json:"user_agent"`
	StoreHashOnly      bool   `json:"store_hash_only,omitempty"`
	EntryRetentionDays *int   `json:"entry_retention_days,omitempty"`
}

const defaultIntervalMinutes = 60

func (s *Server) handleListFeeds(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.feeds == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "feed storage is not configured")
		}
		return
	}
	limit, offset, err := parseLimitOffset(r, 100, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	feeds, total, err := s.feeds.ListFeeds(r.Context(), p.UserID, limit, offset)
	if err != nil {
		s.log.ErrorContext(r.Context(), "list feeds failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out := make([]feedDTO, 0, len(feeds))
	for _, f := range feeds {
		out = append(out, toFeedDTO(f))
	}
	writeJSON(w, http.StatusOK, listResponse[[]feedDTO]{Data: out, Total: total})
}

func (s *Server) handleCreateFeed(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.feeds == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "feed storage is not configured")
		}
		return
	}
	var req feedWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params, err := validateFeedWriteRequest(req, s.ssrfGuard)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if params.Title == "" && s.titleResolver != nil {
		if t, err := s.titleResolver.DiscoverTitle(r.Context(), params.FeedURL, params.FeedType, params.TLSInsecure); err == nil && strings.TrimSpace(t) != "" {
			params.Title = strings.TrimSpace(t)
		}
	}
	if params.Title == "" {
		params.Title = params.FeedURL
	}
	feed, err := s.feeds.CreateFeed(r.Context(), p.UserID, params)
	if err != nil {
		if errors.Is(err, storage.ErrDuplicateFeedURL) {
			writeError(w, http.StatusConflict, "feed_url already exists")
			return
		}
		if errors.Is(err, storage.ErrInvalidReference) {
			writeError(w, http.StatusBadRequest, "invalid category_id")
			return
		}
		s.log.ErrorContext(r.Context(), "create feed failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, listResponse[feedDTO]{Data: toFeedDTO(feed), Total: 1})
}

func (s *Server) handleGetFeed(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.feeds == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "feed storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	feed, err := s.feeds.GetFeed(r.Context(), p.UserID, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "feed not found")
			return
		}
		s.log.ErrorContext(r.Context(), "get feed failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[feedDTO]{Data: toFeedDTO(feed), Total: 1})
}

func (s *Server) handleUpdateFeed(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.feeds == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "feed storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req feedWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params, err := validateFeedWriteRequest(req, s.ssrfGuard)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	feed, err := s.feeds.UpdateFeed(r.Context(), p.UserID, storage.UpdateFeedParams{
		ID:                 id,
		FeedURL:            params.FeedURL,
		Title:              params.Title,
		CategoryID:         params.CategoryID,
		IntervalMinutes:    params.IntervalMinutes,
		ScraperRules:       params.ScraperRules,
		RewriteRules:       params.RewriteRules,
		BlockedRules:       params.BlockedRules,
		KeepRules:          params.KeepRules,
		FetchViaProxy:      params.FetchViaProxy,
		TLSInsecure:        params.TLSInsecure,
		Crawler:            params.Crawler,
		UserAgent:          params.UserAgent,
		StoreHashOnly:      params.StoreHashOnly,
		EntryRetentionDays: params.EntryRetentionDays,
	})
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrNotFound):
			writeError(w, http.StatusNotFound, "feed not found")
			return
		case errors.Is(err, storage.ErrDuplicateFeedURL):
			writeError(w, http.StatusConflict, "feed_url already exists")
			return
		case errors.Is(err, storage.ErrInvalidReference):
			writeError(w, http.StatusBadRequest, "invalid category_id")
			return
		default:
			s.log.ErrorContext(r.Context(), "update feed failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}
	if s.refresher != nil {
		_ = s.refresher.RescheduleFeed(r.Context(), id, params.IntervalMinutes)
	}
	writeJSON(w, http.StatusOK, listResponse[feedDTO]{Data: toFeedDTO(feed), Total: 1})
}

func (s *Server) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.feeds == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "feed storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.feeds.DeleteFeed(r.Context(), p.UserID, id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "feed not found")
			return
		}
		s.log.ErrorContext(r.Context(), "delete feed failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[deletedDTO]{Data: deletedDTO{Deleted: true}, Total: 1})
}

func validateFeedWriteRequest(req feedWriteRequest, guard *ssrf.Guard) (storage.CreateFeedParams, error) {
	feedURL := strings.TrimSpace(req.FeedURL)
	if err := validateFeedURL(feedURL, guard); err != nil {
		return storage.CreateFeedParams{}, err
	}
	interval := req.IntervalMinutes
	if interval == 0 {
		interval = defaultIntervalMinutes
	}
	if interval < storage.MinFeedIntervalMinutes || interval > storage.MaxFeedIntervalMinutes {
		return storage.CreateFeedParams{}, fmt.Errorf("interval_minutes must be between %d and %d", storage.MinFeedIntervalMinutes, storage.MaxFeedIntervalMinutes)
	}
	feedType := reader.DetectFeedTypeFromURL(feedURL)
	if err := storage.ValidateEntryRetentionDays(req.EntryRetentionDays); err != nil {
		return storage.CreateFeedParams{}, err
	}
	return storage.CreateFeedParams{
		FeedURL:            feedURL,
		FeedType:           feedType,
		Title:              strings.TrimSpace(req.Title),
		CategoryID:         req.CategoryID,
		IntervalMinutes:    interval,
		ScraperRules:       strings.TrimSpace(req.ScraperRules),
		RewriteRules:       req.RewriteRules,
		BlockedRules:       req.BlockedRules,
		KeepRules:          req.KeepRules,
		FetchViaProxy:      req.FetchViaProxy,
		TLSInsecure:        req.TLSInsecure,
		Crawler:            req.Crawler,
		UserAgent:          strings.TrimSpace(req.UserAgent),
		StoreHashOnly:      req.StoreHashOnly,
		EntryRetentionDays: req.EntryRetentionDays,
	}, nil
}

func toFeedDTO(f storage.Feed) feedDTO {
	ft := strings.TrimSpace(f.FeedType)
	if ft == "" {
		ft = reader.FeedTypeRSS
	}
	return feedDTO{
		ID:                 f.ID,
		FeedURL:            f.FeedURL,
		FeedType:           ft,
		Title:              f.Title,
		CategoryID:         f.CategoryID,
		IntervalMinutes:    f.IntervalMinutes,
		ScraperRules:       f.ScraperRules,
		RewriteRules:       f.RewriteRules,
		BlockedRules:       f.BlockedRules,
		KeepRules:          f.KeepRules,
		FetchViaProxy:      f.FetchViaProxy,
		TLSInsecure:        f.TLSInsecure,
		Crawler:            f.Crawler,
		UserAgent:          f.UserAgent,
		StoreHashOnly:      f.StoreHashOnly,
		EntryRetentionDays: f.EntryRetentionDays,
	}
}

type refreshResultDTO struct {
	Inserted int `json:"inserted"`
}

func (s *Server) handleRefreshFeed(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok {
		return
	}
	if s.feeds == nil {
		writeError(w, http.StatusServiceUnavailable, "feed storage is not configured")
		return
	}
	if s.entries == nil {
		writeError(w, http.StatusServiceUnavailable, "entry storage is not configured")
		return
	}
	feedID, err := parsePathInt64(r, "feedID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if _, err := s.feeds.GetFeed(ctx, p.UserID, feedID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "feed not found")
			return
		}
		s.log.ErrorContext(r.Context(), "refresh feed lookup failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	inserted, err := s.refresher.RefreshFeedManual(ctx, feedID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "feed not found")
			return
		}
		if errors.Is(err, service.ErrFetchFeed) {
			writeError(w, http.StatusBadGateway, "feed fetch failed")
			return
		}
		if at, ok := reader.RetryAt(err); ok {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(time.Until(at).Seconds()))))
			writeError(w, http.StatusServiceUnavailable, "source rate limited, retry later")
			return
		}
		s.log.ErrorContext(r.Context(), "refresh feed failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, listResponse[refreshResultDTO]{Data: refreshResultDTO{Inserted: inserted}, Total: 1})
}
