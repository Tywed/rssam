//go:build integration

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"rssam/internal/auth"
)

// Every feed list returns the same row as GetFeed minus icon_data. Lists used
// to project a handful of columns, so rules, flags and poll state were
// silently zero outside GetFeed (OPML export lost settings twice).
func TestIntegration_FeedListsMatchGetFeed(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	hash, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(ctx, CreateUserParams{Username: "proj_" + suffix, PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), u.ID) })

	cat, err := store.CreateCategory(ctx, u.ID, "c"+suffix, "")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	retention := 7
	created, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{
		FeedURL:            "https://example.com/proj/" + suffix + ".xml",
		FeedType:           "rss",
		Title:              "proj",
		CategoryID:         &cat.ID,
		IntervalMinutes:    45,
		ScraperRules:       "div.body",
		RewriteRules:       "a=b",
		BlockedRules:       "spam",
		KeepRules:          "keep",
		FetchViaProxy:      true,
		TLSInsecure:        true,
		Crawler:            true,
		UserAgent:          "UA/1",
		StoreHashOnly:      true,
		EntryRetentionDays: &retention,
		BridgeState:        []byte(`{"k":"v"}`),
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if _, err := store.db.Exec(ctx, `
UPDATE feeds SET last_error = 'boom', parsing_error_count = 2, poll_paused = TRUE, manual_paused = TRUE,
       etag = 'W/"1"', last_modified = 'Mon, 01 Jan 2024 00:00:00 GMT', last_checked_at = now(), icon_url = 'https://example.com/i.png',
       icon_data = '\x89504e47'::bytea
WHERE id = $1`, created.ID); err != nil {
		t.Fatal(err)
	}
	want, err := store.GetFeed(ctx, u.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(want.IconData) == 0 {
		t.Fatal("GetFeed must return icon_data")
	}
	want.IconData = nil

	find := func(name string, feeds []Feed) Feed {
		t.Helper()
		for _, f := range feeds {
			if f.ID == created.ID {
				return f
			}
		}
		t.Fatalf("%s: feed %d missing", name, created.ID)
		return Feed{}
	}
	check := func(name string, got Feed) {
		t.Helper()
		if len(got.IconData) != 0 {
			t.Errorf("%s: icon_data must not be loaded by lists", name)
		}
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		if string(g) != string(w) {
			t.Errorf("%s:\n got  %s\n want %s", name, g, w)
		}
	}

	feeds, _, err := store.ListFeeds(ctx, u.ID, NoLimit, 0)
	if err != nil {
		t.Fatal(err)
	}
	check("ListFeeds", find("ListFeeds", feeds))

	feeds, _, err = store.ListFeedsByCategoryPaginated(ctx, u.ID, cat.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	check("ListFeedsByCategoryPaginated", find("ListFeedsByCategoryPaginated", feeds))

	feeds, _, err = store.ListFeedsByStatus(ctx, u.ID, "errors", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	check("ListFeedsByStatus", find("ListFeedsByStatus", feeds))

	all, err := store.ListAllFeeds(ctx, 10000)
	if err != nil {
		t.Fatal(err)
	}
	check("ListAllFeeds", find("ListAllFeeds", all))
}
