package filter

import (
	"context"
	"errors"
	"strings"

	"rssam/internal/storage"
)

// FieldQuery is the rule field whose pattern is a PostgreSQL websearch query
// (morphology, "phrase", -exclude, or) evaluated against the entry tsvector
// in one SQL per batch instead of a per-entry regex.
const FieldQuery = "query"

// FieldTags is accepted only for rows written before 0.1.15; ValidateRules
// refuses it on write.
const FieldTags = "tags"

// ErrQueryRulesUnsupported is returned in strict mode when a filter has
// `query` rules but the caller supplied no QueryHits.
var ErrQueryRulesUnsupported = errors.New("query rules require full-text evaluation")

// QueryMatcher evaluates websearch queries against entries in one round trip.
type QueryMatcher interface {
	MatchEntryQueries(ctx context.Context, items []storage.QueryMatchItem, queries []string) ([][]bool, error)
}

// QueryPatterns returns the distinct `query` patterns of the given filters.
func QueryPatterns(filters []storage.Filter) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, f := range filters {
		for _, r := range f.Rules {
			if strings.ToLower(strings.TrimSpace(r.Field)) != FieldQuery {
				continue
			}
			pat := strings.TrimSpace(r.Pattern)
			if pat == "" {
				continue
			}
			if _, ok := seen[pat]; ok {
				continue
			}
			seen[pat] = struct{}{}
			out = append(out, pat)
		}
	}
	return out
}

// QueryHits evaluates every `query` pattern of filters against items and
// returns one pattern→matched map per item, ready for MatchContext.QueryHits.
// It returns nil, nil when no filter has query rules, so callers pay nothing
// for the feature they do not use.
func QueryHits(ctx context.Context, m QueryMatcher, filters []storage.Filter, items []storage.QueryMatchItem) ([]map[string]bool, error) {
	patterns := QueryPatterns(filters)
	if len(patterns) == 0 || len(items) == 0 {
		return nil, nil
	}
	if m == nil {
		return nil, ErrQueryRulesUnsupported
	}
	rows, err := m.MatchEntryQueries(ctx, items, patterns)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]bool, len(items))
	for i := range items {
		hits := make(map[string]bool, len(patterns))
		for j, pat := range patterns {
			hits[pat] = i < len(rows) && j < len(rows[i]) && rows[i][j]
		}
		out[i] = hits
	}
	return out, nil
}

// QueryItemFromEntry builds a QueryMatchItem; stored entries (ID > 0) are
// matched through their persisted search_vector, unsaved ones through text.
func QueryItemFromEntry(e storage.Entry) storage.QueryMatchItem {
	return storage.QueryMatchItem{EntryID: e.ID, Title: e.Title, Content: e.Content}
}
