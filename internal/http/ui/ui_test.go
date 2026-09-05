package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type uiMemSessions struct {
	sessions map[string]storage.Session
}

func (m *uiMemSessions) CreateSession(_ context.Context, userID int64, sessionID string, expiresAt time.Time) (storage.Session, error) {
	s := storage.Session{UserID: userID, SessionID: sessionID, ExpiresAt: expiresAt}
	m.sessions[sessionID] = s
	return s, nil
}
func (m *uiMemSessions) LookupSession(_ context.Context, sessionID string) (storage.Session, error) {
	s, ok := m.sessions[sessionID]
	if !ok {
		return storage.Session{}, storage.ErrNotFound
	}
	return s, nil
}
func (m *uiMemSessions) TouchSession(_ context.Context, sessionID string, expiresAt time.Time) error {
	s, ok := m.sessions[sessionID]
	if !ok {
		return storage.ErrNotFound
	}
	s.ExpiresAt = expiresAt
	m.sessions[sessionID] = s
	return nil
}
func (m *uiMemSessions) DeleteSession(_ context.Context, sessionID string) error {
	delete(m.sessions, sessionID)
	return nil
}
func (m *uiMemSessions) DeleteUserSessions(_ context.Context, _ int64) error { return nil }
func (m *uiMemSessions) DeleteUserSessionsExcept(_ context.Context, userID int64, keep string) error {
	for id, sess := range m.sessions {
		if sess.UserID == userID && id != keep {
			delete(m.sessions, id)
		}
	}
	return nil
}

type uiMemUsers struct {
	user storage.User
}

func (u *uiMemUsers) CountUsers(context.Context) (int, error)             { return 1, nil }
func (u *uiMemUsers) CountLoginCapableUsers(context.Context) (int, error) { return 1, nil }
func (u *uiMemUsers) ListUsers(context.Context, int, int) ([]storage.User, int, error) {
	return []storage.User{u.user}, 1, nil
}
func (u *uiMemUsers) GetUser(_ context.Context, id int64) (storage.User, error) {
	if id == u.user.ID {
		return u.user, nil
	}
	return storage.User{}, storage.ErrNotFound
}
func (u *uiMemUsers) GetUserByUsername(_ context.Context, name string) (storage.User, error) {
	if name == u.user.Username {
		return u.user, nil
	}
	return storage.User{}, storage.ErrNotFound
}
func (u *uiMemUsers) GetUserByFeverAPIKey(context.Context, string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}
func (u *uiMemUsers) CreateUser(context.Context, storage.CreateUserParams) (storage.User, error) {
	return storage.User{}, nil
}
func (u *uiMemUsers) UpdateUser(context.Context, storage.UpdateUserParams) (storage.User, error) {
	return u.user, nil
}
func (u *uiMemUsers) DeleteUser(context.Context, int64) error { return nil }
func (u *uiMemUsers) LookupAPIKey(context.Context, string) (storage.APIKey, error) {
	return storage.APIKey{}, storage.ErrNotFound
}
func (u *uiMemUsers) TouchAPIKeyUsed(context.Context, int64) error { return nil }
func (u *uiMemUsers) ListAPIKeys(context.Context, int64) ([]storage.APIKey, error) {
	return nil, nil
}
func (u *uiMemUsers) CreateAPIKey(context.Context, storage.CreateAPIKeyParams) (storage.APIKey, error) {
	return storage.APIKey{}, nil
}
func (u *uiMemUsers) DeleteAPIKey(context.Context, int64, int64) error { return nil }

type uiMemEntries struct{}

