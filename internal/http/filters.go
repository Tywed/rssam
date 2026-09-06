package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rssam/internal/filter"
	"rssam/internal/storage"
)

type filterRuleDTO struct {
	ID       int64  `json:"id"`
	Field    string `json:"field"`
	Pattern  string `json:"pattern"`
	Negate   bool   `json:"negate"`
	Op       string `json:"op"`
	Priority int    `json:"priority"`
}

type filterScopeItemDTO struct {
	ID         int64  `json:"id"`
	FeedID     *int64 `json:"feed_id,omitempty"`
	CategoryID *int64 `json:"category_id,omitempty"`
}

type filterActionDTO struct {
	ID          int64  `json:"id"`
	ActionType  string `json:"action_type"`
	ActionParam string `json:"action_param"`
	Priority    int    `json:"priority"`
}

type filterDTO struct {
	ID           int64                `json:"id"`
	UserID       int64                `json:"user_id"`
	Name         string               `json:"name"`
	Enabled      bool                 `json:"enabled"`
	MatchAnyRule bool                 `json:"match_any_rule"`
	Inverse      bool                 `json:"inverse"`
	OrderID      int                  `json:"order_id"`
	FeedScope    string               `json:"feed_scope"`
	MatchCount   int64                `json:"match_count"`
	Rules        []filterRuleDTO      `json:"rules"`
	ScopeItems   []filterScopeItemDTO `json:"scope_items"`
	Actions      []filterActionDTO    `json:"actions"`
}

type filterRuleWriteRequest struct {
	Field    string `json:"field"`
	Pattern  string `json:"pattern"`
	Negate   bool   `json:"negate"`
	Op       string `json:"op"`
	Priority int    `json:"priority"`
}

type filterScopeItemWriteRequest struct {
	FeedID     *int64 `json:"feed_id"`
	CategoryID *int64 `json:"category_id"`
}

type filterActionWriteRequest struct {
	ActionType  string `json:"action_type"`
	ActionParam string `json:"action_param"`
	Priority    int    `json:"priority"`
}

type filterWriteRequest struct {
	Name         string                        `json:"name"`
	Enabled      *bool                         `json:"enabled"`
	MatchAnyRule *bool                         `json:"match_any_rule"`
	Inverse      *bool                         `json:"inverse"`
	OrderID      *int                          `json:"order_id"`
	FeedScope    *string                       `json:"feed_scope"`
	Rules        []filterRuleWriteRequest      `json:"rules"`
	ScopeItems   []filterScopeItemWriteRequest `json:"scope_items"`
	Actions      []filterActionWriteRequest    `json:"actions"`
}

func (s *Server) handleListFilters(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.filters == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "filter storage is not configured")
		}
		return
	}
	limit, offset, err := parseLimitOffset(r, 100, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filters, total, err := s.filters.ListFilters(r.Context(), p.UserID, limit, offset)
	if err != nil {
		s.log.Error("list filters failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out := make([]filterDTO, 0, len(filters))
	for _, f := range filters {
		out = append(out, toFilterDTO(f))
	}
	writeJSON(w, http.StatusOK, listResponse[[]filterDTO]{Data: out, Total: total})
}

func (s *Server) handleCreateFilter(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.filters == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "filter storage is not configured")
		}
		return
	}
	var req filterWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params, err := filterWriteToParams(p.UserID, 0, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRuleLimits(req.Rules, s.filterEngine); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f, err := s.filters.CreateFilter(r.Context(), params)
	if err != nil {
		s.log.Error("create filter failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if s.refresher != nil {
		s.refresher.InvalidateFilterCache(p.UserID)
	}
	writeJSON(w, http.StatusCreated, listResponse[filterDTO]{Data: toFilterDTO(f), Total: 1})
}

func (s *Server) handleGetFilter(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.filters == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "filter storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f, err := s.filters.GetFilter(r.Context(), p.UserID, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "filter not found")
			return
		}
		s.log.Error("get filter failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[filterDTO]{Data: toFilterDTO(f), Total: 1})
}

func (s *Server) handleUpdateFilter(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.filters == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "filter storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req filterWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params, err := filterWriteToUpdateParams(p.UserID, id, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRuleLimits(req.Rules, s.filterEngine); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f, err := s.filters.UpdateFilter(r.Context(), params)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "filter not found")
			return
		}
		s.log.Error("update filter failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if s.refresher != nil {
		s.refresher.InvalidateFilterCache(p.UserID)
	}
	writeJSON(w, http.StatusOK, listResponse[filterDTO]{Data: toFilterDTO(f), Total: 1})
}

