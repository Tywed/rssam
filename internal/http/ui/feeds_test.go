package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type uiMemFeeds struct {
	feeds []storage.Feed
}

func (m *uiMemFeeds) ListFeeds(_ context.Context, _ int64, limit, offset int) ([]storage.Feed, int, error) {
	if limit <= 0 {
		limit = len(m.feeds) - offset
	}
	if offset >= len(m.feeds) {
		return nil, len(m.feeds), nil
	}
	end := offset + limit
	if end > len(m.feeds) {
		end = len(m.feeds)
	}
	return m.feeds[offset:end], len(m.feeds), nil
}
func (m *uiMemFeeds) ListFeedsByCategory(_ context.Context, _ int64, categoryID int64) ([]storage.Feed, error) {
	out, err := m.listFeedsByCategory(categoryID)
	return out, err
}

func (m *uiMemFeeds) listFeedsByCategory(categoryID int64) ([]storage.Feed, error) {
	out := make([]storage.Feed, 0)
	for _, f := range m.feeds {
		if categoryID == 0 {
			if f.CategoryID == nil {
				out = append(out, f)
			}
			continue
		}
		if f.CategoryID != nil && *f.CategoryID == categoryID {
			out = append(out, f)
		}
	}
	return out, nil
}

func (m *uiMemFeeds) SearchFeeds(_ context.Context, _ int64, filter storage.SearchFeedsFilter) ([]storage.Feed, error) {
	q := strings.ToLower(strings.TrimSpace(filter.Query))
	hasQuery := len([]rune(q)) >= storage.MinFeedSearchQueryRunes
	hasCategory := filter.CategoryID != nil
	if !hasQuery && !hasCategory {
		return nil, nil
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = storage.DefaultFeedSuggestLimit
	}
	if limit > storage.MaxFeedSuggestLimit {
		limit = storage.MaxFeedSuggestLimit
	}
	out := make([]storage.Feed, 0)
	for _, f := range m.feeds {
		if hasCategory {
			want := *filter.CategoryID
			if want == 0 {
				if f.CategoryID != nil {
					continue
				}
			} else if f.CategoryID == nil || *f.CategoryID != want {
				continue
			}
		}
		if hasQuery {
			title := strings.ToLower(f.Title)
			url := strings.ToLower(f.FeedURL)
			if !strings.Contains(title, q) && !strings.Contains(url, q) {
				continue
			}
		}
		out = append(out, f)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *uiMemFeeds) ListFeedsByIDs(_ context.Context, _ int64, ids []int64) ([]storage.Feed, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	byID := make(map[int64]storage.Feed, len(m.feeds))
	for _, f := range m.feeds {
		byID[f.ID] = f
	}
	out := make([]storage.Feed, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if f, ok := byID[id]; ok {
			out = append(out, f)
		}
	}
	return out, nil
}

func (m *uiMemFeeds) ListFeedsByCategoryPaginated(_ context.Context, _ int64, categoryID int64, limit, offset int) ([]storage.Feed, int, error) {
	all, err := m.listFeedsByCategory(categoryID)
	if err != nil {
		return nil, 0, err
	}
	total := len(all)
	if limit <= 0 {
		limit = total - offset
	}
	if offset >= total {
		return nil, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total, nil
}
func (m *uiMemFeeds) FeedCountsByCategory(_ context.Context, _ int64) (storage.FeedCategoryCounts, error) {
	counts, uncategorized := categoryFeedCounts(m.feeds)
	return storage.FeedCategoryCounts{
		ByCategory:    counts,
		Uncategorized: uncategorized,
		Total:         len(m.feeds),
	}, nil
}
func (m *uiMemFeeds) CountFeedStatuses(_ context.Context, _ int64) (int, int, error) {
	errors, inactive := countFeedStatuses(m.feeds)
	return errors, inactive, nil
}
func (m *uiMemFeeds) ListAllFeeds(context.Context, int) ([]storage.Feed, error) { return m.feeds, nil }
func (m *uiMemFeeds) GetFeed(_ context.Context, _ int64, id int64) (storage.Feed, error) {
	for _, f := range m.feeds {
		if f.ID == id {
			return f, nil
		}
	}
	return storage.Feed{}, storage.ErrNotFound
}
func (m *uiMemFeeds) CreateFeed(context.Context, int64, storage.CreateFeedParams) (storage.Feed, error) {
	return storage.Feed{}, nil
}
func (m *uiMemFeeds) UpdateFeed(context.Context, int64, storage.UpdateFeedParams) (storage.Feed, error) {
	return storage.Feed{}, nil
}
func (m *uiMemFeeds) DeleteFeed(_ context.Context, _ int64, id int64) error {
	for i, f := range m.feeds {
		if f.ID == id {
			m.feeds = append(m.feeds[:i], m.feeds[i+1:]...)
			return nil
		}
	}
	return storage.ErrNotFound
}
func (m *uiMemFeeds) GetFeedByID(_ context.Context, id int64) (storage.Feed, error) {
	return m.GetFeed(context.Background(), 0, id)
}
func (m *uiMemFeeds) UpdateFeedRefreshMeta(context.Context, storage.UpdateFeedRefreshMetaParams) error {
	return nil
}
func (m *uiMemFeeds) UpdateFeedIcon(context.Context, int64, int64, string, []byte) error {
	return nil
}
func (m *uiMemFeeds) SetFeedNextCheckAt(context.Context, int64, time.Time) error { return nil }
func (m *uiMemFeeds) RecordFeedPollFailure(context.Context, int64, string, int, time.Time) error {
	return nil
}
func (m *uiMemFeeds) ResetFeedPollCircuit(_ context.Context, id int64) error {
	for i, f := range m.feeds {
		if f.ID == id {
			m.feeds[i].PollPaused = false
			m.feeds[i].ParsingErrorCount = 0
			return nil
		}
	}
	return storage.ErrNotFound
}
func (m *uiMemFeeds) SetFeedManualPaused(_ context.Context, id int64, paused bool) error {
	for i, f := range m.feeds {
		if f.ID == id {
			m.feeds[i].ManualPaused = paused
			return nil
		}
	}
	return storage.ErrNotFound
}

func (m *uiMemFeeds) BulkUpdateFeedsByCategory(_ context.Context, userID, categoryID int64, update storage.BulkFeedUpdate) ([]int64, int, error) {
	var ids []int64
	for i, f := range m.feeds {
		if f.UserID != 0 && f.UserID != userID {
			continue
		}
		match := categoryID == 0 && f.CategoryID == nil
		if categoryID > 0 && f.CategoryID != nil && *f.CategoryID == categoryID {
			match = true
		}
		if !match {
			continue
		}
		if update.IntervalMinutes != nil {
			m.feeds[i].IntervalMinutes = *update.IntervalMinutes
		}
		if update.WebhookSet {
			m.feeds[i].WebhookID = update.WebhookID
		}
		if update.StoreHashOnly != nil {
			m.feeds[i].StoreHashOnly = *update.StoreHashOnly
		}
		if update.ManualPaused != nil {
			m.feeds[i].ManualPaused = *update.ManualPaused
		}
		if update.MoveCategory {
			if update.MoveToCategoryID != nil {
				cid := *update.MoveToCategoryID
				m.feeds[i].CategoryID = &cid
			} else {
				m.feeds[i].CategoryID = nil
			}
		}
		ids = append(ids, f.ID)
	}
	return ids, len(ids), nil
}

type uiMemCategories struct {
	cats []storage.Category
}

func (m *uiMemCategories) ListCategories(_ context.Context, _ int64, limit, offset int) ([]storage.Category, int, error) {
	if limit <= 0 {
		limit = len(m.cats) - offset
	}
	if offset >= len(m.cats) {
		return nil, len(m.cats), nil
	}
	end := offset + limit
	if end > len(m.cats) {
		end = len(m.cats)
	}
	return m.cats[offset:end], len(m.cats), nil
}
func (m *uiMemCategories) CreateCategory(context.Context, int64, string, string) (storage.Category, error) {
	return storage.Category{}, nil
}
func (m *uiMemCategories) UpdateCategory(context.Context, int64, int64, string, string) (storage.Category, error) {
	return storage.Category{}, nil
}
func (m *uiMemCategories) DeleteCategory(context.Context, int64, int64) error { return nil }

func (m *uiMemCategories) ReorderCategories(_ context.Context, _ int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	byID := make(map[int64]storage.Category, len(m.cats))
	for _, c := range m.cats {
		byID[c.ID] = c
	}
	if len(byID) != len(ids) {
		return storage.ErrInvalidReference
	}
	seen := make(map[int64]struct{}, len(ids))
	ordered := make([]storage.Category, 0, len(ids))
	for i, id := range ids {
		if _, ok := byID[id]; !ok {
			return storage.ErrInvalidReference
		}
		if _, dup := seen[id]; dup {
			return storage.ErrInvalidReference
		}
		seen[id] = struct{}{}
		c := byID[id]
		c.SortOrder = i
		ordered = append(ordered, c)
	}
	m.cats = ordered
	return nil
}

func uiSessionCookie(t *testing.T, h *Handler, mux http.Handler) string {
	t.Helper()
	token := auth.CSRFToken("csrf-test", "login")
	form := url.Values{"username": {"alice"}, "password": {"secret"}, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c.Value
		}
	}
	t.Fatal("expected session cookie")
	return ""
}

func TestUI_FeedsListTreeAndFilters(t *testing.T) {
	catID := int64(10)
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"),
		}},
		Sessions: &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:  uiMemEntries{},
		Feeds: &uiMemFeeds{feeds: []storage.Feed{
			{ID: 1, Title: "Good Feed", CategoryID: &catID},
			{ID: 2, Title: "Bad Feed", CategoryID: &catID, LastError: "timeout"},
			{ID: 3, Title: "Paused Feed", PollPaused: true},
		}},
		Categories: &uiMemCategories{cats: []storage.Category{
			{ID: catID, Title: "News"},
		}},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/feeds", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("feeds list: status=%d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Каналы с ошибками", "Неактивные каналы", "News", "(2 канала)", "Bad Feed", "tree-toggle"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in feeds list body", want)
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/ui/feeds?filter=errors", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body = rec.Body.String()
	if !strings.Contains(body, `filter=errors" class="feeds-filter active`) {
		t.Fatal("errors filter should be active")
	}
	if !strings.Contains(body, "Bad Feed") || !strings.Contains(body, "Paused Feed") {
		t.Fatal("errors filter should show problematic feeds in management tree")
	}
	if !strings.Contains(body, "(2 канала)") {
		t.Fatal("errors filter should show filtered category count")
	}
}

