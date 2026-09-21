//go:build integration

package storage

import (
	"context"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/migrations"
)

// 0049 turns users.is_admin into users.role: admins become admin, everyone
// else reader; a second run is a no-op.
func TestIntegration_Migration0049_UserRoles(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	// Restore the pre-0049 shape: role gone, is_admin back.
	for _, q := range []string{
		`ALTER TABLE users DROP COLUMN role`,
		`ALTER TABLE users ADD COLUMN is_admin BOOLEAN NOT NULL DEFAULT FALSE`,
		`INSERT INTO users(username, password_hash, is_admin) VALUES ('m49_root', 'h', TRUE), ('m49_bob', 'h', FALSE)`,
	} {
		if _, err := store.db.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	body, err := migrations.Source("0049_user_roles.sql")
	if err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		if _, err := store.db.Exec(ctx, body); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		var n int
		if err := store.db.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'users' AND column_name = 'is_admin'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("run %d: is_admin still present", run)
		}
		root, err := store.GetUserByUsername(ctx, "m49_root")
		if err != nil || root.Role != auth.RoleAdmin {
			t.Fatalf("run %d: root: %+v err=%v", run, root, err)
		}
		bob, err := store.GetUserByUsername(ctx, "m49_bob")
		if err != nil || bob.Role != auth.RoleReader {
			t.Fatalf("run %d: bob: %+v err=%v", run, bob, err)
		}
	}
	if _, err := store.db.Exec(ctx, `UPDATE users SET role = 'root' WHERE username = 'm49_bob'`); err == nil {
		t.Fatal("role check constraint missing")
	}
}