func (s *Server) handleDeleteFilter(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.filters == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "filter storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.filters.DeleteFilter(r.Context(), p.UserID, id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "filter not found")
			return
		}
		s.log.Error("delete filter failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if s.refresher != nil {
		s.refresher.InvalidateFilterCache(p.UserID)
	}
	writeJSON(w, http.StatusOK, listResponse[deletedDTO]{Data: deletedDTO{Deleted: true}, Total: 1})
}

type filterTestEntry struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Author  *string  `json:"author"`
	URL     string   `json:"url"`
	Tags    []string `json:"tags"`
	FeedID  *int64   `json:"feed_id"`
}

type filterTestRequest struct {
	Text   *string          `json:"text"`
	Entry  *filterTestEntry `json:"entry"`
	Limit  *int             `json:"limit"`
	FeedID *int64           `json:"feed_id"`
}

type filterTestMatchRow struct {
	EntryID int64           `json:"entry_id"`
	Title   string          `json:"title"`
	Match   bool            `json:"match"`
	Details json.RawMessage `json:"details,omitempty"`
}

type filterTestResponse struct {
	Match   bool                 `json:"match"`
	Details json.RawMessage      `json:"details,omitempty"`
	Results []filterTestMatchRow `json:"results,omitempty"`
}

func (s *Server) handleTestFilter(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.filters == nil || s.filterEngine == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "filter engine is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filterObj, err := s.filters.GetFilter(r.Context(), p.UserID, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "filter not found")
			return
		}
		s.log.Error("get filter failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var req filterTestRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	limit := 0
	if req.Limit != nil && *req.Limit > 0 {
		limit = *req.Limit
	}
	if limit == 0 {
		if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
	}
	if limit > 0 {
		if s.entries == nil {
			writeError(w, http.StatusServiceUnavailable, "entry storage is not configured")
			return
		}
		if limit > 100 {
			limit = 100
		}
		entries, _, err := s.entries.ListEntries(r.Context(), p.UserID, storage.ListEntriesFilter{Limit: limit, Offset: 0})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		resp := filterTestResponse{Results: make([]filterTestMatchRow, 0, len(entries))}
		for _, e := range entries {
			matchCtx := filter.MatchContext{FeedID: e.FeedID}
			if req.FeedID != nil {
				matchCtx.FeedID = *req.FeedID
			}
			matches, err := s.filterEngine.MatchEntryWithContextStrict(e, matchCtx, []storage.Filter{filterObj})
			row := filterTestMatchRow{EntryID: e.ID, Title: e.Title, Match: len(matches) > 0}
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if len(matches) > 0 {
				row.Details = matches[0].Details
			}
			resp.Results = append(resp.Results, row)
			if row.Match {
				resp.Match = true
			}
		}
		writeJSON(w, http.StatusOK, listResponse[filterTestResponse]{Data: resp, Total: len(resp.Results)})
		return
	}

	var entry storage.Entry
	var matchCtx filter.MatchContext
	if req.Entry != nil {
		entry = storage.Entry{
			Title:   req.Entry.Title,
			Content: req.Entry.Content,
			Author:  req.Entry.Author,
			URL:     req.Entry.URL,
		}
		if req.Entry.FeedID != nil {
			entry.FeedID = *req.Entry.FeedID
			matchCtx.FeedID = *req.Entry.FeedID
		}
	} else if req.Text != nil {
		entry = storage.Entry{Content: *req.Text}
	} else {
		writeError(w, http.StatusBadRequest, "entry, text, or limit is required")
		return
	}

	matches, err := s.filterEngine.MatchEntryWithContextStrict(entry, matchCtx, []storage.Filter{filterObj})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp := filterTestResponse{Match: len(matches) > 0}
	if len(matches) > 0 {
		resp.Details = matches[0].Details
	}
	writeJSON(w, http.StatusOK, listResponse[filterTestResponse]{Data: resp, Total: 1})
}

type filterMatchRowDTO struct {
	ID        int64           `json:"id"`
	FilterID  int64           `json:"filter_id"`
	EntryID   int64           `json:"entry_id"`
	MatchedAt time.Time       `json:"matched_at"`
	Details   json.RawMessage `json:"details,omitempty"`
	Entry     entryDTO        `json:"entry"`
}

func (s *Server) handleListFilterMatches(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.filterMatches == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "filter match storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.filters != nil {
		if _, err := s.filters.GetFilter(r.Context(), p.UserID, id); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "filter not found")
				return
			}
		}
	}
	limit, offset, err := parseLimitOffset(r, 100, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, total, err := s.filterMatches.ListFilterMatches(r.Context(), id, limit, offset)
	if err != nil {
		s.log.Error("list filter matches failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out := make([]filterMatchRowDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, filterMatchRowDTO{
			ID:        row.Match.ID,
			FilterID:  row.Match.FilterID,
			EntryID:   row.Match.EntryID,
			MatchedAt: row.Match.MatchedAt,
			Details:   row.Match.Details,
			Entry:     toEntryDTO(row.Entry, nil),
		})
	}
	writeJSON(w, http.StatusOK, listResponse[[]filterMatchRowDTO]{Data: out, Total: total})
}

