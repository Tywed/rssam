package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"rssam/internal/storage"
)

// DELETE /v1/users/{id} must refuse to remove the last admin that can log in,
// otherwise the instance ends up with no one able to administer it via UI.
func TestDeleteUser_LastAdminRefused(t *testing.T) {
	users := newMemUserStore()
	ctx := context.Background()
	root, err := users.CreateUser(ctx, storage.CreateUserParams{Username: "root", PasswordHash: "h", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := users.CreateUser(ctx, storage.CreateUserParams{Username: "second", PasswordHash: "h", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := users.CreateUser(ctx, storage.CreateUserParams{Username: "bob", PasswordHash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	// Caller is the password-less placeholder id=1 (AUTH_TOKEN principal), so
	// it never counts as a login-capable admin and can target every other row.
	tok := issueAPIToken(t, users, 1)

	s := New(Dependencies{UserStore: users})
	del := func(id int64) int {
		req := httptest.NewRequest(http.MethodDelete, "/v1/users/"+strconv.FormatInt(id, 10), nil)
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		req.Header.Set("X-Auth-Token", tok)
		rec := httptest.NewRecorder()
		s.wrapAPI(http.HandlerFunc(s.handleDeleteUser)).ServeHTTP(rec, req)
		return rec.Code
	}

	if got := del(second.ID); got != http.StatusOK {
		t.Fatalf("delete one of two admins: got %d, want 200", got)
	}
	if got := del(root.ID); got != http.StatusConflict {
		t.Fatalf("delete last admin: got %d, want 409", got)
	}
	if _, err := users.GetUser(ctx, root.ID); err != nil {
		t.Fatalf("last admin must still exist: %v", err)
	}
	if got := del(plain.ID); got != http.StatusOK {
		t.Fatalf("delete regular user: got %d, want 200", got)
	}
	if got := del(root.ID); got != http.StatusConflict {
		t.Fatalf("delete last admin after others removed: got %d, want 409", got)
	}
}
