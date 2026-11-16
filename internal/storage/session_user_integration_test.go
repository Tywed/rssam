//go:build integration

package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestLookupSessionUser_Integration(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	u, err := store.CreateUser(ctx, CreateUserParams{Username: "su_" + suffix, PasswordHash: "x", Role: "editor"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(ctx, u.ID) })
	if _, err := store.CreateSession(ctx, u.ID, "su-live-"+suffix, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSession(ctx, u.ID, "su-dead-"+suffix, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	sess, got, err := LookupSessionUser(ctx, store, store, "su-live-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	if sess.UserID != u.ID || got.ID != u.ID || got.Username != u.Username || got.Role != "editor" || got.PasswordHash != "x" {
		t.Fatalf("joined row mismatch: sess=%+v user=%+v", sess, got)
	}
	if _, _, err := LookupSessionUser(ctx, store, store, "su-dead-"+suffix); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session: err=%v", err)
	}
	if _, _, err := LookupSessionUser(ctx, store, store, "nope-"+suffix); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session: err=%v", err)
	}
}
