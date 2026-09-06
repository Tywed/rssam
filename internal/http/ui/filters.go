package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"rssam/internal/filter"
	"rssam/internal/storage"
)

func (h *Handler) handleFiltersList(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	p, _ := principal(r)
	data := h.baseData(r, "settings")
	data.SettingsSection = "filters"
	filters, _, err := h.cfg.Filters.ListFilters(r.Context(), p.UserID, 1000, 0)
	if err != nil {
		http.Error(w, "list filters failed", http.StatusInternalServerError)
		return
	}
	data.Filters = filters
	data.Title = "Фильтры"
	h.render(w, r, "filters_list", data)
}

func (h *Handler) handleFilterNew(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	data := h.prepareFilterFormData(r)
	data.Title = "Новый фильтр"
	h.render(w, r, "filters_form", data)
}

func (h *Handler) handleFilterEdit(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f, err := h.cfg.Filters.GetFilter(r.Context(), p.UserID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data := h.prepareFilterFormData(r)
	data.Filter = f
	data.ScopeFeedIDs, data.ScopeCategoryIDs = scopeIDMaps(f.ScopeItems)
	data.ScopeFeeds = h.hydrateScopeFeeds(r.Context(), p.UserID, f.ScopeItems)
	data.Title = f.Name
	h.render(w, r, "filters_form", data)
}

func (h *Handler) hydrateScopeFeeds(ctx context.Context, userID int64, items []storage.FilterScopeItem) []storage.Feed {
	if h.cfg.Feeds == nil || len(items) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(items))
	seen := make(map[int64]struct{}, len(items))
	for _, item := range items {
		if item.FeedID == nil || *item.FeedID <= 0 {
			continue
		}
		id := *item.FeedID
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	feeds, err := h.cfg.Feeds.ListFeedsByIDs(ctx, userID, ids)
	if err != nil {
		return nil
	}
	return feeds
}

func (h *Handler) prepareFilterFormData(r *http.Request) pageData {
	data := h.baseData(r, "settings")
	data.SettingsSection = "filters"
	p, _ := principal(r)
	if h.cfg.Webhooks != nil {
		whs, _, _ := h.cfg.Webhooks.ListWebhooks(r.Context(), p.UserID, 500, 0)
		data.Webhooks = whs
	}
	if h.cfg.Labels != nil {
		labels, _, _ := h.cfg.Labels.ListLabels(r.Context(), p.UserID, 500, 0)
		data.Labels = labels
	}
	return data
}

func scopeIDMaps(items []storage.FilterScopeItem) (feeds map[int64]bool, cats map[int64]bool) {
	feeds = map[int64]bool{}
	cats = map[int64]bool{}
	for _, item := range items {
		if item.FeedID != nil {
			feeds[*item.FeedID] = true
		}
		if item.CategoryID != nil {
			cats[*item.CategoryID] = true
		}
	}
	return feeds, cats
}

func (h *Handler) handleFilterCreate(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	p, _ := principal(r)
	params, err := filterParamsFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.cfg.FilterEngine != nil {
		if err := h.cfg.FilterEngine.ValidateRules(params.Rules); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	params.UserID = p.UserID
	if _, err := h.cfg.Filters.CreateFilter(r.Context(), params); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.cfg.Refresher != nil {
		h.cfg.Refresher.InvalidateFilterCache(p.UserID)
	}
	http.Redirect(w, r, "/ui/filters", http.StatusFound)
}

func (h *Handler) handleFilterUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	params, err := filterParamsFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.cfg.FilterEngine != nil {
		if err := h.cfg.FilterEngine.ValidateRules(params.Rules); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	_, err = h.cfg.Filters.UpdateFilter(r.Context(), storage.UpdateFilterParams{
		ID:           id,
		UserID:       p.UserID,
		Name:         params.Name,
		Enabled:      params.Enabled,
		MatchAnyRule: params.MatchAnyRule,
		Inverse:      params.Inverse,
		OrderID:      params.OrderID,
		FeedScope:    params.FeedScope,
		Rules:        params.Rules,
		ScopeItems:   params.ScopeItems,
		Actions:      params.Actions,
	})
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if h.cfg.Refresher != nil {
		h.cfg.Refresher.InvalidateFilterCache(p.UserID)
	}
	http.Redirect(w, r, "/ui/filters/"+strings.TrimSpace(r.PathValue("id")), http.StatusFound)
}

func (h *Handler) handleFilterDelete(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.cfg.Filters.DeleteFilter(r.Context(), p.UserID, id); err != nil {
		http.NotFound(w, r)
		return
	}
	if h.cfg.Refresher != nil {
		h.cfg.Refresher.InvalidateFilterCache(p.UserID)
	}
	http.Redirect(w, r, "/ui/filters", http.StatusFound)
}

func (h *Handler) handleFilterTest(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f, err := h.cfg.Filters.GetFilter(r.Context(), p.UserID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data := h.baseData(r, "settings")
	data.SettingsSection = "filters"
	data.Filter = f
	limit := 20
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	if h.cfg.FilterEngine != nil && h.cfg.Entries != nil {
		entries, _, _ := h.cfg.Entries.ListEntries(r.Context(), p.UserID, storage.ListEntriesFilter{Limit: limit, Offset: 0})
		var preview []storage.FilterMatchWithEntry
		for _, e := range entries {
			matchCtx := filter.MatchContext{FeedID: e.FeedID}
			matches, matchErr := h.cfg.FilterEngine.MatchEntryWithContext(e, matchCtx, []storage.Filter{f})
			if matchErr != nil || len(matches) == 0 {
				continue
			}
			preview = append(preview, storage.FilterMatchWithEntry{
				Match: storage.FilterMatch{FilterID: f.ID, EntryID: e.ID, MatchedAt: e.CreatedAt, Details: matches[0].Details},
				Entry: e,
			})
		}
		data.Matches = preview
	}
	data.Title = "Тест: " + f.Name
	h.render(w, r, "filters_test", data)
}

func (h *Handler) handleFilterMatches(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f, err := h.cfg.Filters.GetFilter(r.Context(), p.UserID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	matches, _, err := h.cfg.FilterMatches.ListFilterMatches(r.Context(), id, 100, 0)
	if err != nil {
		http.Error(w, "list matches failed", http.StatusInternalServerError)
		return
	}
	data := h.baseData(r, "settings")
	data.SettingsSection = "filters"
	data.Filter = f
	data.Matches = matches
	data.Title = "Совпадения"
	h.render(w, r, "filters_matches", data)
}

func filterParamsFromForm(r *http.Request) (storage.CreateFilterParams, error) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return storage.CreateFilterParams{}, errRequired("name")
	}
	enabled := r.FormValue("enabled") == "1"
	matchAny := r.FormValue("match_any_rule") == "1"
	inverse := r.FormValue("inverse") == "1"
	orderID := 0
	if v := strings.TrimSpace(r.FormValue("order_id")); v != "" {
		if n, err := strconvAtoi(v); err == nil {
			orderID = n
		}
	}
	feedScope := strings.TrimSpace(r.FormValue("feed_scope"))
	if feedScope == "" {
		feedScope = storage.FilterFeedScopeAll
	}

	rules := parseRulesFromForm(r)
	scopeItems := parseScopeFromForm(r)
	actions := parseActionsFromForm(r)

	return storage.CreateFilterParams{
		Name:         name,
		Enabled:      enabled,
		MatchAnyRule: matchAny,
		Inverse:      inverse,
		OrderID:      orderID,
		FeedScope:    feedScope,
		Rules:        rules,
		ScopeItems:   scopeItems,
		Actions:      actions,
	}, nil
}

func parseRulesFromForm(r *http.Request) []storage.CreateFilterRuleParams {
	var out []storage.CreateFilterRuleParams
	fields := r.Form["rule_field"]
	patterns := r.Form["rule_pattern"]
	negates := r.Form["rule_negate"]
	for i, field := range fields {
		pattern := ""
		if i < len(patterns) {
			pattern = strings.TrimSpace(patterns[i])
		}
		if strings.TrimSpace(field) == "" || pattern == "" {
			continue
		}
		negate := false
		if i < len(negates) && negates[i] == "1" {
			negate = true
		}
		out = append(out, storage.CreateFilterRuleParams{
			Field:    field,
			Pattern:  pattern,
			Negate:   negate,
			Op:       "and",
			Priority: i,
		})
	}
	if len(out) == 0 {
		if raw := strings.TrimSpace(r.FormValue("rules_json")); raw != "" && raw != "[]" {
			type ruleJSON struct {
				Field    string `json:"field"`
				Pattern  string `json:"pattern"`
				Negate   bool   `json:"negate"`
				Op       string `json:"op"`
				Priority int    `json:"priority"`
			}
			var rules []ruleJSON
			if err := json.Unmarshal([]byte(raw), &rules); err == nil {
				for _, ru := range rules {
					out = append(out, storage.CreateFilterRuleParams{
						Field:    ru.Field,
						Pattern:  ru.Pattern,
						Negate:   ru.Negate,
						Op:       ru.Op,
						Priority: ru.Priority,
					})
				}
			}
		}
	}
	return out
}

func parseScopeFromForm(r *http.Request) []storage.CreateFilterScopeItemParams {
	var out []storage.CreateFilterScopeItemParams
	for _, v := range r.Form["scope_feed_id"] {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			out = append(out, storage.CreateFilterScopeItemParams{FeedID: &id})
		}
	}
	for _, v := range r.Form["scope_category_id"] {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			out = append(out, storage.CreateFilterScopeItemParams{CategoryID: &id})
		}
	}
	return out
}

