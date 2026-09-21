package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
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

func (m *uiMemAdminFeeds) GetAdminFeedRow(_ context.Context, feedID int64) (storage.AdminFeedRow, error) {
	for _, row := range m.rows {
		if row.ID == feedID {
			return row, nil
		}
	}
	return storage.AdminFeedRow{}, storage.ErrNotFound
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
	filtered := filterAdminFeedRows(views, status, params.SilentAfter)
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
	offset := max(params.Offset, 0)
	if offset >= total {
		return nil, total, nil
	}
	end := min(offset+limit, total)
	out := make([]storage.AdminFeedRow, 0, end-offset)
	for _, v := range filtered[offset:end] {
		out = append(out, v.AdminFeedRow)
	}
	return out, total, nil
}
func (m *uiMemAdminFeeds) AdminFeedSummary(context.Context, time.Duration) (storage.AdminFeedSummary, error) {
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
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), Role: auth.RoleAdmin,
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
				{ID: 1, Title: "News", FeedType: "rss", NextCheckAt: &next, EntryCount: 80, UnreadCount: 3},
				{ID: 2, Title: "Broken", FeedType: "rss", LastError: "timeout", ParsingErrorCount: 3, PollPaused: true, EntryCount: 20, UnreadCount: 2},
				{ID: 3, Title: "Glitch", FeedType: "rss", LastError: "parse error", ParsingErrorCount: 1, EntryCount: 5, UnreadCount: 0},
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
		`/ui/admin/feeds/2/delete`,
		`/ui/admin/feeds/3/delete`,
		`data-confirm=`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in admin feeds page", want)
		}
	}
	if strings.Contains(body, `/ui/admin/feeds/1/delete`) {
		t.Fatal("ok feeds should not show delete on admin feeds list")
	}
	if strings.Contains(body, `class="feeds-filters"`) {
		t.Fatal("admin feeds should not render duplicate text filters")
	}
	if !strings.Contains(body, `class="admin-feeds-card active"`) {
		t.Fatal("expected clickable summary card to mark the active filter")
	}
	if !strings.Contains(body, `name="sort"`) || !strings.Contains(body, `name="order"`) {
		t.Fatal("action forms should keep current sort in hidden fields")
	}
}

func TestUI_AdminFeedsPagination(t *testing.T) {
	now := time.Now()
	next := now.Add(time.Hour)
	rows := make([]storage.AdminFeedRow, 0, 55)
	for i := range 55 {
		id := int64(i + 1)
		rows = append(rows, storage.AdminFeedRow{
			ID: id, Title: fmt.Sprintf("Feed %03d", id), FeedType: "rss", NextCheckAt: &next,
		})
	}
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), Role: auth.RoleAdmin,
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
	for i := range 55 {
		id := int64(i + 1)
		rows = append(rows, storage.AdminFeedRow{
			ID: id, Title: fmt.Sprintf("OK %d", id), FeedType: "rss", NextCheckAt: &next,
		})
	}
	for i := range 5 {
		id := int64(100 + i)
		rows = append(rows, storage.AdminFeedRow{
			ID: id, Title: fmt.Sprintf("Err %d", id), FeedType: "rss", LastError: "fail",
		})
	}
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), Role: auth.RoleAdmin,
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
	for i := range 5 {
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

	if got := classifyAdminFeedStatus(storage.AdminFeedRow{PollPaused: true}, now); got != "paused" {
		t.Fatalf("paused: got %q", got)
	}
	if got := classifyAdminFeedStatus(storage.AdminFeedRow{LastError: "x"}, now); got != "errors" {
		t.Fatalf("errors: got %q", got)
	}
	if got := classifyAdminFeedStatus(storage.AdminFeedRow{NextCheckAt: &past}, now); got != "waiting" {
		t.Fatalf("waiting: got %q", got)
	}
	if got := classifyAdminFeedStatus(storage.AdminFeedRow{NextCheckAt: &future}, now); got != "ok" {
		t.Fatalf("ok: got %q", got)
	}
}