func toFilterDTO(f storage.Filter) filterDTO {
	out := filterDTO{
		ID:           f.ID,
		UserID:       f.UserID,
		Name:         f.Name,
		Enabled:      f.Enabled,
		MatchAnyRule: f.MatchAnyRule,
		Inverse:      f.Inverse,
		OrderID:      f.OrderID,
		FeedScope:    f.FeedScope,
		MatchCount:   f.MatchCount,
		Rules:        make([]filterRuleDTO, 0, len(f.Rules)),
		ScopeItems:   make([]filterScopeItemDTO, 0, len(f.ScopeItems)),
		Actions:      make([]filterActionDTO, 0, len(f.Actions)),
	}
	for _, r := range f.Rules {
		out.Rules = append(out.Rules, filterRuleDTO{
			ID:       r.ID,
			Field:    r.Field,
			Pattern:  r.Pattern,
			Negate:   r.Negate,
			Op:       r.Op,
			Priority: r.Priority,
		})
	}
	for _, item := range f.ScopeItems {
		out.ScopeItems = append(out.ScopeItems, filterScopeItemDTO{
			ID:         item.ID,
			FeedID:     item.FeedID,
			CategoryID: item.CategoryID,
		})
	}
	for _, a := range f.Actions {
		out.Actions = append(out.Actions, filterActionDTO{
			ID:          a.ID,
			ActionType:  a.ActionType,
			ActionParam: a.ActionParam,
			Priority:    a.Priority,
		})
	}
	return out
}

func filterWriteToParams(userID int64, _ int64, req filterWriteRequest) (storage.CreateFilterParams, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return storage.CreateFilterParams{}, errors.New("name is required")
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	matchAny := false
	if req.MatchAnyRule != nil {
		matchAny = *req.MatchAnyRule
	}
	inverse := false
	if req.Inverse != nil {
		inverse = *req.Inverse
	}
	orderID := 0
	if req.OrderID != nil {
		orderID = *req.OrderID
	}
	feedScope := storage.FilterFeedScopeAll
	if req.FeedScope != nil {
		feedScope = *req.FeedScope
	}
	return storage.CreateFilterParams{
		UserID:       userID,
		Name:         name,
		Enabled:      enabled,
		MatchAnyRule: matchAny,
		Inverse:      inverse,
		OrderID:      orderID,
		FeedScope:    feedScope,
		Rules:        toCreateRules(req.Rules),
		ScopeItems:   toCreateScopeItems(req.ScopeItems),
		Actions:      toCreateActions(req.Actions),
	}, nil
}

func filterWriteToUpdateParams(userID, id int64, req filterWriteRequest) (storage.UpdateFilterParams, error) {
	p, err := filterWriteToParams(userID, id, req)
	if err != nil {
		return storage.UpdateFilterParams{}, err
	}
	return storage.UpdateFilterParams{
		ID:           id,
		UserID:       userID,
		Name:         p.Name,
		Enabled:      p.Enabled,
		MatchAnyRule: p.MatchAnyRule,
		Inverse:      p.Inverse,
		OrderID:      p.OrderID,
		FeedScope:    p.FeedScope,
		Rules:        p.Rules,
		ScopeItems:   p.ScopeItems,
		Actions:      p.Actions,
	}, nil
}

func toCreateRules(rules []filterRuleWriteRequest) []storage.CreateFilterRuleParams {
	out := make([]storage.CreateFilterRuleParams, 0, len(rules))
	for _, r := range rules {
		out = append(out, storage.CreateFilterRuleParams{
			Field:    r.Field,
			Pattern:  r.Pattern,
			Negate:   r.Negate,
			Op:       r.Op,
			Priority: r.Priority,
		})
	}
	return out
}

func toCreateScopeItems(items []filterScopeItemWriteRequest) []storage.CreateFilterScopeItemParams {
	out := make([]storage.CreateFilterScopeItemParams, 0, len(items))
	for _, item := range items {
		out = append(out, storage.CreateFilterScopeItemParams{
			FeedID:     item.FeedID,
			CategoryID: item.CategoryID,
		})
	}
	return out
}

func toCreateActions(actions []filterActionWriteRequest) []storage.CreateFilterActionParams {
	out := make([]storage.CreateFilterActionParams, 0, len(actions))
	for _, a := range actions {
		out = append(out, storage.CreateFilterActionParams{
			ActionType:  a.ActionType,
			ActionParam: a.ActionParam,
			Priority:    a.Priority,
		})
	}
	return out
}

func validateRuleLimits(rules []filterRuleWriteRequest, eng *filter.Engine) error {
	if eng == nil {
		return nil
	}
	return eng.ValidateRules(toCreateRules(rules))
}
