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

func newCategoryReorderTestHandler(t *testing.T, admin bool) (http.Handler, *uiMemCategories) {
	t.Helper()
	catStore := &uiMemCategories{cats: []storage.Category{
		{ID: 1, Title: "First", SortOrder: 0},
		{ID: 2, Title: "Second", SortOrder: 1},
		{ID: 3, Title: "Third", SortOrder: 2},
	}}
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: admin,
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		Categories: catStore,
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, catStore
}

func TestCategoryReorderRequiresAdmin(t *testing.T) {
	mux, catStore := newCategoryReorderTestHandler(t, false)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	form := url.Values{"csrf_token": {token}, "order": {"3", "1", "2"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/categories/reorder", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin reorder: status=%d", rec.Code)
	}
	if catStore.cats[0].ID != 1 {
		t.Fatal("order should be unchanged for non-admin")
	}
}

func TestCategoryReorder(t *testing.T) {
	mux, catStore := newCategoryReorderTestHandler(t, true)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	form := url.Values{"csrf_token": {token}, "order": {"3", "1", "2"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/categories/reorder", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("reorder: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(catStore.cats) != 3 {
		t.Fatalf("expected 3 categories, got %d", len(catStore.cats))
	}
	if catStore.cats[0].ID != 3 || catStore.cats[1].ID != 1 || catStore.cats[2].ID != 2 {
		t.Fatalf("unexpected order: %+v", catStore.cats)
	}
	if catStore.cats[0].SortOrder != 0 || catStore.cats[2].SortOrder != 2 {
		t.Fatalf("unexpected sort_order: %+v", catStore.cats)
	}
}

func TestCategoryReorderInvalidOrder(t *testing.T) {
	mux, catStore := newCategoryReorderTestHandler(t, true)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	form := url.Values{"csrf_token": {token}, "order": {"3", "1"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/categories/reorder", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("partial reorder: status=%d", rec.Code)
	}
	if catStore.cats[0].ID != 1 {
		t.Fatal("order should be unchanged on invalid request")
	}
}

func TestCategoriesListShowsDragHandleForAdmin(t *testing.T) {
	mux, _ := newCategoryReorderTestHandler(t, true)
	sid := uiSessionCookie(t, nil, mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/categories", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `id="categories-sortable"`) || !strings.Contains(body, "cat-drag-handle") {
		t.Fatal("admin categories list should include drag reorder UI")
	}
}
