package ui

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"rssam/internal/audit"
	"rssam/internal/auth"
	"rssam/internal/storage"
)

// The admin user table lets an admin toggle the role of other users and set a
// new password; both are CSRF-protected, audited, and a password reset revokes
// the target's sessions. Self-demotion and demoting the last admin are refused.
func TestUI_AdminUserRoleAndPassword(t *testing.T) {
	users := &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true}}
	sessions := &uiMemSessions{sessions: map[string]storage.Session{}}
	auditStore := &memAuditStore{}
	h, err := NewHandler(Config{
		Users:      users,
		Sessions:   sessions,
		Entries:    uiMemEntries{},
		Feeds:      &uiMemFeeds{},
		Categories: &uiMemCategories{},
		CSRFSecret: "csrf-test",
		Audit:      &audit.Recorder{Store: auditStore},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)
	token := auth.CSRFToken("csrf-test", sid)

	if rec := postForm(t, mux, sid, "/ui/admin/users/7/role", url.Values{"is_admin": {"1"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("role without csrf: %d", rec.Code)
	}
	if rec := postForm(t, mux, sid, "/ui/admin/users/1/role", url.Values{"csrf_token": {token}, "is_admin": {"0"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("self-demote: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postForm(t, mux, sid, "/ui/admin/users/7/role", url.Values{"csrf_token": {token}, "is_admin": {"1"}}); rec.Code != http.StatusFound {
		t.Fatalf("promote: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postForm(t, mux, sid, "/ui/admin/users/7/password", url.Values{"csrf_token": {token}, "password": {"short"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("weak password: %d", rec.Code)
	}
	if rec := postForm(t, mux, sid, "/ui/admin/users/7/password", url.Values{"csrf_token": {token}, "password": {"longenough1"}}); rec.Code != http.StatusFound {
		t.Fatalf("reset password: %d %s", rec.Code, rec.Body.String())
	}
	if len(users.updates) != 2 || users.updates[0].ID != 7 || users.updates[0].IsAdmin == nil || !*users.updates[0].IsAdmin || users.updates[0].PasswordHash != nil {
		t.Fatalf("role update params = %+v", users.updates)
	}
	if p := users.updates[1]; p.ID != 7 || p.IsAdmin != nil || p.PasswordHash == nil || !auth.CheckPassword(*p.PasswordHash, "longenough1") {
		t.Fatalf("password update params = %+v", p)
	}
	if len(sessions.revoked) != 1 || sessions.revoked[0] != 7 {
		t.Fatalf("password reset must revoke user 7 sessions, got %v", sessions.revoked)
	}
	got := make([]string, 0, len(auditStore.rows))
	for _, e := range auditStore.rows {
		got = append(got, e.Action)
	}
	if strings.Join(got, ",") != "user.update,user.update" {
		t.Fatalf("audit actions = %v", got)
	}
	if d := auditStore.rows[0].Details; d["is_admin"] != true {
		t.Fatalf("role audit details = %v", d)
	}
	if d := auditStore.rows[1].Details; d["password"] != true {
		t.Fatalf("password audit details = %v", d)
	}

	users.updateErr = storage.ErrLastAdmin
	if rec := postForm(t, mux, sid, "/ui/admin/users/7/role", url.Values{"csrf_token": {token}, "is_admin": {"0"}}); rec.Code != http.StatusConflict {
		t.Fatalf("demote last admin: %d", rec.Code)
	}
	users.updateErr = storage.ErrNotFound
	if rec := postForm(t, mux, sid, "/ui/admin/users/7/role", url.Values{"csrf_token": {token}, "is_admin": {"0"}}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user: %d", rec.Code)
	}
	users.updateErr = errors.New("boom")
	if rec := postForm(t, mux, sid, "/ui/admin/users/7/password", url.Values{"csrf_token": {token}, "password": {"longenough1"}}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("store failure: %d", rec.Code)
	}

	// Non-admins never reach the handlers.
	h2 := newTestUIHandler(t, false)
	mux2 := http.NewServeMux()
	h2.Register(mux2)
	sid2 := uiSessionCookie(t, h2, mux2)
	if rec := postForm(t, mux2, sid2, "/ui/admin/users/7/role", url.Values{"csrf_token": {auth.CSRFToken("csrf-test", sid2)}, "is_admin": {"1"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d", rec.Code)
	}
}
