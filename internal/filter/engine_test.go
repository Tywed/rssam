package filter

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"rssam/internal/storage"
)

// rosgvardRE2Pattern is the Go RE2 adaptation of a Perl regex that used (?!\S)
// for a trailing word boundary after ОМОН/СОБР.
const rosgvardRE2Pattern = `[оО]МОН(?:$|[^\p{L}\p{N}_])|[сС]ОБР(?:$|[^\p{L}\p{N}_])|[вВ]неведомствен|[гГ]вардейц|[рР]осгвард|[рР]оссгвард|(?i)ФСВНГ|(?i)rosgvard`

func TestEngine_RosgvardRE2Pattern(t *testing.T) {
	if _, err := regexp.Compile(rosgvardRE2Pattern); err != nil {
		t.Fatalf("compile pattern: %v", err)
	}

	eng := New(Config{})
	now := time.Now().UTC()
	f := storage.Filter{
		ID:        1,
		UpdatedAt: now,
		Rules: []storage.FilterRule{
			{ID: 1, Field: "title", Pattern: rosgvardRE2Pattern, Op: "or"},
		},
	}

	matchTitle := func(title string) bool {
		t.Helper()
		matches, err := eng.MatchEntryStrict(storage.Entry{Title: title}, []storage.Filter{f})
		if err != nil {
			t.Fatalf("match %q: %v", title, err)
		}
		return len(matches) > 0
	}

	for _, title := range []string{"Росгвардия", "ОМОН провел", "rosgvard_krd"} {
		if !matchTitle(title) {
			t.Fatalf("expected match for %q", title)
		}
	}
	if matchTitle("ОМОН123") {
		t.Fatalf("expected no match for ОМОН123 (word boundary after ОМОН)")
	}
}

func TestEngine_AND_OR_Negate(t *testing.T) {
	eng := New(Config{MaxRulesPerFilter: 50, MaxRegexLength: 2048})
	now := time.Now().UTC()

	f := storage.Filter{
		ID:        1,
		UpdatedAt: now,
		Rules: []storage.FilterRule{
			{ID: 11, Field: "title", Pattern: "foo", Op: "and"},
			{ID: 12, Field: "content", Pattern: "bar", Op: "and"},
		},
	}
	entry := storage.Entry{Title: "foo", Content: "xx bar yy"}
	matches, err := eng.MatchEntryStrict(entry, []storage.Filter{f})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected match, got %d", len(matches))
	}

	f2 := storage.Filter{
		ID:        2,
		UpdatedAt: now,
		Rules: []storage.FilterRule{
			{ID: 21, Field: "title", Pattern: "nope", Op: "or"},
			{ID: 22, Field: "content", Pattern: "bar", Op: "or"},
		},
	}
	matches, err = eng.MatchEntryStrict(entry, []storage.Filter{f2})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected OR match, got %d", len(matches))
	}

	f3 := storage.Filter{
		ID:        3,
		UpdatedAt: now,
		Rules: []storage.FilterRule{
			{ID: 31, Field: "title", Pattern: "foo", Negate: true, Op: "and"},
		},
	}
	matches, err = eng.MatchEntryStrict(entry, []storage.Filter{f3})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected negate to fail match, got %d", len(matches))
	}
}

func TestEngine_FieldAuthor(t *testing.T) {
	eng := New(Config{})
	now := time.Now().UTC()
	author := "Alice"
	f := storage.Filter{
		ID:        1,
		UpdatedAt: now,
		Rules: []storage.FilterRule{
			{ID: 1, Field: "author", Pattern: "^Ali", Op: "and"},
		},
	}
	entry := storage.Entry{Author: &author}
	matches, err := eng.MatchEntryStrict(entry, []storage.Filter{f})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected match, got %d", len(matches))
	}
}

func TestEngine_CompileRejectsInvalidRegex(t *testing.T) {
	eng := New(Config{})
	f := storage.Filter{
		ID:        1,
		UpdatedAt: time.Now().UTC(),
		Rules: []storage.FilterRule{
			{ID: 1, Field: "title", Pattern: "(", Op: "and"},
		},
	}
	_, err := eng.MatchEntryStrict(storage.Entry{Title: "x"}, []storage.Filter{f})
	if err == nil {
		t.Fatalf("expected compile error")
	}
}

