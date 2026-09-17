//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"

	"rssam/internal/migrations"
)

// 0046 turns every webhooks.filter_id binding into a filter action, exactly
// once, and clears the column; running it again changes nothing.
func TestIntegration_Migration0046_LegacyWebhookBinding(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "m46")
	wh, err := store.CreateWebhook(ctx, CreateWebhookParams{UserID: owner.ID, Name: "legacy", URL: "https://example.com/l", Headers: []byte(`{}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// already bound through an action: must not be duplicated
	bound, err := store.CreateFilter(ctx, CreateFilterParams{
		UserID: owner.ID, Name: "bound", Enabled: true, FeedScope: FilterFeedScopeAll,
		Rules:   []CreateFilterRuleParams{{Field: "title", Pattern: "a", Op: "and"}},
		Actions: []CreateFilterActionParams{{ActionType: FilterActionWebhook, ActionParam: fmt.Sprint(wh.ID)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// bound only through the legacy column
	legacy, err := store.CreateFilter(ctx, CreateFilterParams{
		UserID: owner.ID, Name: "legacy", Enabled: true, FeedScope: FilterFeedScopeAll,
		Rules: []CreateFilterRuleParams{{Field: "title", Pattern: "b", Op: "and"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []Filter{bound, legacy} {
		wh2, err := store.CreateWebhook(ctx, CreateWebhookParams{UserID: owner.ID, Name: "wh-" + f.Name, URL: "https://example.com/" + f.Name, Headers: []byte(`{}`), Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if f.ID == bound.ID {
			wh2 = wh // bind the same webhook the action already names
		}
		if _, err := store.db.Exec(ctx, `UPDATE webhooks SET filter_id = $1 WHERE id = $2`, f.ID, wh2.ID); err != nil {
			t.Fatal(err)
		}
	}

	body, err := migrations.Source("0046_webhook_filter_id_to_actions.sql")
	if err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		if _, err := store.db.Exec(ctx, body); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		var withFilter int
		if err := store.db.QueryRow(ctx, `SELECT count(*) FROM webhooks WHERE filter_id IS NOT NULL`).Scan(&withFilter); err != nil {
			t.Fatal(err)
		}
		if withFilter != 0 {
			t.Fatalf("run %d: %d webhooks still carry filter_id", run, withFilter)
		}
		b, err := store.GetFilter(ctx, owner.ID, bound.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(b.Actions) != 1 || b.Actions[0].ActionParam != fmt.Sprint(wh.ID) {
			t.Fatalf("run %d: bound filter actions = %+v, want the single pre-existing one", run, b.Actions)
		}
		l, err := store.GetFilter(ctx, owner.ID, legacy.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(l.Actions) != 1 || l.Actions[0].ActionType != FilterActionWebhook {
			t.Fatalf("run %d: legacy filter actions = %+v, want one webhook action", run, l.Actions)
		}
	}
}