func TestIsSilentAdminFeed(t *testing.T) {
	now := time.Now()
	old := now.Add(-10 * 24 * time.Hour)
	recent := now.Add(-time.Hour)
	week := 7 * 24 * time.Hour
	cases := []struct {
		name string
		row  storage.AdminFeedRow
		want bool
	}{
		{"old entry", storage.AdminFeedRow{Feed: storage.Feed{LastEntryAt: &old, CreatedAt: old}}, true},
		{"never delivered, created long ago", storage.AdminFeedRow{Feed: storage.Feed{CreatedAt: old}}, true},
		{"never delivered, created recently", storage.AdminFeedRow{Feed: storage.Feed{CreatedAt: recent}}, false},
		{"recent entry", storage.AdminFeedRow{Feed: storage.Feed{LastEntryAt: &recent, CreatedAt: old}}, false},
		{"paused", storage.AdminFeedRow{Feed: storage.Feed{LastEntryAt: &old, CreatedAt: old, ManualPaused: true}}, false},
		{"erroring", storage.AdminFeedRow{Feed: storage.Feed{LastEntryAt: &old, CreatedAt: old, LastError: "x"}}, false},
	}
	for _, tc := range cases {
		if got := isSilentAdminFeed(tc.row, now, week); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
	if isSilentAdminFeed(cases[0].row, now, 0) {
		t.Error("window 0 must disable the check")
	}
}

func TestUI_AdminFeedsSilentCard(t *testing.T) {
	now := time.Now()
	next := now.Add(time.Hour)
	old := now.Add(-30 * 24 * time.Hour)
	newHandler := func(silentDays int) *http.ServeMux {
		h, err := NewHandler(Config{
			Users:      &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), Role: auth.RoleAdmin}},
			Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
			Entries:    uiMemEntries{},
			Feeds:      &uiMemFeeds{},
			Categories: &uiMemCategories{},
			AdminFeeds: &uiMemAdminFeeds{
				summary: storage.AdminFeedSummary{TotalFeeds: 2, OKCount: 2, SilentCount: 1},
				rows: []storage.AdminFeedRow{
					{Feed: storage.Feed{ID: 1, Title: "Quiet", FeedType: "rss", NextCheckAt: &next, LastEntryAt: &old, CreatedAt: old}},
					{Feed: storage.Feed{ID: 2, Title: "Lively", FeedType: "rss", NextCheckAt: &next, LastEntryAt: &now, CreatedAt: old}},
				},
			},
			CSRFSecret:     "csrf-test",
			FeedSilentDays: silentDays,
		})
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		h.Register(mux)
		return mux
	}
	get := func(mux *http.ServeMux, path string) string {
		sid := uiSessionCookie(t, nil, mux)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d", path, rec.Code)
		}
		return rec.Body.String()
	}

	mux := newHandler(7)
	body := get(mux, "/ui/admin/feeds")
	for _, want := range []string{"Молчат &gt; 7 дн.", "/ui/admin/feeds?status=silent", "Последняя запись", `sort=last_entry`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q", want)
		}
	}
	body = get(mux, "/ui/admin/feeds?status=silent")
	if !strings.Contains(body, "Quiet") || strings.Contains(body, "Lively") {
		t.Fatal("silent filter must list only the quiet feed")
	}

	body = get(newHandler(0), "/ui/admin/feeds")
	if strings.Contains(body, "status=silent") {
		t.Fatal("FEED_SILENT_DAYS=0 must hide the card")
	}
}

// In-memory mirror of the SQL filter/sort in storage.ListAdminFeedsPage, used
// only by uiMemAdminFeeds.
// isSilentAdminFeed mirrors adminFeedsSilentWhere for the in-memory path.
func isSilentAdminFeed(row storage.AdminFeedRow, now time.Time, silentAfter time.Duration) bool {
	if silentAfter <= 0 || row.ManualPaused || row.PollPaused || row.LastError != "" || row.ParsingErrorCount > 0 {
		return false
	}
	last := row.CreatedAt
	if row.LastEntryAt != nil {
		last = *row.LastEntryAt
	}
	return last.Before(now.Add(-silentAfter))
}

func filterAdminFeedRows(rows []adminFeedRowView, status string, silentAfter time.Duration) []adminFeedRowView {
	if status == "" || status == "all" {
		return rows
	}
	now := time.Now()
	out := make([]adminFeedRowView, 0, len(rows))
	for _, row := range rows {
		if status == "silent" {
			if isSilentAdminFeed(row.AdminFeedRow, now, silentAfter) {
				out = append(out, row)
			}
			continue
		}
		if row.Status == status {
			out = append(out, row)
		}
	}
	return out
}

func sortAdminFeedRows(rows []adminFeedRowView, sortKey, order string) {
	desc := order == "desc"
	sort.SliceStable(rows, func(i, j int) bool {
		less := compareAdminFeedRows(rows[i], rows[j], sortKey)
		if desc {
			return !less
		}
		return less
	})
}

func compareAdminFeedRows(a, b adminFeedRowView, sortKey string) bool {
	switch sortKey {
	case "id":
		return a.ID < b.ID
	case "status":
		if a.Status != b.Status {
			return a.Status < b.Status
		}
		return a.ID < b.ID
	case "last_checked":
		return timePtrBefore(a.LastCheckedAt, b.LastCheckedAt, a.ID, b.ID)
	case "next_check":
		return timePtrBefore(a.NextCheckAt, b.NextCheckAt, a.ID, b.ID)
	case "last_entry":
		return timePtrBefore(a.LastEntryAt, b.LastEntryAt, a.ID, b.ID)
	case "errors":
		if a.ParsingErrorCount != b.ParsingErrorCount {
			return a.ParsingErrorCount < b.ParsingErrorCount
		}
		return a.ID < b.ID
	case "entries":
		if a.EntryCount != b.EntryCount {
			return a.EntryCount < b.EntryCount
		}
		return a.ID < b.ID
	case "unread":
		if a.UnreadCount != b.UnreadCount {
			return a.UnreadCount < b.UnreadCount
		}
		return a.ID < b.ID
	default:
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.ID < b.ID
	}
}

func timePtrBefore(a, b *time.Time, idA, idB int64) bool {
	switch {
	case a == nil && b == nil:
		return idA < idB
	case a == nil:
		return true
	case b == nil:
		return false
	default:
		if !a.Equal(*b) {
			return a.Before(*b)
		}
		return idA < idB
	}
}
