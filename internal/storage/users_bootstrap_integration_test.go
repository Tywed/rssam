//go:build integration

package storage

import (
	"context"
	"testing"

	"rssam/internal/auth"
)

func restorePlaceholderUser(t *testing.T, store *PostgresStore) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = store.db.Exec(context.Background(), `
UPDATE users
SET username = 'default', password_hash = '', fever_api_key = '', is_admin = TRUE
WHERE id = 1`)
		_, _ = store.db.Exec(context.Background(), `DELETE FROM users WHERE username IN ('bootstrap-admin', 'default-adopt') AND id <> 1`)
	})
}

func requireEmptyTenantForBootstrap(t *testing.T, store *PostgresStore) {
	t.Helper()
	ctx := context.Background()
	n, err := store.CountLoginCapableUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n > 0 {
		t.Skipf("skipping bootstrap test: %d login-capable users already exist (use an empty test database)", n)
	}
	var feeds int
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM feeds`).Scan(&feeds); err != nil {
		t.Fatal(err)
	}
	if feeds > 0 {
		t.Skip("skipping bootstrap test: database already has feeds")
	}
}

// Migration 0009 seeds a password-less "default" user; it must not count as a
// bootstrapped admin. The env admin is written onto id=1 so AUTH_TOKEN and the
// UI share the tenant.
func TestIntegration_EnsureBootstrapAdmin_AdoptsPlaceholderID1(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	restorePlaceholderUser(t, store)
	requireEmptyTenantForBootstrap(t, store)

	if _, err := store.db.Exec(ctx, `
UPDATE users
SET username = 'default', password_hash = '', fever_api_key = '', is_admin = TRUE
WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	if err := store.EnsureBootstrapAdmin(ctx, "bootstrap-admin", "s3cret-pass"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	u, err := store.GetUser(ctx, 1)
	if err != nil {
		t.Fatalf("id=1: %v", err)
	}
	if u.Username != "bootstrap-admin" || !u.IsAdmin || !auth.CheckPassword(u.PasswordHash, "s3cret-pass") {
		t.Fatalf("id=1 not adopted: username=%q is_admin=%v hash_ok=%v", u.Username, u.IsAdmin, auth.CheckPassword(u.PasswordHash, "s3cret-pass"))
	}
	if _, err := store.GetUserByUsername(ctx, "default"); err != ErrNotFound {
		t.Fatalf("placeholder name should be gone, err=%v", err)
	}

	if err := store.EnsureBootstrapAdmin(ctx, "another-admin", "x-pass-123"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetUserByUsername(ctx, "another-admin"); err != ErrNotFound {
		t.Fatalf("second bootstrap should be a no-op, err=%v", err)
	}
}

func TestIntegration_EnsureBootstrapAdmin_AdoptsPlaceholderWithSameName(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	restorePlaceholderUser(t, store)
	requireEmptyTenantForBootstrap(t, store)

	if _, err := store.db.Exec(ctx, `
UPDATE users
SET username = 'default', password_hash = '', fever_api_key = '', is_admin = TRUE
WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `INSERT INTO users(username, password_hash, is_admin) VALUES ('default-adopt', '', FALSE) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}

	if err := store.EnsureBootstrapAdmin(ctx, "default-adopt", "adopt-pass-1"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	u, err := store.GetUserByUsername(ctx, "default-adopt")
	if err != nil {
		t.Fatal(err)
	}
	if !u.IsAdmin || !auth.CheckPassword(u.PasswordHash, "adopt-pass-1") {
		t.Fatalf("placeholder not adopted: is_admin=%v", u.IsAdmin)
	}
	placeholder, err := store.GetUser(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if placeholder.Username != "default" || placeholder.PasswordHash != "" {
		t.Fatalf("id=1 should stay the empty default placeholder, got %+v", placeholder)
	}
}