func TestEngine_RejectsEmptyRegex(t *testing.T) {
	eng := New(Config{})
	f := storage.Filter{
		ID:        1,
		UpdatedAt: time.Now().UTC(),
		Rules: []storage.FilterRule{
			{ID: 1, Field: "title", Pattern: "   ", Op: "and"},
		},
	}
	_, err := eng.MatchEntryStrict(storage.Entry{Title: "x"}, []storage.Filter{f})
	if err == nil {
		t.Fatalf("expected error for empty regex")
	}
}

func TestEngine_MatchAnyRule(t *testing.T) {
	eng := New(Config{})
	now := time.Now().UTC()
	f := storage.Filter{
		ID:           1,
		UpdatedAt:    now,
		MatchAnyRule: true,
		Rules: []storage.FilterRule{
			{ID: 1, Field: "title", Pattern: "nope", Op: "and"},
			{ID: 2, Field: "content", Pattern: "bar", Op: "and"},
		},
	}
	entry := storage.Entry{Title: "foo", Content: "bar"}
	matches, err := eng.MatchEntryStrict(entry, []storage.Filter{f})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected OR match, got %d", len(matches))
	}
}

func TestEngine_Inverse(t *testing.T) {
	eng := New(Config{})
	now := time.Now().UTC()
	f := storage.Filter{
		ID:        1,
		UpdatedAt: now,
		Inverse:   true,
		Rules: []storage.FilterRule{
			{ID: 1, Field: "title", Pattern: "news", Op: "and"},
		},
	}
	matches, err := eng.MatchEntryStrict(storage.Entry{Title: "news today"}, []storage.Filter{f})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected inverse to block match")
	}
	matches, err = eng.MatchEntryStrict(storage.Entry{Title: "other"}, []storage.Filter{f})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected inverse match on non-matching rules")
	}
}

func TestEngine_BothField(t *testing.T) {
	eng := New(Config{})
	now := time.Now().UTC()
	f := storage.Filter{
		ID:        1,
		UpdatedAt: now,
		Rules: []storage.FilterRule{
			{ID: 1, Field: "both", Pattern: "alert", Op: "and"},
		},
	}
	matches, err := eng.MatchEntryStrict(storage.Entry{Title: "alert", Content: ""}, []storage.Filter{f})
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected title match in both field")
	}
	matches, err = eng.MatchEntryStrict(storage.Entry{Title: "", Content: "system alert"}, []storage.Filter{f})
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected content match in both field")
	}
}

func TestEngine_FeedScopeInclude(t *testing.T) {
	eng := New(Config{})
	now := time.Now().UTC()
	feedID := int64(10)
	f := storage.Filter{
		ID:        1,
		UpdatedAt: now,
		FeedScope: storage.FilterFeedScopeInclude,
		ScopeItems: []storage.FilterScopeItem{
			{FeedID: &feedID},
		},
		Rules: []storage.FilterRule{
			{ID: 1, Field: "title", Pattern: "x", Op: "and"},
		},
	}
	entry := storage.Entry{FeedID: feedID, Title: "x"}
	matches, err := eng.MatchEntryWithContextStrict(entry, MatchContext{FeedID: feedID}, []storage.Filter{f})
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected include scope match")
	}
	entry.FeedID = 99
	matches, err = eng.MatchEntryWithContextStrict(entry, MatchContext{FeedID: 99}, []storage.Filter{f})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected include scope to skip other feeds")
	}
}

