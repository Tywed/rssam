package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type uiMemAdminFeeds struct {
	rows    []storage.AdminFeedRow
	summary storage.AdminFeedSummary
	jobs    storage.PollFeedJobCounts
}

func (m *uiMemAdminFeeds) ListAdminFeeds(_ context.Context, _ int) ([]storage.AdminFeedRow, error) {
	return m.rows, nil
}
func (m *uiMemAdminFeeds) ListAdminFeedsPage(_ context.Context, params storage.AdminFeedsListParams) ([]storage.AdminFeedRow, int, error) {
	now := time.Now()
	views := make([]adminFeedRowView, 0, len(m.rows))
	for _, row := range m.rows {
		views = append(views, adminFeedRowView{
			AdminFeedRow: row,
			Status:       classifyAdminFeedStatus(row, now),
		})
	}
	status := params.Status
	if status == "" {
		status = "all"
	}
	filtered := filterAdminFeedRows(views, status)
	sortKey := params.SortKey
	if sortKey == "" {
		sortKey = "name"
	}
	order := params.Order
	if order == "" {
		order = "asc"
	}
	sortAdminFeedRows(filtered, sortKey, order)

	total := len(filtered)
	limit := params.Limit
	if limit <= 0 {
		limit = adminFeedsPageSize
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return nil, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	out := make([]storage.AdminFeedRow, 0, end-offset)
	for _, v := range filtered[offset:end] {
		out = append(out, v.AdminFeedRow)
	}
	return out, total, nil
}
func (m *uiMemAdminFeeds) AdminFeedSummary(context.Context) (storage.AdminFeedSummary, error) {
	return m.summary, nil
}
func (m *uiMemAdminFeeds) PollFeedJobCounts(context.Context) (storage.PollFeedJobCounts, error) {
	return m.jobs, nil
}
func (m *uiMemAdminFeeds) OfferedPollsPerMin(context.Context) (float64, error) {
	return 2, nil
}
func (m *uiMemAdminFeeds) DueWebhookLogCount(context.Context) (int, error) {
	return 0, nil
}
func (m *uiMemAdminFeeds) GetPollFeedJob(_ context.Context, feedID int64) (*storage.AdminFeedJob, error) {
	return nil, nil
}
func (m *uiMemAdminFeeds) EstimateDatabaseSize(context.Context) (int64, error) {
	return 42 * 1024 * 1024, nil
}

func newAdminFeedsUIHandler(t *testing.T) http.Handler {
	t.Helper()
	now := time.Now()
	next := now.Add(time.Hour)
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true,
		}},
		Sessions: &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:  uiMemEntries{},
		Feeds: &uiMemFeeds{feeds: []storage.Feed{
			{ID: 1, Title: "News", FeedType: "rss", NextCheckAt: &next},
			{ID: 2, Title: "Broken", FeedType: "rss", LastError: "timeout", ParsingErrorCount: 3, PollPaused: true},
		}},
		Categories: &uiMemCategories{},
		AdminFeeds: &uiMemAdminFeeds{
			summary: storage.AdminFeedSummary{
				TotalFeeds: 2, OKCount: 1, ErrorCount: 1, PausedCount: 1, WaitingCount: 0,
				TotalEntries: 100, TotalUnread: 5,
			},
			jobs: storage.PollFeedJobCounts{Pending: 2, Running: 1},
			rows: []storage.AdminFeedRow{
				{Feed: storage.Feed{ID: 1, Title: "News", FeedType: "rss", NextCheckAt: &next}, EntryCount: 80, UnreadCount: 3},
				{Feed: storage.Feed{ID: 2, Title: "Broken", FeedType: "rss", LastError: "timeout", ParsingErrorCount: 3, PollPaused: true}, EntryCount: 20, UnreadCount: 2},
				{Feed: storage.Feed{ID: 3, Title: "Glitch", FeedType: "rss", LastError: "parse error", ParsingErrorCount: 1}, EntryCount: 5, UnreadCount: 0},
			},
		},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestUI_AdminFeedsForbidden(t *testing.T) {
	h := newTestUIHandler(t, false)
	mux := http.NewServeMux()
	h.Register(mux)

	token := auth.CSRFToken("csrf-test", "login")
	form := strings.NewReader("username=alice&password=secret&csrf_token=" + token)
	req := httptest.NewRequest(http.MethodPost, "/ui/login", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var sid string
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			sid = c.Value
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/ui/admin/feeds", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestUI_AdminFeedsPage(t *testing.T) {
	mux := newAdminFeedsUIHandler(t)
	sid := uiSessionCookie(t, nil, mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/admin/feeds", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin feeds: status=%d body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Состояние лент",
		"Мониторинг опроса и проблем",
		">1<",
		"Broken",
		"Glitch",
		"feed-status-error",
		"feed-status-paused",
		"/ui/admin/feeds?status=errors",
		"Страница 1 из 1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in admin feeds page", want)
		}
	}
}

