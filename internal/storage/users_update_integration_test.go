//go:build integration

package storage

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// UpdateUser applies only the fields that are set, refuses to demote the
// last login-capable admin (also when a demotion races a deletion) and
// reports ErrNotFound for unknown ids.
func TestUpdateUser_RoleAndPassword(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	mk := func(name string, admin bool) User {
		t.Helper()
		u, err := store.CreateUser(ctx, CreateUserParams{Username: name, PasswordHash: "x", IsAdmin: admin})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return u
	}
	on, off := true, false
	a := mk("adm_a", true)
	bob := mk("bob", false)

	if _, err := store.UpdateUser(ctx, UpdateUserParams{ID: 999999, IsAdmin: &on}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user: got %v, want ErrNotFound", err)
	}
	if _, err := store.UpdateUser(ctx, UpdateUserParams{ID: a.ID, IsAdmin: &off}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote last admin: got %v, want ErrLastAdmin", err)
	}
	u, err := store.UpdateUser(ctx, UpdateUserParams{ID: bob.ID, IsAdmin: &on})
	if err != nil || !u.IsAdmin || u.PasswordHash != "x" {
		t.Fatalf("promote bob: %+v, %v", u, err)
	}
	hash := "y"
	u, err = store.UpdateUser(ctx, UpdateUserParams{ID: a.ID, IsAdmin: &off, PasswordHash: &hash})
	if err != nil || u.IsAdmin || u.PasswordHash != "y" {
		t.Fatalf("demote a with bob present: %+v, %v", u, err)
	}
	u, err = store.UpdateUser(ctx, UpdateUserParams{ID: a.ID})
	if err != nil || u.IsAdmin || u.PasswordHash != "y" {
		t.Fatalf("no-op update must return the current row: %+v, %v", u, err)
	}
	// A password-less admin is not login-capable: promoting one must not
	// make bob demotable.
	ghost := mk("ghost", false)
	if _, err := store.db.Exec(ctx, `UPDATE users SET password_hash = '', is_admin = true WHERE id = $1`, ghost.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateUser(ctx, UpdateUserParams{ID: bob.ID, IsAdmin: &off}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote bob with password-less admin present: got %v, want ErrLastAdmin", err)
	}

	// Race: two admins, demote one while deleting the other. Exactly one
	// operation succeeds and one login-capable admin survives.
	for i := 0; i < 5; i++ {
		if _, err := store.UpdateUser(ctx, UpdateUserParams{ID: a.ID, IsAdmin: &on}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() { defer wg.Done(); _, errs[0] = store.UpdateUser(ctx, UpdateUserParams{ID: a.ID, IsAdmin: &off}) }()
		go func() {
			defer wg.Done()
			_, errs[1] = store.UpdateUser(ctx, UpdateUserParams{ID: bob.ID, IsAdmin: &off})
		}()
		wg.Wait()
		okCount, lastCount := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				okCount++
			case errors.Is(err, ErrLastAdmin):
				lastCount++
			default:
				t.Fatalf("round %d: unexpected error %v", i, err)
			}
		}
		if okCount != 1 || lastCount != 1 {
			t.Fatalf("round %d: errs=%v, want exactly one success and one ErrLastAdmin", i, errs)
		}
		var remaining int
		if err := store.db.QueryRow(ctx, `SELECT count(*) FROM users WHERE is_admin AND password_hash <> ''`).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != 1 {
			t.Fatalf("round %d: %d login-capable admins left, want 1", i, remaining)
		}
		if _, err := store.UpdateUser(ctx, UpdateUserParams{ID: bob.ID, IsAdmin: &on}); err != nil {
			t.Fatal(err)
		}
	}
}