func parseActionsFromForm(r *http.Request) []storage.CreateFilterActionParams {
	types := r.Form["action_type"]
	params := r.Form["action_param"]
	var out []storage.CreateFilterActionParams
	for i, actionType := range types {
		actionType = strings.TrimSpace(actionType)
		if actionType == "" {
			continue
		}
		param := ""
		if i < len(params) {
			param = parseFilterActionParam(actionType, params[i])
		}
		out = append(out, storage.CreateFilterActionParams{
			ActionType:  actionType,
			ActionParam: param,
			Priority:    i,
		})
	}
	return out
}

// filterActionOptionValue is the <option value> for a label/webhook so IDs
// from different tables do not collide in one <select> (label 1 vs webhook 1).
func filterActionOptionValue(kind string, id int64) string {
	return strings.TrimSpace(kind) + ":" + strconv.FormatInt(id, 10)
}

func filterActionSavedOptionValue(actionType, param string) string {
	actionType = strings.TrimSpace(actionType)
	param = strings.TrimSpace(param)
	if actionType == "" || actionType == "delete" || param == "" {
		return ""
	}
	return actionType + ":" + param
}

func parseFilterActionParam(actionType, raw string) string {
	raw = strings.TrimSpace(raw)
	actionType = strings.TrimSpace(actionType)
	if raw == "" || actionType == "" || actionType == "delete" {
		return ""
	}
	prefix := actionType + ":"
	if strings.HasPrefix(raw, prefix) {
		return strings.TrimSpace(raw[len(prefix):])
	}
	if i := strings.IndexByte(raw, ':'); i > 0 {
		kind := raw[:i]
		if kind == "label" || kind == "webhook" {
			return ""
		}
	}
	return raw
}