func TestUI_AdminFeedsPagination(t *testing.T) {
	now := time.Now()
	next := now.Add(time.Hour)
	rows := make([]storage.AdminFeedRow, 0, 55)
	for i := 0; i < 55; i++ {
		id := int64(i + 1)
		rows = append(rows, storage.AdminFeedRow{
			Feed: storage.Feed{ID: id, Title: fmt.Sprintf("Feed %03d", id), FeedType: "rss", NextCheckAt: &next},
		})
	}
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true,
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		Feeds:      &uiMemFeeds{},
		Categories: &uiMemCategories{},
		AdminFeeds: &uiMemAdminFeeds{
			summary: storage.AdminFeedSummary{TotalFeeds: 55, OKCount: 55},
			rows:    rows,
		},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/admin/feeds", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "Feed 001") || !strings.Contains(body, "Feed 050") {
		t.Fatal("first admin page should show first 50 feeds")
	}
	if strings.Contains(body, "Feed 051") {
		t.Fatal("first admin page should not include feeds beyond page size")
	}
	if strings.Count(body, `href="/ui/admin/feeds/`) > 50 {
		t.Fatal("first admin page should render at most 50 feed rows")
	}
	if !strings.Contains(body, "Страница 1 из 2") {
		t.Fatal("expected page 1 of 2 indicator")
	}

	req = httptest.NewRequest(http.MethodGet, "/ui/admin/feeds?page=2", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body = rec.Body.String()
	if !strings.Contains(body, "Feed 051") || !strings.Contains(body, "Feed 055") {
		t.Fatal("second admin page should show remaining feeds")
	}
	if !strings.Contains(body, "Страница 2 из 2") {
		t.Fatal("expected page 2 of 2 indicator")
	}
}

func TestUI_AdminFeedsStatusFilterWithPagination(t *testing.T) {
	now := time.Now()
	next := now.Add(time.Hour)
	rows := make([]storage.AdminFeedRow, 0, 60)
	for i := 0; i < 55; i++ {
		id := int64(i + 1)
		rows = append(rows, storage.AdminFeedRow{
			Feed: storage.Feed{ID: id, Title: fmt.Sprintf("OK %d", id), FeedType: "rss", NextCheckAt: &next},
		})
	}
	for i := 0; i < 5; i++ {
		id := int64(100 + i)
		rows = append(rows, storage.AdminFeedRow{
			Feed: storage.Feed{ID: id, Title: fmt.Sprintf("Err %d", id), FeedType: "rss", LastError: "fail"},
		})
	}
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true,
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		Feeds:      &uiMemFeeds{},
		Categories: &uiMemCategories{},
		AdminFeeds: &uiMemAdminFeeds{
			summary: storage.AdminFeedSummary{TotalFeeds: 60, OKCount: 55, ErrorCount: 5},
			rows:    rows,
		},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/admin/feeds?status=errors", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, "OK 1") {
		t.Fatal("errors filter should not show ok feeds")
	}
	for i := 0; i < 5; i++ {
		if !strings.Contains(body, fmt.Sprintf("Err %d", 100+i)) {
			t.Fatalf("errors filter should show Err %d", 100+i)
		}
	}
	if !strings.Contains(body, "Страница 1 из 1") {
		t.Fatal("filtered result should fit on one page")
	}
}

func TestClassifyAdminFeedStatus(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)

	if got := classifyAdminFeedStatus(storage.AdminFeedRow{Feed: storage.Feed{PollPaused: true}}, now); got != "paused" {
		t.Fatalf("paused: got %q", got)
	}
	if got := classifyAdminFeedStatus(storage.AdminFeedRow{Feed: storage.Feed{LastError: "x"}}, now); got != "errors" {
		t.Fatalf("errors: got %q", got)
	}
	if got := classifyAdminFeedStatus(storage.AdminFeedRow{Feed: storage.Feed{NextCheckAt: &past}}, now); got != "waiting" {
		t.Fatalf("waiting: got %q", got)
	}
	if got := classifyAdminFeedStatus(storage.AdminFeedRow{Feed: storage.Feed{NextCheckAt: &future}}, now); got != "ok" {
		t.Fatalf("ok: got %q", got)
	}
}
