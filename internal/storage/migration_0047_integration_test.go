//go:build integration

package storage

import (
	"context"
	"testing"

	"rssam/internal/migrations"
)

// 0047 deletes the leftover "tags" rules and disables the filters that
// carried them (an always-false rule kept them from ever firing); filters
// without such rules are untouched, and a second run changes nothing.
func TestIntegration_Migration0047_DropTagsRules(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "m47")
	mk := func(name string) Filter {
		t.Helper()
		f, err := store.CreateFilter(ctx, CreateFilterParams{
			UserID: owner.ID, Name: name, Enabled: true, FeedScope: FilterFeedScopeAll,
			Rules: []CreateFilterRuleParams{{Field: "title", Pattern: name, Op: "and"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	clean, tagged := mk("clean"), mk("tagged")
	if _, err := store.db.Exec(ctx, `INSERT INTO filter_rules(filter_id, field, pattern, op) VALUES ($1, 'tags', 'x', 'and')`, tagged.ID); err != nil {
		t.Fatal(err)
	}

	body, err := migrations.Source("0047_drop_tags_filter_rules.sql")
	if err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		if _, err := store.db.Exec(ctx, body); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		var tags int
		if err := store.db.QueryRow(ctx, `SELECT count(*) FROM filter_rules WHERE field = 'tags'`).Scan(&tags); err != nil {
			t.Fatal(err)
		}
		if tags != 0 {
			t.Fatalf("run %d: %d tags rules left", run, tags)
		}
		c, err := store.GetFilter(ctx, owner.ID, clean.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !c.Enabled || len(c.Rules) != 1 {
			t.Fatalf("run %d: clean filter touched: %+v", run, c)
		}
		tg, err := store.GetFilter(ctx, owner.ID, tagged.ID)
		if err != nil {
			t.Fatal(err)
		}
		if tg.Enabled || len(tg.Rules) != 1 || tg.Rules[0].Field != "title" {
			t.Fatalf("run %d: tagged filter = enabled:%v rules:%+v, want disabled with the title rule only", run, tg.Enabled, tg.Rules)
		}
	}
	// Re-enabling the filter after review sticks: the migration only acts
	// on filters that still carry a tags rule.
	if _, err := store.db.Exec(ctx, `UPDATE filters SET enabled = true WHERE id = $1`, tagged.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, body); err != nil {
		t.Fatal(err)
	}
	if tg, _ := store.GetFilter(ctx, owner.ID, tagged.ID); !tg.Enabled {
		t.Fatal("re-run disabled a filter without tags rules")
	}
}
