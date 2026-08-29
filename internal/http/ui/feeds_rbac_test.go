package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

func newFeedsRBACHandler(t *testing.T, admin bool) http.Handler {
	t.Helper()
	catID := int64(10)
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: admin,
		}},
		Sessions: &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:  uiMemEntries{},
		Feeds: &uiMemFeeds{feeds: []storage.Feed{
			{ID: 1, Title: "News Feed", CategoryID: &catID},
		}},
		Categories: &uiMemCategories{cats: []storage.Category{{ID: catID, Title: "News"}}},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestFeedsRBAC_NonAdminCannotMutateSubscriptions(t *testing.T) {
	mux := newFeedsRBACHandler(t, false)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	req := httptest.NewRequest(http.MethodGet, "/ui/feeds", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status=%d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "Добавить") || strings.Contains(body, "Экспорт OPML") || strings.Contains(body, "Импорт OPML") {
		t.Fatal("user feeds list must not show subscription admin actions")
	}
	if strings.Contains(body, `href="/ui/categories"`) {
		t.Fatal("user settings nav must not include categories")
	}
	if strings.Contains(body, "/ui/feeds/1/edit") {
		t.Fatal("user must not get edit links")
	}
	if !strings.Contains(body, `href="/ui/feeds/1"`) {
		t.Fatal("user should open feed show page")
	}

	form := url.Values{"csrf_token": {token}, "feed_url": {"https://example.com/x.xml"}}
	post := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := post("/ui/feeds"); rec.Code != http.StatusForbidden {
		t.Fatalf("create feed: status=%d", rec.Code)
	}
	if rec := post("/ui/feeds/1"); rec.Code != http.StatusForbidden {
		t.Fatalf("update feed: status=%d", rec.Code)
	}
	if rec := post("/ui/feeds/1/delete"); rec.Code != http.StatusForbidden {
		t.Fatalf("delete feed: status=%d", rec.Code)
	}
	if rec := post("/ui/categories"); rec.Code != http.StatusForbidden {
		t.Fatalf("create category: status=%d", rec.Code)
	}
	if rec := post("/ui/feeds/import"); rec.Code != http.StatusForbidden {
		t.Fatalf("import: status=%d", rec.Code)
	}

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := get("/ui/feeds/new"); rec.Code != http.StatusForbidden {
		t.Fatalf("new feed: status=%d", rec.Code)
	}
	if rec := get("/ui/feeds/1/edit"); rec.Code != http.StatusForbidden {
		t.Fatalf("edit feed: status=%d", rec.Code)
	}
	if rec := get("/ui/feeds/export"); rec.Code != http.StatusForbidden {
		t.Fatalf("export: status=%d", rec.Code)
	}
	if rec := get("/ui/categories"); rec.Code != http.StatusForbidden {
		t.Fatalf("categories: status=%d", rec.Code)
	}
	if rec := get("/ui/feeds/1"); rec.Code != http.StatusOK {
		t.Fatalf("show feed: status=%d", rec.Code)
	}
	show := get("/ui/feeds/1").Body.String()
	if strings.Contains(show, "Редактировать") {
		t.Fatal("user feed show must not include edit")
	}
	if !strings.Contains(show, "Обновить сейчас") {
		t.Fatal("user may refresh a single feed")
	}

	refreshForm := url.Values{"csrf_token": {token}}
	req = httptest.NewRequest(http.MethodPost, "/ui/feeds/1/refresh", strings.NewReader(refreshForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("user refresh: status=%d", rec.Code)
	}
}

func TestFeedsRBAC_AdminKeepsSubscriptionControls(t *testing.T) {
	mux := newFeedsRBACHandler(t, true)
	sid := uiSessionCookie(t, nil, mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/feeds", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Добавить") || !strings.Contains(body, "Экспорт OPML") || !strings.Contains(body, "Импорт OPML") {
		t.Fatal("admin feeds list should show subscription controls")
	}
	if !strings.Contains(body, `href="/ui/categories"`) {
		t.Fatal("admin settings nav should include categories")
	}
}
