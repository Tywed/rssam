//go:build integration

package storage

import (
	"context"
	"testing"
)

// Migration 0065 removes the toast_tuple_target 0045 set: with read state
// out of entries the 128 B target only split every new entry into five
// TOAST rows. A later migration that recreates the table must not bring
// it back.
func TestIntegration_Migration0065EntriesToastTarget(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	var target int
	if err := store.db.QueryRow(ctx, `
SELECT coalesce((SELECT option_value::int FROM pg_options_to_table(reloptions) WHERE option_name = 'toast_tuple_target'), 0)
FROM pg_class WHERE relname = 'entries'`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if target != 0 {
		t.Fatalf("entries.toast_tuple_target = %d, want default", target)
	}
}
