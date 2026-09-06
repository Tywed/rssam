//go:build integration

package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// DeleteUser must never remove the last admin that can log in, including
// when two deletions race each other.
func TestDeleteUser_LastAdminGuard(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	var external int
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM users WHERE is_admin AND password_hash <> ''`).Scan(&external); err != nil {
		t.Fatal(err)
	}
	if external > 0 {
		t.Skipf("database already has %d login-capable admin(s); last-admin guard cannot be exercised", external)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	mk := func(name string, admin bool) User {
		t.Helper()
		u, err := store.CreateUser(ctx, CreateUserParams{Username: name + "_" + suffix, PasswordHash: "x", IsAdmin: admin})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		t.Cleanup(func() {
			_, _ = store.db.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, u.ID)
		})
		return u
	}

	a := mk("adm_a", true)
	b := mk("adm_b", true)
	plain := mk("plain", false)

	if err := store.DeleteUser(ctx, a.ID); err != nil {
		t.Fatalf("delete one of two admins: %v", err)
	}
	if err := store.DeleteUser(ctx, b.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("delete last admin: got %v, want ErrLastAdmin", err)
	}
	if _, err := store.GetUser(ctx, b.ID); err != nil {
		t.Fatalf("last admin must still exist: %v", err)
	}
	if err := store.DeleteUser(ctx, plain.ID); err != nil {
		t.Fatalf("delete regular user: %v", err)
	}
	if err := store.DeleteUser(ctx, plain.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing user: got %v, want ErrNotFound", err)
	}
	// The password-less placeholder (id=1) is not login-capable and must not
	// count: with only b left as a real admin, deleting b is still refused.
	if err := store.DeleteUser(ctx, b.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("delete last admin with placeholder present: got %v, want ErrLastAdmin", err)
	}

	// Race: with exactly two admins, delete both concurrently. Exactly one
	// deletion must succeed and one login-capable admin must survive.
	survivor := b.ID
	for i := 0; i < 5; i++ {
		d := mk(fmt.Sprintf("adm_d%d", i), true)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() { defer wg.Done(); errs[0] = store.DeleteUser(ctx, survivor) }()
		go func() { defer wg.Done(); errs[1] = store.DeleteUser(ctx, d.ID) }()
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
		if errs[0] == nil {
			survivor = d.ID
		}
	}
}
