package httpserver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type recSessions struct {
	memSessionStore
	revokedFor int64
	kept       string
}

func (r *recSessions) DeleteUserSessionsExcept(_ context.Context, userID int64, keep string) error {
	r.revokedFor, r.kept = userID, keep
	return nil
}

// PUT /v1/me password change requires the current password and revokes the
// other sessions.
func TestUpdateMe_RequiresCurrentPasswordAndRevokesSessions(t *testing.T) {
	users := newMemUserStore()
	hash, _ := auth.HashPassword("oldpass123")
	u := users.users[1]
	u.PasswordHash = hash
	users.users[1] = u
	sessions := &recSessions{}
	s := New(Dependencies{UserStore: users, SessionStore: sessions})
	p := auth.Principal{UserID: 1, IsAdmin: true}

	do := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/v1/me", bytes.NewBufferString(body))
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "keep-me"})
		req = req.WithContext(auth.WithPrincipal(req.Context(), p))
		rec := httptest.NewRecorder()
		s.handleUpdateMe(rec, req)
		return rec
	}
	if rec := do(`{"password":"newpass123"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("missing current_password: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(`{"current_password":"nope","password":"newpass123"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong current_password: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(`{"current_password":"oldpass123","password":"newpass123"}`); rec.Code != http.StatusOK {
		t.Fatalf("valid change: %d %s", rec.Code, rec.Body.String())
	}
	if sessions.revokedFor != 1 || sessions.kept != "keep-me" {
		t.Fatalf("other sessions not revoked: for=%d kept=%q", sessions.revokedFor, sessions.kept)
	}
}

// A password-less account may set its first password without proof only
// while no user can log in (bootstrap); afterwards that is an admin's job.
func TestUpdateMe_PasswordlessOnlyDuringBootstrap(t *testing.T) {
	users := newMemUserStore()
	s := New(Dependencies{UserStore: users, SessionStore: &memSessionStore{}})
	do := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/v1/me", bytes.NewBufferString(body))
		req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{UserID: 1, IsAdmin: true}))
		rec := httptest.NewRecorder()
		s.handleUpdateMe(rec, req)
		return rec
	}
	if _, err := users.CreateUser(context.Background(), storage.CreateUserParams{Username: "bob", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if rec := do(`{"password":"newpass123"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("password-less after bootstrap: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(`{"current_password":"","password":"newpass123"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("password-less after bootstrap, empty current: %d %s", rec.Code, rec.Body.String())
	}
	if err := users.DeleteUser(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if rec := do(`{"password":"newpass123"}`); rec.Code != http.StatusOK {
		t.Fatalf("bootstrap: %d %s", rec.Code, rec.Body.String())
	}
	if u, _ := users.GetUser(context.Background(), 1); !auth.CheckPassword(u.PasswordHash, "newpass123") {
		t.Fatalf("password not stored: %+v", u)
	}
	if rec := do(`{"password":"another123"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("second change without current_password: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSystemInfo_AdminOnly(t *testing.T) {
	s := New(Dependencies{UserStore: newMemUserStore()})
	req := httptest.NewRequest(http.MethodGet, "/v1/system/info", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{UserID: 2, IsAdmin: false}))
	rec := httptest.NewRecorder()
	s.handleSystemInfo(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin got %d", rec.Code)
	}
}

var _ storage.SessionStore = (*recSessions)(nil)
