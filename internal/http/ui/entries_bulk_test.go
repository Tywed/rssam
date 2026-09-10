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

type bulkEntries struct {
	uiMemEntries
	ids    []int64
	update storage.BulkEntryUpdate
	calls  int
}

func (b *bulkEntries) BulkUpdateEntries(_ context.Context, _ int64, ids []int64, u storage.BulkEntryUpdate) (int, error) {
	b.calls++
	b.ids = ids
	b.update = u
	return len(ids), nil
}

func TestUI_EntriesBulk(t *testing.T) {
	entries := &bulkEntries{}
	h, err := NewHandler(Config{
		Users:      &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret")}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    entries,
		Feeds:      &uiMemFeeds{},
		Categories: &uiMemCategories{},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)
	csrf := auth.CSRFToken("csrf-test", sid)

	post := func(form url.Values, referer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/ui/entries/bulk", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := post(url.Values{"csrf_token": {csrf}, "action": {"read"}, "entry_ids": {"3", "1", "2"}}, "http://example.com/ui/unread?feed_id=1&offset=50")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/unread?feed_id=1&offset=50" {
		t.Fatalf("read: status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	if entries.calls != 1 || len(entries.ids) != 3 || entries.update.Status == nil || *entries.update.Status != storage.EntryStatusRead || entries.update.Starred != nil {
		t.Fatalf("read: ids=%v update=%+v", entries.ids, entries.update)
	}

	rec = post(url.Values{"csrf_token": {csrf}, "action": {"unstar"}, "entry_ids": {"7"}}, "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/unread" {
		t.Fatalf("unstar: status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	if entries.update.Starred == nil || *entries.update.Starred || entries.update.Status != nil {
		t.Fatalf("unstar: update=%+v", entries.update)
	}

	for name, form := range map[string]url.Values{
		"no csrf":    {"action": {"read"}, "entry_ids": {"1"}},
		"no ids":     {"csrf_token": {csrf}, "action": {"read"}},
		"bad id":     {"csrf_token": {csrf}, "action": {"read"}, "entry_ids": {"x"}},
		"bad action": {"csrf_token": {csrf}, "action": {"remove"}, "entry_ids": {"1"}},
	} {
		calls := entries.calls
		if rec := post(form, ""); rec.Code < 400 {
			t.Errorf("%s: status=%d", name, rec.Code)
		}
		if entries.calls != calls {
			t.Errorf("%s: store was called", name)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/ui/unread", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `id="bulk-form" class="bulk-bar hidden"`) {
		t.Fatal("bulk bar must be rendered hidden until a row is checked")
	}
}
