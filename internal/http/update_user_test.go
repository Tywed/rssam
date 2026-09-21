package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

// PUT /v1/users/{id}: admins toggle the role and reset passwords of other
// users; the last login-capable admin cannot be demoted, nobody can demote
// themselves, and a reset password revokes the target's sessions.
func TestUpdateUser(t *testing.T) {
	users := newMemUserStore()
	ctx := context.Background()
	root, err := users.CreateUser(ctx, storage.CreateUserParams{Username: "root", PasswordHash: "h", Role: auth.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := users.CreateUser(ctx, storage.CreateUserParams{Username: "bob", PasswordHash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	rootTok := issueAPIToken(t, users, root.ID)
	bobTok := issueAPIToken(t, users, bob.ID)
	sessions := &memSessionStore{}
	s := New(Dependencies{UserStore: users, SessionStore: sessions})

	put := func(tok string, id int64, body string) *httptest.ResponseRecorder {
		sid := strconv.FormatInt(id, 10)
		req := httptest.NewRequest(http.MethodPut, "/v1/users/"+sid, strings.NewReader(body))
		req.SetPathValue("id", sid)
		req.Header.Set("X-Auth-Token", tok)
		rec := httptest.NewRecorder()
		s.wrapAPI(http.HandlerFunc(s.handleUpdateUser)).ServeHTTP(rec, req)
		return rec
	}

	if rec := put(bobTok, root.ID, `{"role":"reader"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put(rootTok, bob.ID, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put(rootTok, root.ID, `{"role":"reader"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("self-demote: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put(rootTok, 999, `{"role":"admin"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing user: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put(rootTok, bob.ID, `{"password":"short"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("weak password: %d %s", rec.Code, rec.Body.String())
	}

	rec := put(rootTok, bob.ID, `{"role":"admin"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"role":"admin"`) {
		t.Fatalf("promote bob: %d %s", rec.Code, rec.Body.String())
	}
	if len(sessions.revoked) != 0 {
		t.Fatalf("role change must not revoke sessions: %v", sessions.revoked)
	}
	// bob is now an admin and root may step down; afterwards bob is the
	// last login-capable admin and cannot be demoted by anyone else.
	if rec := put(bobTok, root.ID, `{"role":"reader"}`); rec.Code != http.StatusOK {
		t.Fatalf("bob demotes root: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put(rootTok, bob.ID, `{"role":"reader"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("demoted root must lose access: %d", rec.Code)
	}
	if rec := put(bobTok, root.ID, `{"role":"admin"}`); rec.Code != http.StatusOK {
		t.Fatalf("re-promote root: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put(rootTok, bob.ID, `{"role":"reader"}`); rec.Code != http.StatusOK {
		t.Fatalf("demote bob with root present: %d %s", rec.Code, rec.Body.String())
	}
	// The password-less placeholder id=1 never counts as a login-capable
	// admin, so root is the last one now.
	tok1 := issueAPIToken(t, users, 1)
	if rec := put(tok1, root.ID, `{"role":"reader"}`); rec.Code != http.StatusConflict {
		t.Fatalf("demote last admin: %d %s", rec.Code, rec.Body.String())
	}

	if rec := put(rootTok, bob.ID, `{"password":"N3w-longer-pass"}`); rec.Code != http.StatusOK {
		t.Fatalf("reset password: %d %s", rec.Code, rec.Body.String())
	}
	if len(sessions.revoked) != 1 || sessions.revoked[0] != bob.ID {
		t.Fatalf("password reset must revoke bob's sessions, got %v", sessions.revoked)
	}
	updated, err := users.GetUser(ctx, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.IsAdmin() || !auth.CheckPassword(updated.PasswordHash, "N3w-longer-pass") {
		t.Fatalf("stored user not updated: %+v", updated)
	}
}
