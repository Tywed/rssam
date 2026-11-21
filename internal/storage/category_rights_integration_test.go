//go:build integration

package storage

import (
	"context"
	"errors"
	"testing"
)

func TestIntegration_CategoryRights(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	author := newIntegrationUser(t, store, "cat_author")
	other := newIntegrationUser(t, store, "cat_other")

	cat, err := store.CreateCategory(ctx, author.ID, "Shared", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if cat.OwnerID != author.ID {
		t.Fatalf("owner = %d, want %d", cat.OwnerID, author.ID)
	}
	orphan, err := store.CreateCategory(ctx, 0, "Orphan", "")
	if err != nil || orphan.OwnerID != 0 {
		t.Fatalf("orphan: %+v %v", orphan, err)
	}

	// Rename: author yes, other editor no, admin yes, orphan admin only.
	if _, err := store.UpdateCategory(ctx, UpdateCategoryParams{ID: cat.ID, UserID: author.ID, Title: "Shared2"}); err != nil {
		t.Fatalf("author rename: %v", err)
	}
	if _, err := store.UpdateCategory(ctx, UpdateCategoryParams{ID: cat.ID, UserID: other.ID, Title: "Hijack"}); !errors.Is(err, ErrNotCategoryOwner) {
		t.Fatalf("other rename: err=%v want ErrNotCategoryOwner", err)
	}
	if _, err := store.UpdateCategory(ctx, UpdateCategoryParams{ID: cat.ID, UserID: other.ID, Admin: true, Title: "Admin"}); err != nil {
		t.Fatalf("admin rename: %v", err)
	}
	if _, err := store.UpdateCategory(ctx, UpdateCategoryParams{ID: orphan.ID, UserID: author.ID, Title: "x"}); !errors.Is(err, ErrNotCategoryOwner) {
		t.Fatalf("orphan rename by editor: err=%v", err)
	}
	if _, err := store.UpdateCategory(ctx, UpdateCategoryParams{ID: cat.ID + 100000, UserID: author.ID, Admin: true, Title: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: err=%v want ErrNotFound", err)
	}

	// Delete: blocked for an editor while someone else follows it.
	if _, err := store.FollowCategory(ctx, other.ID, cat.ID); err != nil {
		t.Fatalf("follow: %v", err)
	}
	counts, err := store.CountOtherCategoryFollowers(ctx, author.ID)
	if err != nil || counts[cat.ID] != 1 {
		t.Fatalf("other followers for author: %v %v", counts, err)
	}
	if counts, _ := store.CountOtherCategoryFollowers(ctx, other.ID); counts[cat.ID] != 0 {
		t.Fatalf("other followers for the follower himself: %v", counts)
	}
	var inUse *CategoryInUseError
	if err := store.DeleteCategory(ctx, author.ID, cat.ID); !errors.As(err, &inUse) || inUse.Followers != 1 {
		t.Fatalf("delete followed: err=%v", err)
	}
	if err := store.DeleteCategory(ctx, author.ID, cat.ID+100000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: err=%v", err)
	}
	if _, err := store.UnfollowCategory(ctx, other.ID, cat.ID); err != nil {
		t.Fatalf("unfollow: %v", err)
	}
	// The follower himself does not count: a lone follower may delete.
	if _, err := store.FollowCategory(ctx, other.ID, cat.ID); err != nil {
		t.Fatalf("follow again: %v", err)
	}
	if err := store.DeleteCategory(ctx, other.ID, cat.ID); err != nil {
		t.Fatalf("delete by lone follower: %v", err)
	}
	if err := store.DeleteCategoryByID(ctx, orphan.ID); err != nil {
		t.Fatalf("admin delete orphan: %v", err)
	}
}
