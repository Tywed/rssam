package filter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"rssam/internal/storage"
)

type Config struct {
	MaxRulesPerFilter int
	MaxRegexLength    int
	CompileTimeout    time.Duration
	MatchTimeout      time.Duration
}

type Engine struct {
	cfg Config

	mu    sync.RWMutex
	cache map[cacheKey]*compiledFilter
}

type cacheKey struct {
	id  int64
	ver int64 // updated_at unix nanos
}

type compiledFilter struct {
	filterID     int64
	matchAnyRule bool
	inverse      bool
	feedScope    string
	scopeItems   []storage.FilterScopeItem
	rules        []compiledRule
}

type compiledRule struct {
	ruleID   int64
	field    string
	pattern  string
	negate   bool
	op       string
	priority int
	re       *regexp.Regexp
}

type MatchContext struct {
	FeedID     int64
	CategoryID *int64
}

type MatchResult struct {
	FilterID int64           `json:"filter_id"`
	Matched  bool            `json:"matched"`
	Rules    []RuleMatchInfo `json:"rules"`
}

type RuleMatchInfo struct {
	RuleID  int64  `json:"rule_id"`
	Field   string `json:"field"`
	Pattern string `json:"pattern"`
	Negate  bool   `json:"negate"`
	Op      string `json:"op"`
	Matched bool   `json:"matched"`
}

func New(cfg Config) *Engine {
	if cfg.MaxRulesPerFilter <= 0 {
		cfg.MaxRulesPerFilter = 50
	}
	if cfg.MaxRegexLength <= 0 {
		cfg.MaxRegexLength = 2048
	}
	if cfg.CompileTimeout <= 0 {
		cfg.CompileTimeout = 100 * time.Millisecond
	}
	if cfg.MatchTimeout <= 0 {
		cfg.MatchTimeout = 50 * time.Millisecond
	}
	return &Engine{cfg: cfg, cache: map[cacheKey]*compiledFilter{}}
}

func (e *Engine) Limits() (maxRulesPerFilter int, maxRegexLength int) {
	return e.cfg.MaxRulesPerFilter, e.cfg.MaxRegexLength
}

type Match struct {
	FilterID int64
	Details  []byte
}

func (e *Engine) MatchEntry(entry storage.Entry, filters []storage.Filter) ([]Match, error) {
	return e.MatchEntryWithContext(entry, MatchContext{FeedID: entry.FeedID}, filters)
}

func (e *Engine) MatchEntryWithContext(entry storage.Entry, ctx MatchContext, filters []storage.Filter) ([]Match, error) {
	return e.matchEntry(entry, ctx, filters, false)
}

// MatchAll is an alias for MatchEntry (spec §8.1 naming).
func (e *Engine) MatchAll(c context.Context, entry storage.Entry, filters []storage.Filter) ([]Match, error) {
	_ = c
	return e.MatchEntry(entry, filters)
}

func (e *Engine) MatchEntryStrict(entry storage.Entry, filters []storage.Filter) ([]Match, error) {
	return e.MatchEntryWithContextStrict(entry, MatchContext{FeedID: entry.FeedID}, filters)
}

func (e *Engine) MatchEntryWithContextStrict(entry storage.Entry, ctx MatchContext, filters []storage.Filter) ([]Match, error) {
	return e.matchEntry(entry, ctx, filters, true)
}

func (e *Engine) matchEntry(entry storage.Entry, ctx MatchContext, filters []storage.Filter, strict bool) ([]Match, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	out := make([]Match, 0, 8)
	for _, f := range filters {
		cf, err := e.getCompiled(f)
		if err != nil {
			if strict {
				return nil, err
			}
			continue
		}
		if !matchesFeedScope(cf, ctx) {
			continue
		}
		mr, err := e.matchCompiledSafe(entry, cf)
		if err != nil {
			if strict {
				return nil, err
			}
			continue
		}
		if !mr.Matched {
			continue
		}
		details, _ := json.Marshal(mr)
		out = append(out, Match{FilterID: f.ID, Details: details})
	}
	return out, nil
}

func matchesFeedScope(cf *compiledFilter, ctx MatchContext) bool {
	switch cf.feedScope {
	case "", storage.FilterFeedScopeAll:
		return true
	case storage.FilterFeedScopeInclude:
		if len(cf.scopeItems) == 0 {
			return false
		}
		for _, item := range cf.scopeItems {
			if item.FeedID != nil && *item.FeedID == ctx.FeedID {
				return true
			}
			if item.CategoryID != nil && ctx.CategoryID != nil && *item.CategoryID == *ctx.CategoryID {
				return true
			}
		}
		return false
	case storage.FilterFeedScopeExclude:
		for _, item := range cf.scopeItems {
			if item.FeedID != nil && *item.FeedID == ctx.FeedID {
				return false
			}
			if item.CategoryID != nil && ctx.CategoryID != nil && *item.CategoryID == *ctx.CategoryID {
				return false
			}
		}
		return true
	default:
		return true
	}
}

