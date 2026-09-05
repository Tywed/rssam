package ui

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type entryReadStore struct {
	entry storage.Entry
}

func (s *entryReadStore) CreateEntries(context.Context, int64, []storage.CreateEntryParams) (int, []storage.Entry, error) {
	return 0, nil, nil
}
func (s *entryReadStore) GetEntry(_ context.Context, _ int64, id int64) (storage.Entry, error) {
	if s.entry.ID == id {
		return s.entry, nil
	}
	return storage.Entry{}, storage.ErrNotFound
}
func (s *entryReadStore) GetFeedEntry(context.Context, int64, int64, int64) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (s *entryReadStore) GetEntryByID(context.Context, int64) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (s *entryReadStore) UpdateEntryContent(context.Context, int64, storage.UpdateEntryContentParams) (storage.Entry, error) {
	return storage.Entry{}, nil
}
func (s *entryReadStore) UpdateEntry(context.Context, int64, int64, int64, storage.UpdateEntryParams) (storage.Entry, error) {
	return storage.Entry{}, nil
}
func (s *entryReadStore) ListEntries(context.Context, int64, storage.ListEntriesFilter) ([]storage.Entry, int, error) {
	if s.entry.ID == 0 {
		return nil, 0, nil
	}
	return []storage.Entry{s.entry}, 1, nil
}
func (s *entryReadStore) ListFeedEntries(context.Context, int64, int64, storage.ListEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, nil
}
func (s *entryReadStore) SearchEntries(context.Context, int64, storage.SearchEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, nil
}
func (s *entryReadStore) ListEnclosuresByEntryIDs(context.Context, int64, []int64) (map[int64][]storage.Enclosure, error) {
	return nil, nil
}
func (s *entryReadStore) CountUnreadByFeed(context.Context, int64) (int, error) { return 0, nil }
func (s *entryReadStore) CountUnreadByCategory(context.Context, int64) (int, error) {
	return 0, nil
}
func (s *entryReadStore) CountUnreadGlobal(context.Context) (int, error)               { return 0, nil }
func (s *entryReadStore) CountUnreadGlobalForUser(context.Context, int64) (int, error) { return 0, nil }
func (s *entryReadStore) UnreadCountsForUser(context.Context, int64) (map[int64]int, map[int64]int, error) {
	return map[int64]int{}, map[int64]int{}, nil
}
func (s *entryReadStore) BulkUpdateEntries(_ context.Context, _ int64, entryIDs []int64, update storage.BulkEntryUpdate) (int, error) {
	for _, id := range entryIDs {
		if s.entry.ID == id && update.Status != nil {
			s.entry.Status = *update.Status
		}
	}
	return len(entryIDs), nil
}
func (s *entryReadStore) MarkAllFeedEntriesRead(context.Context, int64, int64) (int, error) {
	return 0, nil
}
func (s *entryReadStore) MarkAllCategoryEntriesRead(context.Context, int64, int64) (int, error) {
	return 0, nil
}
func (s *entryReadStore) MarkAllEntriesRead(context.Context, int64) (int, error) { return 0, nil }

func loginTestSession(t *testing.T, mux *http.ServeMux) string {
	t.Helper()
	token := auth.CSRFToken("csrf-test", "login")
	form := url.Values{"username": {"alice"}, "password": {"secret"}, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var sid string
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			sid = c.Value
		}
	}
	if sid == "" {
		t.Fatal("expected session cookie")
	}
	return sid
}

func newEntryReadMux(t *testing.T, store storage.EntryStore) *http.ServeMux {
	t.Helper()
	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID:           1,
			Username:     "alice",
			PasswordHash: hash,
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    store,
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestUI_EntryReadMultipartCSRF(t *testing.T) {
	store := &entryReadStore{entry: storage.Entry{ID: 42, FeedID: 1, Status: storage.EntryStatusUnread, Title: "Test"}}
	mux := newEntryReadMux(t, store)
	sid := loginTestSession(t, mux)
	token := auth.CSRFToken("csrf-test", sid)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("csrf_token", token); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/ui/entries/42/read", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rec.Code, rec.Body.String())
	}
	if store.entry.Status != storage.EntryStatusRead {
		t.Fatalf("expected entry marked read, got %q", store.entry.Status)
	}
	if !strings.Contains(rec.Body.String(), "hl-row read") {
		t.Fatalf("expected read row partial, got %q", rec.Body.String())
	}
}

type searchFailStore struct {
	entryReadStore
}

func (s *searchFailStore) SearchEntries(context.Context, int64, storage.SearchEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, errors.New("boom")
}

func TestUI_SearchFailureKeepsLayout(t *testing.T) {
	store := &searchFailStore{entryReadStore: entryReadStore{}}
	mux := newEntryReadMux(t, store)
	sid := loginTestSession(t, mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/search?q=test", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.TrimSpace(body) == "search failed" {
		t.Fatal("raw search failed page")
	}
	if !strings.Contains(body, "Поиск не выполнен") {
		t.Fatalf("expected flash error, got %q", body)
	}
	if !strings.Contains(body, "rssam-app") {
		t.Fatal("expected full UI layout")
	}
}

func TestUI_EntryReadURLEncodedCSRF(t *testing.T) {
	store := &entryReadStore{entry: storage.Entry{ID: 7, FeedID: 1, Status: storage.EntryStatusUnread, Title: "Test"}}
	mux := newEntryReadMux(t, store)
	sid := loginTestSession(t, mux)
	token := auth.CSRFToken("csrf-test", sid)

	form := url.Values{"csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/ui/entries/7/read", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rec.Code, rec.Body.String())
	}
	if store.entry.Status != storage.EntryStatusRead {
		t.Fatalf("expected entry marked read, got %q", store.entry.Status)
	}
}