func TestUI_FeedsListNoTruncation(t *testing.T) {
	catTelegram := int64(10)
	catVK := int64(20)
	feeds := make([]storage.Feed, 0, 1100)
	for i := 0; i < 1050; i++ {
		id := int64(i + 1)
		catID := catTelegram
		feeds = append(feeds, storage.Feed{ID: id, Title: fmt.Sprintf("TG %d", id), CategoryID: &catID})
	}
	for i := 0; i < 50; i++ {
		id := int64(1050 + i + 1)
		catID := catVK
		feeds = append(feeds, storage.Feed{ID: id, Title: fmt.Sprintf("VK %d", id), CategoryID: &catID})
	}

	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"),
		}},
		Sessions: &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:  uiMemEntries{},
		Feeds:    &uiMemFeeds{feeds: feeds},
		Categories: &uiMemCategories{cats: []storage.Category{
			{ID: catTelegram, Title: "Telegram"},
			{ID: catVK, Title: "VK"},
		}},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/feeds", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("feeds list: status=%d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"(1100 каналов)", "Telegram", "(1050 каналов)", "VK", "(50 каналов)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in feeds list body (truncation bug?)", want)
		}
	}
	if !strings.Contains(body, `class="feeds-mgmt-tree feeds-tree-lazy"`) {
		t.Fatal("expected lazy feeds management tree for 1100 feeds")
	}
	if strings.Contains(body, `class="tree-row tree-feed-row`) {
		t.Fatal("lazy feeds list should not inline feed rows in initial HTML")
	}
	if !strings.Contains(body, `data-lazy-url="/ui/feeds/categories/10/tree"`) {
		t.Fatal("expected lazy URL for category feeds")
	}

	req = httptest.NewRequest(http.MethodGet, "/ui/feeds/categories/10/tree", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("feeds tree partial: status=%d", rec.Code)
	}
	partial := rec.Body.String()
	if !strings.Contains(partial, "TG 1") || !strings.Contains(partial, "TG 50") {
		t.Fatal("feeds tree partial should list first page of category feeds")
	}
	if strings.Contains(partial, "TG 1050") {
		t.Fatal("first page should not include feeds beyond page size")
	}
	if !strings.Contains(partial, "tree-feeds-load-more") {
		t.Fatal("expected load-more button in feeds tree partial")
	}

	req = httptest.NewRequest(http.MethodGet, "/ui/feeds/categories/10/tree?offset=50", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("feeds tree partial page 2: status=%d", rec.Code)
	}
	partial = rec.Body.String()
	if !strings.Contains(partial, "TG 51") {
		t.Fatal("feeds tree page 2 should continue category feed list")
	}
}

func mustHash(t *testing.T, pass string) string {
	t.Helper()
	h, err := auth.HashPassword(pass)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestFeedParamsFromFormIntervalValidation(t *testing.T) {
	t.Run("accepts minimum interval", func(t *testing.T) {
		form := url.Values{
			"feed_url":         {"https://example.com/feed.xml"},
			"interval_minutes": {"1"},
		}
		req := httptest.NewRequest(http.MethodPost, "/ui/feeds", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		params, err := feedParamsFromForm(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if params.IntervalMinutes != 1 {
			t.Fatalf("interval=%d, want 1", params.IntervalMinutes)
		}
	})

	t.Run("rejects below minimum", func(t *testing.T) {
		form := url.Values{
			"feed_url":         {"https://example.com/feed.xml"},
			"interval_minutes": {"0"},
		}
		req := httptest.NewRequest(http.MethodPost, "/ui/feeds", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		_, err := feedParamsFromForm(req)
		if err == nil {
			t.Fatal("expected validation error")
		}
	})
}
