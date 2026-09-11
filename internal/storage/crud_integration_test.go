//go:build integration

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestIntegration_CategoriesAndFeedUpdate(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "crud")
	other := newIntegrationUser(t, store, "crud_other")

	news, err := store.CreateCategory(ctx, owner.ID, "News", "#111111")
	if err != nil {
		t.Fatal(err)
	}
	tech, err := store.CreateCategory(ctx, owner.ID, "Tech", "#222222")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateCategory(ctx, other.ID, news.ID, "Hijack", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign update: err=%v", err)
	}
	upd, err := store.UpdateCategory(ctx, owner.ID, news.ID, "World", " #333333 ")
	if err != nil || upd.Title != "World" || upd.Color != "#333333" {
		t.Fatalf("update category: %+v err=%v", upd, err)
	}

	feed := newFeedForUser(t, store, owner.ID, "cat", 15)
	inTech, err := store.UpdateFeed(ctx, owner.ID, UpdateFeedParams{
		ID: feed.ID, FeedURL: feed.FeedURL, Title: "renamed", CategoryID: &tech.ID, IntervalMinutes: 45,
		UserAgent: "ua/1", Crawler: true, StoreHashOnly: true, EntryRetentionDays: ptr(7), BridgeState: []byte(`{"cursor":1}`),
	})
	if err != nil || inTech.Title != "renamed" || inTech.CategoryID == nil || *inTech.CategoryID != tech.ID || inTech.IntervalMinutes != 45 || !inTech.Crawler || !inTech.StoreHashOnly || inTech.EntryRetentionDays == nil || *inTech.EntryRetentionDays != 7 {
		t.Fatalf("update feed: %+v err=%v", inTech, err)
	}
	if _, err := store.UpdateFeed(ctx, other.ID, UpdateFeedParams{ID: feed.ID, FeedURL: feed.FeedURL, Title: "x", IntervalMinutes: 45}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign feed update: err=%v", err)
	}
	if _, err := store.UpdateFeed(ctx, owner.ID, UpdateFeedParams{ID: feed.ID, FeedURL: feed.FeedURL, Title: "x", IntervalMinutes: 45, EntryRetentionDays: ptr(0)}); err == nil {
		t.Fatal("retention 0 must be rejected")
	}
	if _, err := store.UpdateFeed(ctx, owner.ID, UpdateFeedParams{ID: feed.ID, FeedURL: feed.FeedURL, Title: "x", IntervalMinutes: 45, CategoryID: ptr(int64(999999))}); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("dangling category: err=%v", err)
	}
	second := newFeedForUser(t, store, owner.ID, "dup", 15)
	if _, err := store.UpdateFeed(ctx, owner.ID, UpdateFeedParams{ID: second.ID, FeedURL: feed.FeedURL, Title: "x", IntervalMinutes: 45}); !errors.Is(err, ErrDuplicateFeedURL) {
		t.Fatalf("duplicate url: err=%v", err)
	}

	byCat, err := store.ListFeedsByCategory(ctx, owner.ID, tech.ID)
	if err != nil || len(byCat) != 1 || byCat[0].ID != feed.ID {
		t.Fatalf("by category: %v err=%v", byCat, err)
	}
	uncat, err := store.ListFeedsByCategory(ctx, owner.ID, 0)
	if err != nil || len(uncat) != 1 || uncat[0].ID != second.ID {
		t.Fatalf("uncategorized: %d err=%v", len(uncat), err)
	}
	counts, err := store.FeedCountsByCategory(ctx, owner.ID)
	if err != nil || counts.Total != 2 || counts.Uncategorized != 1 || counts.ByCategory[tech.ID] != 1 {
		t.Fatalf("counts=%+v err=%v", counts, err)
	}

	if err := store.UpdateFeedIcon(ctx, owner.ID, feed.ID, "https://example.com/i.png", []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateFeedIcon(ctx, other.ID, feed.ID, "x", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign icon: err=%v", err)
	}
	got, _ := store.GetFeed(ctx, owner.ID, feed.ID)
	if got.IconURL != "https://example.com/i.png" || len(got.IconData) != 3 {
		t.Fatalf("icon not stored: %+v", got)
	}

	// Deleting the category detaches its feeds (ON DELETE SET NULL).
	if err := store.DeleteCategory(ctx, other.ID, tech.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign delete: err=%v", err)
	}
	if err := store.DeleteCategory(ctx, owner.ID, tech.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetFeed(ctx, owner.ID, feed.ID)
	if got.CategoryID != nil {
		t.Fatalf("feed still points at deleted category: %+v", got)
	}
	if err := store.DeleteCategory(ctx, owner.ID, tech.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete: err=%v", err)
	}
}

func TestIntegration_BulkUpdateFeedsByCategory(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "bulk")
	other := newIntegrationUser(t, store, "bulk_other")
	cat, _ := store.CreateCategory(ctx, owner.ID, "Bulk", "")
	dest, _ := store.CreateCategory(ctx, owner.ID, "Dest", "")
	foreignCat, _ := store.CreateCategory(ctx, other.ID, "Foreign", "")

	a := newFeedForUser(t, store, owner.ID, "a", 10)
	b := newFeedForUser(t, store, owner.ID, "b", 10)
	loose := newFeedForUser(t, store, owner.ID, "loose", 10)
	for _, f := range []Feed{a, b} {
		if _, err := store.UpdateFeed(ctx, owner.ID, UpdateFeedParams{ID: f.ID, FeedURL: f.FeedURL, Title: f.Title, IntervalMinutes: 10, CategoryID: &cat.ID}); err != nil {
			t.Fatal(err)
		}
	}
	wh, err := store.CreateWebhook(ctx, CreateWebhookParams{UserID: owner.ID, Name: "h", URL: "https://example.com/h", Headers: []byte(`{}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	bad := []BulkFeedUpdate{
		{},
		{IntervalMinutes: ptr(10), StoreHashOnly: ptr(true)},
		{IntervalMinutes: ptr(MaxFeedIntervalMinutes + 1)},
		{IntervalMinutes: ptr(MinFeedIntervalMinutes - 1)},
	}
	for i, u := range bad {
		if _, _, err := store.BulkUpdateFeedsByCategory(ctx, owner.ID, cat.ID, u); err == nil {
			t.Fatalf("bad update %d accepted", i)
		}
	}
	if _, _, err := store.BulkUpdateFeedsByCategory(ctx, owner.ID, foreignCat.ID, BulkFeedUpdate{ManualPaused: ptr(true)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign category: err=%v", err)
	}
	if _, _, err := store.BulkUpdateFeedsByCategory(ctx, owner.ID, cat.ID, BulkFeedUpdate{MoveCategory: true, MoveToCategoryID: &foreignCat.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("move to foreign category: err=%v", err)
	}
	if _, _, err := store.BulkUpdateFeedsByCategory(ctx, owner.ID, cat.ID, BulkFeedUpdate{WebhookSet: true, WebhookID: ptr(int64(999999))}); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("unknown webhook: err=%v", err)
	}

	ids, n, err := store.BulkUpdateFeedsByCategory(ctx, owner.ID, cat.ID, BulkFeedUpdate{IntervalMinutes: ptr(120)})
	if err != nil || n != 2 || len(ids) != 2 {
		t.Fatalf("interval: ids=%v n=%d err=%v", ids, n, err)
	}
	_, n, _ = store.BulkUpdateFeedsByCategory(ctx, owner.ID, cat.ID, BulkFeedUpdate{WebhookSet: true, WebhookID: &wh.ID})
	if n != 2 {
		t.Fatalf("webhook set n=%d", n)
	}
	_, n, _ = store.BulkUpdateFeedsByCategory(ctx, owner.ID, cat.ID, BulkFeedUpdate{StoreHashOnly: ptr(true)})
	if n != 2 {
		t.Fatalf("hash only n=%d", n)
	}
	_, n, _ = store.BulkUpdateFeedsByCategory(ctx, owner.ID, cat.ID, BulkFeedUpdate{ManualPaused: ptr(true)})
	if n != 2 {
		t.Fatalf("pause n=%d", n)
	}
	got, _ := store.GetFeed(ctx, owner.ID, a.ID)
	if got.IntervalMinutes != 120 || got.WebhookID == nil || *got.WebhookID != wh.ID || !got.StoreHashOnly || !got.ManualPaused {
		t.Fatalf("after bulk: %+v", got)
	}
	_, n, _ = store.BulkUpdateFeedsByCategory(ctx, owner.ID, cat.ID, BulkFeedUpdate{WebhookSet: true})
	got, _ = store.GetFeed(ctx, owner.ID, a.ID)
	if n != 2 || got.WebhookID != nil {
		t.Fatalf("webhook clear: n=%d feed=%+v", n, got)
	}

	// Uncategorized (categoryID 0) targets only feeds without a category.
	_, n, err = store.BulkUpdateFeedsByCategory(ctx, owner.ID, 0, BulkFeedUpdate{MoveCategory: true, MoveToCategoryID: &dest.ID})
	if err != nil || n != 1 {
		t.Fatalf("move loose: n=%d err=%v", n, err)
	}
	got, _ = store.GetFeed(ctx, owner.ID, loose.ID)
	if got.CategoryID == nil || *got.CategoryID != dest.ID {
		t.Fatalf("loose feed not moved: %+v", got)
	}
	_, n, _ = store.BulkUpdateFeedsByCategory(ctx, owner.ID, cat.ID, BulkFeedUpdate{MoveCategory: true})
	if n != 2 {
		t.Fatalf("move to uncategorized n=%d", n)
	}
	loose2, _ := store.ListFeedsByCategory(ctx, owner.ID, 0)
	if len(loose2) != 2 {
		t.Fatalf("uncategorized after move: %d", len(loose2))
	}
	// Another user's feeds are never touched even with the same category id space.
	if _, n, err := store.BulkUpdateFeedsByCategory(ctx, other.ID, 0, BulkFeedUpdate{ManualPaused: ptr(true)}); err != nil || n != 0 {
		t.Fatalf("other user n=%d err=%v", n, err)
	}
}

func TestIntegration_SearchFeedsAndListByIDs(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "fsearch")
	other := newIntegrationUser(t, store, "fsearch_other")
	cat, _ := store.CreateCategory(ctx, owner.ID, "S", "")

	mk := func(userID int64, title, url string, catID *int64) Feed {
		t.Helper()
		f, err := store.CreateFeed(ctx, userID, CreateFeedParams{FeedURL: url, FeedType: "rss", Title: title, IntervalMinutes: 60, CategoryID: catID})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	suffix := time.Now().UnixNano()
	golang := mk(owner.ID, "Go Weekly", "https://example.com/gw/"+itoa(suffix), &cat.ID)
	rust := mk(owner.ID, "Rust blog", "https://example.com/rust_lang/"+itoa(suffix), nil)
	pct := mk(owner.ID, "100% cotton", "https://example.com/pct/"+itoa(suffix), nil)
	foreign := mk(other.ID, "Go Weekly (other)", "https://example.com/gw-other/"+itoa(suffix), nil)

	find := func(f SearchFeedsFilter) []int64 {
		t.Helper()
		feeds, err := store.SearchFeeds(ctx, owner.ID, f)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]int64, 0, len(feeds))
		for _, x := range feeds {
			out = append(out, x.ID)
		}
		return out
	}
	if got := find(SearchFeedsFilter{Query: "g"}); len(got) != 0 {
		t.Fatalf("one-rune query must be ignored, got %v", got)
	}
	if got := find(SearchFeedsFilter{Query: " go "}); len(got) != 1 || got[0] != golang.ID {
		t.Fatalf("title match: %v (foreign %d must be excluded)", got, foreign.ID)
	}
	if got := find(SearchFeedsFilter{Query: "rust_lang"}); len(got) != 1 || got[0] != rust.ID {
		t.Fatalf("url match with escaped underscore: %v", got)
	}
	if got := find(SearchFeedsFilter{Query: "0%"}); len(got) != 1 || got[0] != pct.ID {
		t.Fatalf("percent is literal: %v", got)
	}
	if got := find(SearchFeedsFilter{CategoryID: &cat.ID}); len(got) != 1 || got[0] != golang.ID {
		t.Fatalf("browse category: %v", got)
	}
	if got := find(SearchFeedsFilter{CategoryID: ptr(int64(0)), Limit: 1}); len(got) != 1 || got[0] != pct.ID {
		t.Fatalf("uncategorized ordered by title, limit 1: %v", got)
	}
	if got := find(SearchFeedsFilter{Query: "go", CategoryID: ptr(int64(0))}); len(got) != 0 {
		t.Fatalf("query+category intersect: %v", got)
	}

	byIDs, err := store.ListFeedsByIDs(ctx, owner.ID, []int64{rust.ID, golang.ID, rust.ID, foreign.ID, 999999})
	if err != nil || len(byIDs) != 2 || byIDs[0].ID != rust.ID || byIDs[1].ID != golang.ID {
		t.Fatalf("by ids keeps request order, dedups, drops foreign: %v err=%v", byIDs, err)
	}
	if got, err := store.ListFeedsByIDs(ctx, owner.ID, nil); err != nil || got != nil {
		t.Fatalf("empty ids: %v err=%v", got, err)
	}
	if clampFeedSuggestLimit(0) != DefaultFeedSuggestLimit || clampFeedSuggestLimit(MaxFeedSuggestLimit+1) != MaxFeedSuggestLimit || clampFeedSuggestLimit(5) != 5 {
		t.Fatal("clampFeedSuggestLimit")
	}
}

func TestIntegration_UsersSessionsLabelsSettings(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "usr")
	other := newIntegrationUser(t, store, "usr_other")

	total0, err := store.CountUsers(ctx)
	if err != nil || total0 < 2 {
		t.Fatalf("count users=%d err=%v", total0, err)
	}
	users, total, err := store.ListUsers(ctx, 1, 0)
	if err != nil || len(users) != 1 || total != total0 {
		t.Fatalf("list users: n=%d total=%d err=%v", len(users), total, err)
	}
	if _, err := store.CreateUser(ctx, CreateUserParams{Username: owner.Username, PasswordHash: "x"}); !errors.Is(err, ErrDuplicateUsername) {
		t.Fatalf("duplicate username: err=%v", err)
	}
	if _, err := store.CreateUser(ctx, CreateUserParams{Username: "  "}); err == nil {
		t.Fatal("blank username accepted")
	}

	// Password change derives a fresh Fever key from the plain password.
	upd, err := store.UpdateUser(ctx, UpdateUserParams{ID: owner.ID, PasswordHash: ptr("new-hash"), PlainPassword: "pw2"})
	if err != nil || upd.PasswordHash != "new-hash" || upd.FeverAPIKey != feverAPIKey(owner.Username, "pw2") {
		t.Fatalf("update user: %+v err=%v", upd, err)
	}
	byKey, err := store.GetUserByFeverAPIKey(ctx, " "+upd.FeverAPIKey+" ")
	if err != nil || byKey.ID != owner.ID {
		t.Fatalf("by fever key: %+v err=%v", byKey, err)
	}
	if _, err := store.GetUserByFeverAPIKey(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty key: err=%v", err)
	}
	if _, err := store.GetUserByFeverAPIKey(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown key: err=%v", err)
	}
	same, err := store.UpdateUser(ctx, UpdateUserParams{ID: owner.ID})
	if err != nil || same.PasswordHash != "new-hash" {
		t.Fatalf("no-op update: %+v err=%v", same, err)
	}
	if _, err := store.UpdateUser(ctx, UpdateUserParams{ID: 999999, PasswordHash: ptr("h")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user update: err=%v", err)
	}
	if _, err := store.UpdateUser(ctx, UpdateUserParams{ID: 999999, PasswordHash: ptr("h"), PlainPassword: "p"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user update with plain pw: err=%v", err)
	}

	exp := time.Now().Add(time.Hour)
	s1, err := store.CreateSession(ctx, owner.ID, "sess-1-"+itoa(time.Now().UnixNano()), exp)
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := store.CreateSession(ctx, owner.ID, "sess-2-"+itoa(time.Now().UnixNano()), exp)
	s3, _ := store.CreateSession(ctx, other.ID, "sess-3-"+itoa(time.Now().UnixNano()), exp)
	if err := store.DeleteUserSessionsExcept(ctx, owner.ID, s1.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupSession(ctx, s2.SessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("s2 should be gone: err=%v", err)
	}
	if _, err := store.LookupSession(ctx, s1.SessionID); err != nil {
		t.Fatalf("s1 must survive: %v", err)
	}
	if err := store.DeleteSession(ctx, s1.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupSession(ctx, s1.SessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("s1 after delete: err=%v", err)
	}
	if err := store.DeleteUserSessions(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupSession(ctx, s3.SessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("s3 after user-wide delete: err=%v", err)
	}

	lbl, err := store.CreateLabel(ctx, CreateLabelParams{UserID: owner.ID, Caption: "Important", FgColor: "#fff", BgColor: "#c00"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateLabel(ctx, CreateLabelParams{UserID: owner.ID, Caption: "Zed"}); err != nil {
		t.Fatal(err)
	}
	labels, total, err := store.ListLabels(ctx, owner.ID, 1, 0)
	if err != nil || len(labels) != 1 || total != 2 || labels[0].ID != lbl.ID {
		t.Fatalf("list labels: n=%d total=%d first=%+v err=%v", len(labels), total, labels, err)
	}
	if _, err := store.GetLabel(ctx, other.ID, lbl.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign label: err=%v", err)
	}
	got, err := store.UpdateLabel(ctx, UpdateLabelParams{ID: lbl.ID, UserID: owner.ID, Caption: "Urgent", FgColor: "#000", BgColor: "#ff0"})
	if err != nil || got.Caption != "Urgent" || got.BgColor != "#ff0" {
		t.Fatalf("update label: %+v err=%v", got, err)
	}
	if _, err := store.UpdateLabel(ctx, UpdateLabelParams{ID: lbl.ID, UserID: other.ID, Caption: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign label update: err=%v", err)
	}
	fetched, err := store.GetLabel(ctx, owner.ID, lbl.ID)
	if err != nil || fetched.Caption != "Urgent" {
		t.Fatalf("get label: %+v err=%v", fetched, err)
	}

	key := "it_setting_" + itoa(time.Now().UnixNano())
	if _, err := store.GetAppSetting(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing setting: err=%v", err)
	}
	if err := store.SetAppSetting(ctx, key, json.RawMessage(`{"date":"2026-09-11"}`)); err != nil {
		t.Fatal(err)
	}
	raw, err := store.GetAppSetting(ctx, key)
	if err != nil || string(raw) != `{"date": "2026-09-11"}` {
		t.Fatalf("get setting: %s err=%v", raw, err)
	}
	if err := store.SetAppSetting(ctx, key, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ = store.GetAppSetting(ctx, key)
	if string(raw) != `{}` {
		t.Fatalf("empty value must be stored as {}: %s", raw)
	}
	t.Cleanup(func() { _, _ = store.db.Exec(context.Background(), `DELETE FROM app_settings WHERE key = $1`, key) })
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
