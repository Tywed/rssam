//go:build integration

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"rssam/internal/reader"
	"rssam/internal/storage"
)

// A relay feed keeps a matched item only until its filter webhook is
// delivered: no unread rows, no filter_matches, hidden from lists, and
// only the dedup hash survives the delivery. Items no filter wants are a
// hash from the start.
func TestE2E_RelayFeed_WebhookOnlyThenHash(t *testing.T) {
	env := newEnv(t, map[string]string{
		"/relay.xml": rssDocument("Relay feed", []string{"ALERT disk full", "plain news"}),
	})
	ctx := context.Background()
	label, err := env.store.CreateLabel(ctx, storage.CreateLabelParams{UserID: env.user.ID, Caption: fmt.Sprintf("relay-%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.store.DeleteLabel(context.Background(), env.user.ID, label.ID) })
	wh := env.createWebhook(t, storage.CreateWebhookParams{Name: "relay", URL: env.hooks.URL + "/relay"})
	if _, err := env.store.CreateFilter(ctx, storage.CreateFilterParams{
		UserID: env.user.ID, Name: "relay alerts", Enabled: true, FeedScope: storage.FilterFeedScopeAll,
		Rules: []storage.CreateFilterRuleParams{{Field: "title", Pattern: `^ALERT\b`}},
		Actions: []storage.CreateFilterActionParams{
			{ActionType: storage.FilterActionLabel, ActionParam: fmt.Sprint(label.ID)},
			{ActionType: storage.FilterActionWebhook, ActionParam: fmt.Sprint(wh.ID)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	feed, err := env.store.CreateFeed(ctx, env.user.ID, storage.CreateFeedParams{
		FeedURL: env.feeds.URL + "/relay.xml", FeedType: reader.FeedTypeRSS, Title: "relay", IntervalMinutes: 60,
		StoreMode: storage.StoreModeRelay,
	})
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, "relay webhook delivered", func() bool {
		logs := env.webhookLogs(t, wh.ID)
		return len(logs) == 1 && logs[0].Status == "sent"
	})
	if got := env.receiver.byPath("/relay"); len(got) != 1 {
		t.Fatalf("relay webhook requests = %d, want 1", len(got))
	}

	var entries, withTitle, unread, matches, labels, dedup int
	row := env.pool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM entries WHERE feed_id = $1),
       (SELECT count(*) FROM entries WHERE feed_id = $1 AND title <> ''),
       (SELECT count(*) FROM user_unread WHERE feed_id = $1),
       (SELECT count(*) FROM filter_matches fm JOIN entries e ON e.id = fm.entry_id WHERE e.feed_id = $1),
       (SELECT count(*) FROM entry_labels el JOIN entries e ON e.id = el.entry_id WHERE e.feed_id = $1),
       (SELECT count(*) FROM feed_entry_dedup WHERE feed_id = $1)`, feed.ID)
	if err := row.Scan(&entries, &withTitle, &unread, &matches, &labels, &dedup); err != nil {
		t.Fatal(err)
	}
	// The matched item is a stripped shell awaiting purge; the unmatched one
	// was never an entry. Both are dedup hashes now.
	if entries != 1 || withTitle != 0 || unread != 0 || matches != 0 || labels != 0 || dedup != 2 {
		t.Fatalf("after delivery: entries=%d withTitle=%d unread=%d matches=%d labels=%d dedup=%d", entries, withTitle, unread, matches, labels, dedup)
	}
	if list := env.feedEntries(t, feed.ID); len(list) != 0 {
		t.Fatalf("relay entries must stay out of lists: %+v", list)
	}
	if perFeed, _, err := env.store.UnreadCountsForUser(ctx, env.user.ID); err != nil || perFeed[feed.ID] != 0 {
		t.Fatalf("unread count for relay feed = %d err=%v", perFeed[feed.ID], err)
	}

	// The shell leaves with the removed-entries retention pass.
	if _, err := env.pool.Exec(ctx, `UPDATE entries SET removed_at = now() - interval '60 days' WHERE feed_id = $1`, feed.ID); err != nil {
		t.Fatal(err)
	}
	res, err := env.runner.RetentionCleanupNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM entries WHERE feed_id = $1`, feed.ID).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 0 {
		t.Fatalf("shell not purged: entries=%d cleanup=%+v", entries, res)
	}
}
