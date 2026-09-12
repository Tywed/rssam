package service

import (
	"context"
	"strings"
	"testing"

	"rssam/internal/filter"
	"rssam/internal/storage"
)

type memQueryMatcher struct {
	calls   int
	items   []storage.QueryMatchItem
	queries []string
	match   func(item storage.QueryMatchItem, q string) bool
}

func (m *memQueryMatcher) MatchEntryQueries(_ context.Context, items []storage.QueryMatchItem, queries []string) ([][]bool, error) {
	m.calls++
	m.items, m.queries = items, queries
	out := make([][]bool, len(items))
	for i, it := range items {
		out[i] = make([]bool, len(queries))
		for j, q := range queries {
			out[i][j] = m.match(it, q)
		}
	}
	return out, nil
}

func TestProcessEntriesDedupOnly_QueryRuleBatched(t *testing.T) {
	dedup := &memDedupStore{known: map[string]struct{}{"old": {}}}
	entries := &memEntryCreate{}
	filters := &memFilterList{filters: []storage.Filter{{
		ID: 1, UserID: 1, Enabled: true, FeedScope: storage.FilterFeedScopeAll,
		Rules: []storage.FilterRule{{Field: "query", Pattern: "нефть -прогноз"}},
	}}}
	matcher := &memQueryMatcher{match: func(it storage.QueryMatchItem, q string) bool {
		return strings.Contains(it.Content, "нефти")
	}}
	r := &FeedRefresher{
		Dedup:   dedup,
		Entries: entries,
		Filters: filters,
		Engine:  filter.New(filter.Config{}),
		Queries: matcher,
	}
	feed := storage.Feed{ID: 1, UserID: 1, StoreHashOnly: true}

	inserted, created, fresh, err := r.processEntriesDedupOnly(context.Background(), feed, []storage.CreateEntryParams{
		{Title: "already seen", Hash: "old", Content: "цены на нефти"},
		{Title: "sport", Hash: "h1", Content: "футбол"},
		{Title: "oil", Hash: "h2", Content: "цены на нефти выросли"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 1 || len(created) != 1 || fresh != 2 {
		t.Fatalf("inserted=%d created=%d fresh=%d, want 1/1/2", inserted, len(created), fresh)
	}
	if matcher.calls != 1 {
		t.Fatalf("query matcher must run once per batch, ran %d", matcher.calls)
	}
	if len(matcher.items) != 2 || matcher.items[0].EntryID != 0 || matcher.items[1].Content != "цены на нефти выросли" {
		t.Fatalf("matcher must see only fresh unsaved items: %+v", matcher.items)
	}
	if len(matcher.queries) != 1 || matcher.queries[0] != "нефть -прогноз" {
		t.Fatalf("queries=%q", matcher.queries)
	}
	if len(entries.created) != 1 || entries.created[0].Hash != "h2" {
		t.Fatalf("created=%+v", entries.created)
	}
	if len(dedup.recorded) != 1 || dedup.recorded[0].Hash != "h1" {
		t.Fatalf("dedup recorded=%+v", dedup.recorded)
	}
}

func TestProcessEntriesDedupOnly_QueryRuleWithoutMatcherKeepsRegexRules(t *testing.T) {
	dedup := &memDedupStore{}
	entries := &memEntryCreate{}
	filters := &memFilterList{filters: []storage.Filter{
		{ID: 1, UserID: 1, Enabled: true, Rules: []storage.FilterRule{{Field: "query", Pattern: "нефть"}}},
		{ID: 2, UserID: 1, Enabled: true, Rules: []storage.FilterRule{{Field: "title", Pattern: "oil"}}},
	}}
	r := &FeedRefresher{Dedup: dedup, Entries: entries, Filters: filters, Engine: filter.New(filter.Config{})}
	feed := storage.Feed{ID: 1, UserID: 1, StoreHashOnly: true}

	inserted, _, _, err := r.processEntriesDedupOnly(context.Background(), feed, []storage.CreateEntryParams{
		{Title: "oil", Hash: "h1", Content: "нефть"},
		{Title: "gas", Hash: "h2", Content: "нефть"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 1 || len(entries.created) != 1 || entries.created[0].Hash != "h1" {
		t.Fatalf("regex filter must still work without a query matcher: inserted=%d created=%+v", inserted, entries.created)
	}
}

type memMatchStore struct {
	created []storage.CreateFilterMatchParams
}

func (m *memMatchStore) CreateFilterMatches(_ context.Context, ms []storage.CreateFilterMatchParams) (int, error) {
	m.created = append(m.created, ms...)
	return len(ms), nil
}
func (m *memMatchStore) IncrementFilterMatchCount(context.Context, int64, int64) error { return nil }
func (m *memMatchStore) ListFilterMatches(context.Context, int64, int, int) ([]storage.FilterMatchWithEntry, int, error) {
	return nil, 0, nil
}

func TestApplyFiltersBestEffort_QueryRuleUsesStoredEntryIDs(t *testing.T) {
	labels := &memLabelStore{labels: map[int64]storage.Label{7: {ID: 7, UserID: 1}}}
	matches := &memMatchStore{}
	filters := &memFilterList{filters: []storage.Filter{{
		ID: 1, UserID: 1, Enabled: true,
		Rules:   []storage.FilterRule{{Field: "query", Pattern: "нефть"}},
		Actions: []storage.FilterAction{{ActionType: storage.FilterActionLabel, ActionParam: "7"}},
	}}}
	matcher := &memQueryMatcher{match: func(it storage.QueryMatchItem, q string) bool { return it.EntryID == 11 }}
	r := &FeedRefresher{
		Entries: &memEntryBulk{},
		Filters: filters,
		Matches: matches,
		Labels:  labels,
		Engine:  filter.New(filter.Config{}),
		Queries: matcher,
	}
	feed := storage.Feed{ID: 1, UserID: 1}
	r.applyFiltersBestEffort(context.Background(), feed, []storage.Entry{{ID: 10, FeedID: 1}, {ID: 11, FeedID: 1}, {ID: 12, FeedID: 1}})

	if matcher.calls != 1 || len(matcher.items) != 3 || matcher.items[1].EntryID != 11 {
		t.Fatalf("one batched call with stored ids expected: calls=%d items=%+v", matcher.calls, matcher.items)
	}
	if len(matches.created) != 1 || matches.created[0].EntryID != 11 {
		t.Fatalf("filter_matches=%+v", matches.created)
	}
	if len(labels.assign) != 1 || labels.assign[0].entryID != 11 {
		t.Fatalf("label assign=%+v", labels.assign)
	}
}
