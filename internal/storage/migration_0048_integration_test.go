//go:build integration

package storage

import (
	"context"
	"testing"

	"rssam/internal/migrations"
)

// 0048 removes webhooks.filter_id; the migration is idempotent and the
// existing webhooks survive it.
func TestIntegration_Migration0048_DropWebhooksFilterID(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "m48")
	w, err := store.CreateWebhook(ctx, CreateWebhookParams{UserID: owner.ID, Name: "hook", Kind: WebhookKindHTTP, URL: "https://example.com/hook", Headers: []byte(`{}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	body, err := migrations.Source("0048_drop_webhooks_filter_id.sql")
	if err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		if _, err := store.db.Exec(ctx, body); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		var n int
		if err := store.db.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'webhooks' AND column_name = 'filter_id'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("run %d: filter_id still present", run)
		}
		got, err := store.GetWebhook(ctx, owner.ID, w.ID)
		if err != nil || got.URL != w.URL {
			t.Fatalf("run %d: webhook after migration: %+v err=%v", run, got, err)
		}
	}
}
