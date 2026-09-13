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

type uiMemPollHours struct {
	set map[int64]string
}

func (m *uiMemPollHours) GetCategoryPollHours(_ context.Context, id int64) (string, error) {
	return m.set[id], nil
}

func (m *uiMemPollHours) SetCategoryPollHours(_ context.Context, _ int64, id int64, v string) error {
	if m.set == nil {
		m.set = map[int64]string{}
	}
	m.set[id] = v
	return nil
}

func TestUI_CategoryPollHoursForm(t *testing.T) {
	cats := &uiMemCategories{cats: []storage.Category{{ID: 1, Title: "Night", PollHours: "22:00-06:00"}}}
	ph := &uiMemPollHours{}
	h, err := NewHandler(Config{
		Users:             &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true}},
		Sessions:          &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:           uiMemEntries{},
		Categories:        cats,
		CategoryPollHours: ph,
		CSRFSecret:        "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	req := httptest.NewRequest(http.MethodGet, "/ui/categories", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="poll_hours" value="22:00-06:00"`) {
		t.Fatalf("list: status=%d body has no poll_hours field: %s", rec.Code, rec.Body.String())
	}

	post := func(path string, form url.Values) int {
		form.Set("csrf_token", token)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post("/ui/categories/1", url.Values{"title": {"Night"}, "poll_hours": {" 8:00 - 22:00 "}}); code != http.StatusFound {
		t.Fatalf("update: status=%d", code)
	}
	if ph.set[1] != "08:00-22:00" {
		t.Fatalf("stored %q, want normalized 08:00-22:00", ph.set[1])
	}
	if code := post("/ui/categories/1", url.Values{"title": {"Night"}, "poll_hours": {"25:00-01:00"}}); code != http.StatusBadRequest {
		t.Fatalf("invalid window: status=%d", code)
	}
	if code := post("/ui/categories/1", url.Values{"title": {"Night"}, "poll_hours": {""}}); code != http.StatusFound || ph.set[1] != "" {
		t.Fatalf("clear: status=%d stored=%q", code, ph.set[1])
	}
	// A form without the field (older clients, bulk pages) leaves the window alone.
	ph.set[1] = "08:00-22:00"
	if code := post("/ui/categories/1", url.Values{"title": {"Night"}}); code != http.StatusFound || ph.set[1] != "08:00-22:00" {
		t.Fatalf("no field: status=%d stored=%q", code, ph.set[1])
	}
}
