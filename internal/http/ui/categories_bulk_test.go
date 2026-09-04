package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

func newCategoryBulkTestHandler(t *testing.T, admin bool) (http.Handler, *uiMemFeeds) {
	t.Helper()
	catID := int64(10)
	feedStore := &uiMemFeeds{feeds: []storage.Feed{
		{ID: 1, Title: "A", CategoryID: &catID, IntervalMinutes: 60},
		{ID: 2, Title: "B", CategoryID: &catID},
		{ID: 3, Title: "Other"},
	}}
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: admin,
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		Feeds:      feedStore,
		Categories: &uiMemCategories{cats: []storage.Category{{ID: catID, Title: "News"}}},
		CSRFSecret: "csrf-test",
		Dedup:      &uiMemDedup{n: 7},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, feedStore
}

func TestCategoryBulkActionsRequireAdmin(t *testing.T) {
	mux, _ := newCategoryBulkTestHandler(t, false)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	form := url.Values{"csrf_token": {token}, "interval_minutes": {"30"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/categories/10/feeds/bulk-interval", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin bulk interval: status=%d", rec.Code)
	}
}

func TestCategoryBulkInterval(t *testing.T) {
	mux, feedStore := newCategoryBulkTestHandler(t, true)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	form := url.Values{"csrf_token": {token}, "interval_minutes": {"15"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/categories/10/feeds/bulk-interval", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk interval: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Интервал опроса обновлён для 2 лент") {
		t.Fatalf("unexpected flash: %s", rec.Body.String())
	}
	if feedStore.feeds[0].IntervalMinutes != 15 || feedStore.feeds[1].IntervalMinutes != 15 {
		t.Fatalf("expected interval 15 on category feeds: %+v", feedStore.feeds[:2])
	}
	if feedStore.feeds[2].IntervalMinutes != 0 {
		t.Fatal("uncategorized feed should be unchanged")
	}
}

func TestCategoryBulkPause(t *testing.T) {
	mux, feedStore := newCategoryBulkTestHandler(t, true)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	form := url.Values{"csrf_token": {token}, "action": {"pause"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/categories/10/feeds/bulk-pause", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk pause: status=%d", rec.Code)
	}
	if !feedStore.feeds[0].ManualPaused || !feedStore.feeds[1].ManualPaused {
		t.Fatal("expected manual pause on category feeds")
	}
}

func TestCategoryBulkHashOnly(t *testing.T) {
	mux, feedStore := newCategoryBulkTestHandler(t, true)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	form := url.Values{"csrf_token": {token}, "action": {"enable"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/categories/10/feeds/bulk-hash-only", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk hash-only: status=%d", rec.Code)
	}
	if !feedStore.feeds[0].StoreHashOnly || !feedStore.feeds[1].StoreHashOnly {
		t.Fatal("expected store_hash_only on category feeds")
	}
}

func TestFeedsListShowsCategoryActionsForAdmin(t *testing.T) {
	mux, _ := newCategoryBulkTestHandler(t, true)
	sid := uiSessionCookie(t, nil, mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/feeds", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "Действия") || !strings.Contains(body, "/ui/categories/10/feeds/bulk-interval") {
		t.Fatal("admin feeds list should show category bulk actions")
	}
}

type uiMemDedup struct {
	n    int64
	last storage.CollapseEntriesParams
}

func (m *uiMemDedup) FilterKnownEntryHashes(context.Context, int64, []string) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}
func (m *uiMemDedup) RecordFeedEntryDedup(context.Context, int64, []storage.FeedEntryDedupParams) (int, error) {
	return 0, nil
}
func (m *uiMemDedup) StripEntryPayloadAfterWebhook(context.Context, int64) error { return nil }
func (m *uiMemDedup) CollapseEntriesToHashes(_ context.Context, p storage.CollapseEntriesParams) (int64, error) {
	m.last = p
	return m.n, nil
}

func TestCategoryBulkHashEntries(t *testing.T) {
	mux, _ := newCategoryBulkTestHandler(t, true)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	form := url.Values{"csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/ui/categories/10/feeds/bulk-hash-entries", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Свёрнуто в хеш: 7 записей") {
		t.Fatalf("flash: %s", rec.Body.String())
	}
}

func TestAdminHashAllEntries(t *testing.T) {
	mux, _ := newCategoryBulkTestHandler(t, true)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	form := url.Values{"csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/system/hash-entries", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d loc=%s body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/ui/admin/system?hashed=7" {
		t.Fatalf("location=%s", loc)
	}
}

func TestAdminHashAllEntriesIncludesLabeled(t *testing.T) {
	dedup := &uiMemDedup{n: 3}
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true,
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		Feeds:      &uiMemFeeds{},
		Categories: &uiMemCategories{},
		CSRFSecret: "csrf-test",
		Dedup:      dedup,
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)
	form := url.Values{"csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/system/hash-entries", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if !dedup.last.IncludeLabeled || !dedup.last.OnlyHashOnlyFeeds {
		t.Fatalf("params=%+v", dedup.last)
	}
}
