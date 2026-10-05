//go:build integration

package storage

import (
	"context"
	"errors"
	"testing"
)

// Following a collection subscribes to every feed of the set into one
// category (created from the collection title when none is given), feeds
// added later reach followers, removed feeds and unfollowing take only the
// subscriptions the follow created, and a feed listed by a collection
// stays in the catalog without subscribers.
func TestIntegration_Collections_FollowSyncUnfollow(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	editor := newIntegrationUser(t, store, "col_editor")
	bob := newIntegrationUser(t, store, "col_bob")
	f1, _ := newIntegrationFeedWithEntries(t, store, editor.ID, SubscribeUnreadBackfill+5)
	f2, _ := newIntegrationFeedWithEntries(t, store, editor.ID, 2)
	f3, _ := newIntegrationFeedWithEntries(t, store, editor.ID, 1)
	manual, _ := newIntegrationFeedWithEntries(t, store, editor.ID, 1)

	col, err := store.CreateCollection(ctx, editor.ID, CollectionParams{Title: "Новости", Description: "d"})
	if err != nil || col.OwnerID != editor.ID || col.OwnerName != editor.Username || col.Title != "Новости" || col.FeedCount != 0 || col.Followed {
		t.Fatalf("create: %+v err=%v", col, err)
	}
	if n, err := store.AddCollectionFeeds(ctx, col.ID, []int64{f1.ID, f2.ID, manual.ID, f1.ID}); err != nil || n != 3 {
		t.Fatalf("add feeds: n=%d err=%v", n, err)
	}
	if _, err := store.AddCollectionFeeds(ctx, col.ID, []int64{999999}); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("add unknown feed: %v", err)
	}
	if _, err := store.AddCollectionFeeds(ctx, 999999, []int64{f1.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("add to unknown collection: %v", err)
	}

	// Bob already reads `manual` by hand; the follow must leave that row alone.
	if _, err := store.Subscribe(ctx, bob.ID, manual.ID, SubscriptionParams{}); err != nil {
		t.Fatal(err)
	}
	res, err := store.FollowCollection(ctx, bob.ID, col.ID, nil)
	if err != nil || res.Subscribed != 2 || res.CategoryID == 0 {
		t.Fatalf("follow: %+v err=%v", res, err)
	}
	if _, err := store.FollowCollection(ctx, bob.ID, col.ID, nil); !errors.Is(err, ErrAlreadyFollowing) {
		t.Fatalf("follow twice: %v", err)
	}
	if _, err := store.FollowCollection(ctx, bob.ID, 999999, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("follow unknown: %v", err)
	}
	var catTitle string
	if err := store.db.QueryRow(ctx, `SELECT title FROM categories WHERE id = $1 AND user_id = $2`, res.CategoryID, bob.ID).Scan(&catTitle); err != nil || catTitle != "Новости" {
		t.Fatalf("follow category: %q err=%v", catTitle, err)
	}
	got, err := store.GetCollection(ctx, bob.ID, col.ID)
	if err != nil || !got.Followed || got.CategoryID == nil || *got.CategoryID != res.CategoryID || got.FeedCount != 3 || got.FollowerCount != 1 {
		t.Fatalf("bob's view: %+v err=%v", got, err)
	}
	if n, _ := store.CountUnreadByCategory(ctx, bob.ID, res.CategoryID); n != SubscribeUnreadBackfill+2 {
		t.Fatalf("unread in follow category = %d", n)
	}
	if f, _ := store.GetFeed(ctx, bob.ID, manual.ID); f.CategoryID != nil {
		t.Fatalf("manual subscription moved into the collection category: %+v", f)
	}
	feeds, err := store.ListCollectionFeeds(ctx, bob.ID, col.ID)
	if err != nil || len(feeds) != 3 || !feeds[0].Subscribed {
		t.Fatalf("collection feeds: %v err=%v", feeds, err)
	}
	list, err := store.ListCollections(ctx, editor.ID)
	if err != nil || len(list) != 1 || list[0].Followed || list[0].FollowerCount != 1 {
		t.Fatalf("editor list: %+v err=%v", list, err)
	}

	// A feed added later reaches the follower in the follow category.
	if n, err := store.AddCollectionFeeds(ctx, col.ID, []int64{f3.ID}); err != nil || n != 1 {
		t.Fatalf("add later: n=%d err=%v", n, err)
	}
	if f, err := store.GetFeed(ctx, bob.ID, f3.ID); err != nil || f.CategoryID == nil || *f.CategoryID != res.CategoryID {
		t.Fatalf("later feed for bob: %+v err=%v", f, err)
	}

	// Removing a feed from the set unsubscribes the follower; the editor
	// still reads it so the catalog row stays.
	if err := store.RemoveCollectionFeed(ctx, col.ID, f3.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveCollectionFeed(ctx, col.ID, f3.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove twice: %v", err)
	}
	if _, err := store.GetFeed(ctx, bob.ID, f3.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob keeps removed feed: %v", err)
	}
	if _, err := store.GetFeedByID(ctx, f3.ID); err != nil {
		t.Fatalf("catalog row gone with the editor still subscribed: %v", err)
	}

	// A collection feed outlives its last subscriber.
	if err := store.Unsubscribe(ctx, editor.ID, f2.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Unsubscribe(ctx, bob.ID, f2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetFeedByID(ctx, f2.ID); err != nil {
		t.Fatalf("collection feed dropped as orphan: %v", err)
	}
	if n, err := store.UnfollowCollection(ctx, bob.ID, col.ID); err != nil || n != 1 {
		t.Fatalf("unfollow: n=%d err=%v", n, err)
	}
	if _, err := store.UnfollowCollection(ctx, bob.ID, col.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unfollow twice: %v", err)
	}
	if _, err := store.GetFeed(ctx, bob.ID, f1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unfollow kept f1: %v", err)
	}
	if f, err := store.GetFeed(ctx, bob.ID, manual.ID); err != nil || f.CategoryID != nil {
		t.Fatalf("unfollow touched the manual subscription: %+v err=%v", f, err)
	}
	if got, _ := store.GetCollection(ctx, bob.ID, col.ID); got.Followed || got.FollowerCount != 0 {
		t.Fatalf("after unfollow: %+v", got)
	}
	if n, _ := store.CountCollections(ctx); n != 1 {
		t.Fatalf("count = %d", n)
	}
}

// Two collections sharing a feed: leaving one keeps the subscription as
// long as the other is followed; deleting a collection detaches rather
// than removes the subscriptions it created; the follow category can be
// chosen and must belong to the follower.
func TestIntegration_Collections_OverlapAndDelete(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	editor := newIntegrationUser(t, store, "col2_editor")
	bob := newIntegrationUser(t, store, "col2_bob")
	shared, _ := newIntegrationFeedWithEntries(t, store, editor.ID, 1)
	onlyA, _ := newIntegrationFeedWithEntries(t, store, editor.ID, 1)
	editorCat, _ := store.CreateCategory(ctx, editor.ID, "E", "")
	bobCat, _ := store.CreateCategory(ctx, bob.ID, "B", "")

	a, _ := store.CreateCollection(ctx, editor.ID, CollectionParams{Title: "A"})
	b, _ := store.CreateCollection(ctx, editor.ID, CollectionParams{Title: "B"})
	if _, err := store.AddCollectionFeeds(ctx, a.ID, []int64{shared.ID, onlyA.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddCollectionFeeds(ctx, b.ID, []int64{shared.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FollowCollection(ctx, bob.ID, a.ID, &editorCat.ID); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("follow into a foreign category: %v", err)
	}
	if res, err := store.FollowCollection(ctx, bob.ID, a.ID, &bobCat.ID); err != nil || res.Subscribed != 2 || res.CategoryID != bobCat.ID {
		t.Fatalf("follow a: %+v err=%v", res, err)
	}
	if res, err := store.FollowCollection(ctx, bob.ID, b.ID, nil); err != nil || res.Subscribed != 0 {
		t.Fatalf("follow b: %+v err=%v", res, err)
	}
	if n, err := store.UnfollowCollection(ctx, bob.ID, a.ID); err != nil || n != 1 {
		t.Fatalf("unfollow a: n=%d err=%v", n, err)
	}
	if _, err := store.GetFeed(ctx, bob.ID, shared.ID); err != nil {
		t.Fatalf("shared feed lost although b is still followed: %v", err)
	}
	if _, err := store.GetFeed(ctx, bob.ID, onlyA.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("onlyA survived unfollow: %v", err)
	}

	if err := store.UpdateCollection(ctx, b.ID, CollectionParams{Title: "B2", Description: "x"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetCollection(ctx, bob.ID, b.ID); got.Title != "B2" || got.Description != "x" {
		t.Fatalf("update: %+v", got)
	}
	if err := store.UpdateCollection(ctx, 999999, CollectionParams{Title: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update unknown: %v", err)
	}
	if err := store.DeleteCollection(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteCollection(ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if _, err := store.GetFeed(ctx, bob.ID, shared.ID); err != nil {
		t.Fatalf("deleting the collection removed the subscription: %v", err)
	}
	var detached int
	_ = store.db.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE user_id = $1 AND collection_id IS NULL`, bob.ID).Scan(&detached)
	if detached != 1 {
		t.Fatalf("detached subscriptions = %d", detached)
	}
	// Owner deletion keeps the collection; its feeds are not orphans.
	if err := store.Unsubscribe(ctx, editor.ID, shared.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteUser(ctx, editor.ID); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetCollection(ctx, bob.ID, a.ID)
	if err != nil || got.OwnerID != 0 || got.OwnerName != "" || got.FeedCount != 2 {
		t.Fatalf("collection after owner deletion: %+v err=%v", got, err)
	}
	if _, err := store.GetFeedByID(ctx, onlyA.ID); err != nil {
		t.Fatalf("collection feed dropped with its last subscriber: %v", err)
	}
}
