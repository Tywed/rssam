//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"rssam/internal/auth"
)

func TestIntegration_ReorderCategories(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	hash, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(ctx, CreateUserParams{
		Username:     "cat_reorder_" + suffix,
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), u.ID) })

	c1, err := store.CreateCategory(ctx, u.ID, "Alpha", "")
	if err != nil {
		t.Fatalf("create c1: %v", err)
	}
	c2, err := store.CreateCategory(ctx, u.ID, "Beta", "")
	if err != nil {
		t.Fatalf("create c2: %v", err)
	}
	c3, err := store.CreateCategory(ctx, u.ID, "Gamma", "")
	if err != nil {
		t.Fatalf("create c3: %v", err)
	}

	if err := store.ReorderCategories(ctx, u.ID, []int64{c3.ID, c1.ID, c2.ID}); err != nil {
		t.Fatalf("reorder: %v", err)
	}

	cats, _, err := store.ListCategories(ctx, u.ID, 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(cats) != 3 {
		t.Fatalf("expected 3 categories, got %d", len(cats))
	}
	if cats[0].ID != c3.ID || cats[1].ID != c1.ID || cats[2].ID != c2.ID {
		t.Fatalf("unexpected order: %+v", cats)
	}
	if cats[0].SortOrder != 0 || cats[2].SortOrder != 2 {
		t.Fatalf("unexpected sort_order: %+v", cats)
	}

	if err := store.ReorderCategories(ctx, u.ID, []int64{c1.ID, c2.ID}); err != ErrInvalidReference {
		t.Fatalf("partial reorder: got %v want ErrInvalidReference", err)
	}
}