func TestEngine_FeedScopeExclude(t *testing.T) {
	eng := New(Config{})
	now := time.Now().UTC()
	feedID := int64(5)
	catID := int64(2)
	f := storage.Filter{
		ID:        1,
		UpdatedAt: now,
		FeedScope: storage.FilterFeedScopeExclude,
		ScopeItems: []storage.FilterScopeItem{
			{CategoryID: &catID},
		},
		Rules: []storage.FilterRule{
			{ID: 1, Field: "title", Pattern: ".*", Op: "and"},
		},
	}
	matches, err := eng.MatchEntryWithContextStrict(
		storage.Entry{FeedID: feedID, Title: "hi"},
		MatchContext{FeedID: feedID, CategoryID: &catID},
		[]storage.Filter{f},
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected exclude category to skip")
	}
	otherCat := int64(3)
	matches, err = eng.MatchEntryWithContextStrict(
		storage.Entry{FeedID: feedID, Title: "hi"},
		MatchContext{FeedID: feedID, CategoryID: &otherCat},
		[]storage.Filter{f},
	)
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected match outside excluded category")
	}
}

func TestEngine_ValidateRules(t *testing.T) {
	eng := New(Config{MaxRulesPerFilter: 2, MaxRegexLength: 5})
	cases := []struct {
		name    string
		rules   []storage.CreateFilterRuleParams
		wantErr string
	}{
		{name: "ok", rules: []storage.CreateFilterRuleParams{{Field: "title", Pattern: "foo"}}},
		{name: "no rules", rules: nil},
		{name: "invalid regex", rules: []storage.CreateFilterRuleParams{{Field: "title", Pattern: "("}}, wantErr: "compile regex"},
		{name: "empty", rules: []storage.CreateFilterRuleParams{{Field: "title", Pattern: "  "}}, wantErr: "empty regex"},
		{name: "too long", rules: []storage.CreateFilterRuleParams{{Field: "title", Pattern: "abcdef"}}, wantErr: "regex too long"},
		{name: "bad field", rules: []storage.CreateFilterRuleParams{{Field: "body", Pattern: "x"}}, wantErr: "invalid field"},
		{name: "tags refused on write", rules: []storage.CreateFilterRuleParams{{Field: "Tags", Pattern: "x"}}, wantErr: "entries have no tags"},
		{name: "or on first rule", rules: []storage.CreateFilterRuleParams{{Field: "title", Pattern: "a", Op: "OR"}, {Field: "title", Pattern: "b"}}, wantErr: "first rule cannot have op"},
		{name: "or on second rule", rules: []storage.CreateFilterRuleParams{{Field: "title", Pattern: "a", Op: "and"}, {Field: "title", Pattern: "b", Op: "or"}}},
		{name: "too many", rules: []storage.CreateFilterRuleParams{
			{Field: "title", Pattern: "a"}, {Field: "title", Pattern: "b"}, {Field: "title", Pattern: "c"},
		}, wantErr: "too many rules"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := eng.ValidateRules(tc.rules)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// Rows written before 0.1.15 with field "tags" keep compiling (and never
// match), so an old filter does not start failing strict evaluation.
func TestEngine_LegacyTagsRuleStillCompiles(t *testing.T) {
	eng := New(Config{})
	f := storage.Filter{ID: 1, Rules: []storage.FilterRule{{ID: 1, Field: "tags", Pattern: "news"}}}
	got, err := eng.MatchEntryStrict(storage.Entry{Title: "news", Content: "news"}, []storage.Filter{f})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v err=%v", got, err)
	}
}

func TestEngine_QueryField(t *testing.T) {
	eng := New(Config{MaxRulesPerFilter: 50, MaxRegexLength: 2048})
	now := time.Now().UTC()
	f := storage.Filter{
		ID:        7,
		UpdatedAt: now,
		Rules: []storage.FilterRule{
			{ID: 1, Field: "query", Pattern: `"курс рубля" -прогноз`, Op: "and"},
			{ID: 2, Field: "title", Pattern: "(?i)цб", Op: "and"},
		},
	}
	entry := storage.Entry{Title: "ЦБ прокомментировал", Content: "курс рубля"}

	if _, err := eng.MatchEntryStrict(entry, []storage.Filter{f}); !errors.Is(err, ErrQueryRulesUnsupported) {
		t.Fatalf("strict mode without QueryHits: want ErrQueryRulesUnsupported, got %v", err)
	}
	matches, err := eng.MatchEntry(entry, []storage.Filter{f})
	if err != nil || len(matches) != 0 {
		t.Fatalf("lenient mode without QueryHits must not match: %v %v", matches, err)
	}

	hit := MatchContext{QueryHits: map[string]bool{`"курс рубля" -прогноз`: true}}
	matches, err = eng.MatchEntryWithContextStrict(entry, hit, []storage.Filter{f})
	if err != nil || len(matches) != 1 {
		t.Fatalf("query hit + regex hit: want 1 match, got %v %v", matches, err)
	}
	if !strings.Contains(string(matches[0].Details), `"field":"query"`) {
		t.Fatalf("details must record the query rule: %s", matches[0].Details)
	}
	miss := MatchContext{QueryHits: map[string]bool{`"курс рубля" -прогноз`: false}}
	matches, err = eng.MatchEntryWithContextStrict(entry, miss, []storage.Filter{f})
	if err != nil || len(matches) != 0 {
		t.Fatalf("query miss must fail AND: %v %v", matches, err)
	}

	neg := storage.Filter{ID: 8, UpdatedAt: now, Rules: []storage.FilterRule{{ID: 3, Field: "query", Pattern: "спорт", Negate: true}}}
	matches, err = eng.MatchEntryWithContextStrict(entry, MatchContext{QueryHits: map[string]bool{"спорт": false}}, []storage.Filter{neg})
	if err != nil || len(matches) != 1 {
		t.Fatalf("negated query miss must match: %v %v", matches, err)
	}

	if err := eng.ValidateRules([]storage.CreateFilterRuleParams{{Field: "query", Pattern: "(unbalanced"}}); err != nil {
		t.Fatalf("query patterns are not regexes and must not be compiled: %v", err)
	}
}

func TestQueryPatternsAndHits(t *testing.T) {
	filters := []storage.Filter{
		{ID: 1, Rules: []storage.FilterRule{{Field: "title", Pattern: "x"}, {Field: "query", Pattern: " нефть "}}},
		{ID: 2, Rules: []storage.FilterRule{{Field: "Query", Pattern: "нефть"}, {Field: "query", Pattern: "газ or уголь"}, {Field: "query", Pattern: "  "}}},
	}
	got := QueryPatterns(filters)
	if len(got) != 2 || got[0] != "нефть" || got[1] != "газ or уголь" {
		t.Fatalf("QueryPatterns = %q", got)
	}
	if QueryPatterns([]storage.Filter{{Rules: []storage.FilterRule{{Field: "title", Pattern: "x"}}}}) != nil {
		t.Fatal("no query rules must yield nil")
	}

	items := []storage.QueryMatchItem{{Title: "a"}, {Title: "b"}}
	hits, err := QueryHits(context.Background(), nil, filters[:0], items)
	if err != nil || hits != nil {
		t.Fatalf("no query rules: want nil,nil got %v %v", hits, err)
	}
	if _, err := QueryHits(context.Background(), nil, filters, items); !errors.Is(err, ErrQueryRulesUnsupported) {
		t.Fatalf("nil matcher with query rules: got %v", err)
	}
	m := &stubQueryMatcher{rows: [][]bool{{true, false}, {false, true}}}
	hits, err = QueryHits(context.Background(), m, filters, items)
	if err != nil || len(hits) != 2 {
		t.Fatalf("QueryHits: %v %v", hits, err)
	}
	if !hits[0]["нефть"] || hits[0]["газ or уголь"] || hits[1]["нефть"] || !hits[1]["газ or уголь"] {
		t.Fatalf("hits mapping wrong: %v", hits)
	}
	if m.gotQueries[0] != "нефть" || len(m.gotItems) != 2 {
		t.Fatalf("matcher call: %v %v", m.gotQueries, m.gotItems)
	}
}

type stubQueryMatcher struct {
	rows       [][]bool
	gotItems   []storage.QueryMatchItem
	gotQueries []string
}

func (m *stubQueryMatcher) MatchEntryQueries(_ context.Context, items []storage.QueryMatchItem, queries []string) ([][]bool, error) {
	m.gotItems, m.gotQueries = items, queries
	return m.rows, nil
}