func (e *Engine) getCompiled(f storage.Filter) (*compiledFilter, error) {
	if len(f.Rules) == 0 {
		return &compiledFilter{
			filterID:     f.ID,
			matchAnyRule: f.MatchAnyRule,
			inverse:      f.Inverse,
			feedScope:    normalizeFeedScope(f.FeedScope),
			scopeItems:   f.ScopeItems,
		}, nil
	}
	if e.cfg.MaxRulesPerFilter > 0 && len(f.Rules) > e.cfg.MaxRulesPerFilter {
		return nil, fmt.Errorf("too many rules: %d", len(f.Rules))
	}
	ver := time.Time{}.UnixNano()
	if !f.UpdatedAt.IsZero() {
		ver = f.UpdatedAt.UTC().UnixNano()
	}
	key := cacheKey{id: f.ID, ver: ver}

	e.mu.RLock()
	cached := e.cache[key]
	e.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	cf, err := compileFilter(f, e.cfg.MaxRegexLength, e.cfg.CompileTimeout)
	if err != nil {
		return nil, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	for k := range e.cache {
		if k.id == f.ID && k.ver != ver {
			delete(e.cache, k)
		}
	}
	e.cache[key] = cf
	return cf, nil
}

func normalizeFeedScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case storage.FilterFeedScopeInclude, storage.FilterFeedScopeExclude:
		return strings.ToLower(strings.TrimSpace(scope))
	default:
		return storage.FilterFeedScopeAll
	}
}

func compileFilter(f storage.Filter, maxRegexLen int, compileTimeout time.Duration) (*compiledFilter, error) {
	cf := &compiledFilter{
		filterID:     f.ID,
		matchAnyRule: f.MatchAnyRule,
		inverse:      f.Inverse,
		feedScope:    normalizeFeedScope(f.FeedScope),
		scopeItems:   f.ScopeItems,
		rules:        make([]compiledRule, 0, len(f.Rules)),
	}
	for _, r := range f.Rules {
		pat := strings.TrimSpace(r.Pattern)
		if pat == "" {
			return nil, errors.New("empty regex is not allowed")
		}
		if maxRegexLen > 0 && len(pat) > maxRegexLen {
			return nil, fmt.Errorf("regex too long: %d", len(pat))
		}
		field := strings.ToLower(strings.TrimSpace(r.Field))
		if !isAllowedField(field) {
			return nil, fmt.Errorf("invalid field: %q", field)
		}
		op := strings.ToLower(strings.TrimSpace(r.Op))
		if op == "" {
			op = "and"
		}
		re, err := compileRegexp(pat, compileTimeout)
		if err != nil {
			return nil, fmt.Errorf("compile regex: %w", err)
		}
		cf.rules = append(cf.rules, compiledRule{
			ruleID:   r.ID,
			field:    field,
			pattern:  pat,
			negate:   r.Negate,
			op:       op,
			priority: r.Priority,
			re:       re,
		})
	}
	return cf, nil
}

func compileRegexp(pat string, _ time.Duration) (*regexp.Regexp, error) {
	return regexp.Compile(pat)
}

func (e *Engine) matchCompiledSafe(entry storage.Entry, f *compiledFilter) (MatchResult, error) {
	return matchCompiled(entry, f), nil
}

func matchCompiled(entry storage.Entry, f *compiledFilter) MatchResult {
	if len(f.rules) == 0 {
		return MatchResult{FilterID: f.filterID, Matched: false}
	}

	agg := false
	initialized := false
	info := make([]RuleMatchInfo, 0, len(f.rules))

	for _, r := range f.rules {
		ok := ruleMatches(entry, r)
		if !initialized {
			agg = ok
			initialized = true
		} else if f.matchAnyRule {
			agg = agg || ok
		} else if r.op == "or" {
			agg = agg || ok
		} else {
			agg = agg && ok
		}
		info = append(info, RuleMatchInfo{
			RuleID:  r.ruleID,
			Field:   r.field,
			Pattern: r.pattern,
			Negate:  r.negate,
			Op:      r.op,
			Matched: ok,
		})
	}

	matched := agg
	if f.inverse {
		matched = !matched
	}

	return MatchResult{FilterID: f.filterID, Matched: matched, Rules: info}
}

func ruleMatches(entry storage.Entry, r compiledRule) bool {
	var ok bool
	if r.field == "both" {
		ok = r.re.MatchString(clipMatchField(entry.Title)) || r.re.MatchString(clipMatchField(entry.Content))
	} else {
		ok = r.re.MatchString(fieldValue(entry, r.field))
	}
	if r.negate {
		ok = !ok
	}
	return ok
}

const maxMatchFieldBytes = 256 * 1024

func fieldValue(e storage.Entry, field string) string {
	var v string
	switch field {
	case "title":
		v = e.Title
	case "content":
		v = e.Content
	case "author":
		if e.Author == nil {
			return ""
		}
		v = *e.Author
	case "url":
		v = e.URL
	case "tags":
		return ""
	default:
		return ""
	}
	return clipMatchField(v)
}

func clipMatchField(s string) string {
	if len(s) <= maxMatchFieldBytes {
		return s
	}
	return s[:maxMatchFieldBytes]
}

func isAllowedField(f string) bool {
	switch f {
	case "title", "content", "both", "author", "url", "tags":
		return true
	default:
		return false
	}
}

// ParseActionParamID parses label/webhook id from action_param.
func ParseActionParamID(param string) (int64, bool) {
	param = strings.TrimSpace(param)
	if param == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(param, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
