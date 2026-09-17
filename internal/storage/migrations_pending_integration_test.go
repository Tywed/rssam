//go:build integration

package storage

import (
	"context"
	"testing"

	"rssam/internal/migrations"
)

// Pending drives the RUN_MIGRATIONS=false startup refusal: a fully migrated
// schema reports nothing, one forgotten row reports exactly that file, and a
// schema without schema_migrations reports everything.
func TestIntegration_MigrationsPending(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()

	if p, err := migrations.Pending(ctx, store.db); err != nil || len(p) != 0 {
		t.Fatalf("migrated schema: pending=%v err=%v", p, err)
	}
	if _, err := store.db.Exec(ctx, `DELETE FROM schema_migrations WHERE version = '0047_drop_tags_filter_rules.sql'`); err != nil {
		t.Fatal(err)
	}
	p, err := migrations.Pending(ctx, store.db)
	if err != nil || len(p) != 1 || p[0] != "0047_drop_tags_filter_rules.sql" {
		t.Fatalf("one row removed: pending=%v err=%v", p, err)
	}
	if _, err := store.db.Exec(ctx, `DROP TABLE schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if p, err := migrations.Pending(ctx, store.db); err != nil || len(p) < 47 {
		t.Fatalf("no table: pending=%d err=%v", len(p), err)
	}
}
