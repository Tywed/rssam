package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type memSessionStore struct {
	s storage.Session
}

func (m *memSessionStore) CreateSession(_ context.Context, userID int64, sessionID string, expiresAt time.Time) (storage.Session, error) {
	m.s = storage.Session{UserID: userID, SessionID: sessionID, ExpiresAt: expiresAt}
	return m.s, nil
}
func (m *memSessionStore) LookupSession(_ context.Context, sessionID string) (storage.Session, error) {
	if m.s.SessionID == sessionID {
		return m.s, nil
	}
	return storage.Session{}, storage.ErrNotFound
}
func (m *memSessionStore) TouchSession(_ context.Context, _ string, _ time.Time) error { return nil }
func (m *memSessionStore) DeleteSession(_ context.Context, _ string) error               { return nil }
func (m *memSessionStore) DeleteUserSessions(_ context.Context, _ int64) error           { return nil }

func TestAuthenticateSessionCookie(t *testing.T) {
	sessions := &memSessionStore{}
	sessions.s = storage.Session{UserID: 42, SessionID: "sess42", ExpiresAt: time.Now().Add(time.Hour)}

	s := &Server{
		sessions: sessions,
		users: &uiMemUsersWS{user: storage.User{ID: 42, IsAdmin: true}},
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "sess42"})
	p, ok := s.authenticateRequest(req.Context(), req)
	if !ok || p.UserID != 42 || !p.IsAdmin {
		t.Fatalf("session auth failed: ok=%v p=%+v", ok, p)
	}
}

type uiMemUsersWS struct{ user storage.User }

func (u *uiMemUsersWS) CountUsers(context.Context) (int, error) { return 1, nil }
func (u *uiMemUsersWS) ListUsers(context.Context, int, int) ([]storage.User, int, error) {
	return nil, 0, nil
}
func (u *uiMemUsersWS) GetUser(_ context.Context, id int64) (storage.User, error) {
	if id == u.user.ID {
		return u.user, nil
	}
	return storage.User{}, storage.ErrNotFound
}
func (u *uiMemUsersWS) GetUserByUsername(context.Context, string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}
func (u *uiMemUsersWS) GetUserByFeverAPIKey(context.Context, string) (storage.User, error) {
	return storage.User{}, storage.ErrNotFound
}
func (u *uiMemUsersWS) CreateUser(context.Context, storage.CreateUserParams) (storage.User, error) {
	return storage.User{}, nil
}
func (u *uiMemUsersWS) UpdateUser(context.Context, storage.UpdateUserParams) (storage.User, error) {
	return u.user, nil
}
func (u *uiMemUsersWS) DeleteUser(context.Context, int64) error { return nil }
func (u *uiMemUsersWS) LookupAPIKey(context.Context, string) (storage.APIKey, error) {
	return storage.APIKey{}, storage.ErrNotFound
}
func (u *uiMemUsersWS) TouchAPIKeyUsed(context.Context, int64) error { return nil }
func (u *uiMemUsersWS) ListAPIKeys(context.Context, int64) ([]storage.APIKey, error) {
	return nil, nil
}
func (u *uiMemUsersWS) CreateAPIKey(context.Context, storage.CreateAPIKeyParams) (storage.APIKey, error) {
	return storage.APIKey{}, nil
}
func (u *uiMemUsersWS) DeleteAPIKey(context.Context, int64, int64) error { return nil }

func TestWSAuthSessionCookie(t *testing.T) {
	sessions := &memSessionStore{s: storage.Session{UserID: 1, SessionID: "abc", ExpiresAt: time.Now().Add(time.Hour)}}
	s := &Server{
		wsEnabled: true,
		sessions:  sessions,
		users:     &uiMemUsersWS{user: storage.User{ID: 1}},
	}
	req := httptest.NewRequest(http.MethodGet, "/ws/v1", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "abc"})
	if !s.hasValidAuthToken(req) {
		t.Fatal("expected ws cookie auth")
	}
}
