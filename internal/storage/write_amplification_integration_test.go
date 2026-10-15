//go:build integration

package storage

import (
	"context"
	"testing"
)

// Migration 0045 pins a storage parameter the write path relies on; a later
// migration that recreates the table must carry it over.
func TestIntegration_Migration0045StorageParameters(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	var target int
	if err := store.db.QueryRow(ctx, `
SELECT coalesce((SELECT option_value::int FROM pg_options_to_table(reloptions) WHERE option_name = 'toast_tuple_target'), 0)
FROM pg_class WHERE relname = 'entries'`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if target != 128 {
		t.Fatalf("entries.toast_tuple_target = %d, want 128", target)
	}
}