func (uiMemEntries) CreateEntries(context.Context, int64, []storage.CreateEntryParams) (int, []storage.Entry, error) {
	return 0, nil, nil
}
func (uiMemEntries) GetEntry(context.Context, int64, int64) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (uiMemEntries) GetFeedEntry(context.Context, int64, int64, int64) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (uiMemEntries) GetEntryByID(context.Context, int64) (storage.Entry, error) {
	return storage.Entry{}, storage.ErrNotFound
}
func (uiMemEntries) UpdateEntryContent(context.Context, int64, storage.UpdateEntryContentParams) (storage.Entry, error) {
	return storage.Entry{}, nil
}
func (uiMemEntries) UpdateEntry(context.Context, int64, int64, int64, storage.UpdateEntryParams) (storage.Entry, error) {
	return storage.Entry{}, nil
}
func (uiMemEntries) ListEntries(_ context.Context, _ int64, _ storage.ListEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, nil
}
func (uiMemEntries) ListFeedEntries(context.Context, int64, int64, storage.ListEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, nil
}
func (uiMemEntries) SearchEntries(context.Context, int64, storage.SearchEntriesFilter) ([]storage.Entry, int, error) {
	return nil, 0, nil
}
func (uiMemEntries) ListEnclosuresByEntryIDs(context.Context, int64, []int64) (map[int64][]storage.Enclosure, error) {
	return nil, nil
}
func (uiMemEntries) CountUnreadByFeed(context.Context, int64) (int, error)        { return 0, nil }
func (uiMemEntries) CountUnreadByCategory(context.Context, int64) (int, error)    { return 0, nil }
func (uiMemEntries) CountUnreadGlobal(context.Context) (int, error)               { return 0, nil }
func (uiMemEntries) CountUnreadGlobalForUser(context.Context, int64) (int, error) { return 0, nil }
func (uiMemEntries) UnreadCountsForUser(context.Context, int64) (map[int64]int, map[int64]int, error) {
	return map[int64]int{}, map[int64]int{}, nil
}
func (uiMemEntries) BulkUpdateEntries(context.Context, int64, []int64, storage.BulkEntryUpdate) (int, error) {
	return 0, nil
}
func (uiMemEntries) MarkAllFeedEntriesRead(context.Context, int64, int64) (int, error) { return 0, nil }
func (uiMemEntries) MarkAllCategoryEntriesRead(context.Context, int64, int64) (int, error) {
	return 0, nil
}
func (uiMemEntries) MarkAllEntriesRead(context.Context, int64) (int, error) { return 0, nil }

func newTestUIHandler(t *testing.T, admin bool) *Handler {
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
			IsAdmin:      admin,
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestUI_LoginRedirectUnread(t *testing.T) {
	h := newTestUIHandler(t, false)
	mux := http.NewServeMux()
	h.Register(mux)

	// Protected route redirects to login.
	req := httptest.NewRequest(http.MethodGet, "/ui/unread", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "/ui/login") {
		t.Fatalf("got %d location=%q", rec.Code, rec.Header().Get("Location"))
	}

	// Login page OK.
	req = httptest.NewRequest(http.MethodGet, "/ui/login", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "rssam") {
		t.Fatalf("login page: status=%d", rec.Code)
	}

	// Login POST.
	token := auth.CSRFToken("csrf-test", "login")
	form := url.Values{"username": {"alice"}, "password": {"secret"}, "csrf_token": {token}}
	req = httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/unread" {
		t.Fatalf("login post: %d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
	cookie := rec.Result().Cookies()
	var sid string
	for _, c := range cookie {
		if c.Name == auth.SessionCookieName {
			sid = c.Value
		}
	}
	if sid == "" {
		t.Fatal("expected session cookie")
	}

	req = httptest.NewRequest(http.MethodGet, "/ui/unread", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unread after login: %d", rec.Code)
	}
}

func TestUI_AdminForbidden(t *testing.T) {
	h := newTestUIHandler(t, false)
	mux := http.NewServeMux()
	h.Register(mux)

	// Login to get session cookie.
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

	req = httptest.NewRequest(http.MethodGet, "/ui/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestUI_CSRFRejected(t *testing.T) {
	h := newTestUIHandler(t, false)
	mux := http.NewServeMux()
	h.Register(mux)

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

	form = url.Values{"password": {"newpass123"}}
	req = httptest.NewRequest(http.MethodPost, "/ui/settings/password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected CSRF 403, got %d", rec.Code)
	}
}
