package httpserver

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

// POST /v1/users and PUT /v1/me enforce the password policy server-side.
func TestCreateUser_PasswordPolicy(t *testing.T) {
	users := newMemUserStore()
	s := New(Dependencies{UserStore: users})
	admin := auth.Principal{UserID: 1, IsAdmin: true}

	do := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/users", bytes.NewBufferString(body))
		req = req.WithContext(auth.WithPrincipal(req.Context(), admin))
		rec := httptest.NewRecorder()
		s.handleCreateUser(rec, req)
		return rec
	}
	if rec := do(`{"username":"short","password":"1234567"}`); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "at least 8") {
		t.Fatalf("7-char password accepted: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(`{"username":"long","password":"` + strings.Repeat("a", 73) + `"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("73-byte password accepted: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(`{"username":"ok","password":"12345678"}`); rec.Code != http.StatusCreated {
		t.Fatalf("8-char password rejected: %d %s", rec.Code, rec.Body.String())
	}
}

// The env bootstrap credentials are the operator's choice: a short
// ADMIN_PASSWORD must not brick the first-run bootstrap.
func TestCreateUser_BootstrapEnvPasswordBypassesPolicy(t *testing.T) {
	users := newMemUserStore()
	users.users = map[int64]storage.User{} // no login-capable users yet
	users.byName = map[string]int64{}
	s := New(Dependencies{UserStore: users, AdminUsername: "root", AdminPassword: "short"})

	req := httptest.NewRequest(http.MethodPost, "/v1/users", bytes.NewBufferString(`{"username":"root","password":"short"}`))
	rec := httptest.NewRecorder()
	s.handleCreateUser(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("env bootstrap rejected: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateMe_PasswordPolicy(t *testing.T) {
	users := newMemUserStore()
	hash, _ := auth.HashPassword("oldpass123")
	u := users.users[1]
	u.PasswordHash = hash
	users.users[1] = u
	s := New(Dependencies{UserStore: users, SessionStore: &recSessions{}})
	p := auth.Principal{UserID: 1, IsAdmin: true}

	req := httptest.NewRequest(http.MethodPut, "/v1/me", bytes.NewBufferString(`{"current_password":"oldpass123","password":"short"}`))
	req = req.WithContext(auth.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	s.handleUpdateMe(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short password accepted on PUT /v1/me: %d %s", rec.Code, rec.Body.String())
	}
}
