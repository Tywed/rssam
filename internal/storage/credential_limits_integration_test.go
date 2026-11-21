//go:build integration

package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestIntegration_CredentialLimits(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	u := newIntegrationUser(t, store, "limits")

	for i := range MaxAPIKeysPerUser {
		if _, err := store.CreateAPIKey(ctx, CreateAPIKeyParams{UserID: u.ID, Name: "k", TokenHash: fmt.Sprintf("h-%d-%d", u.ID, i)}); err != nil {
			t.Fatalf("key %d: %v", i, err)
		}
	}
	_, err := store.CreateAPIKey(ctx, CreateAPIKeyParams{UserID: u.ID, Name: "k", TokenHash: fmt.Sprintf("h-%d-x", u.ID)})
	if !errors.Is(err, ErrTooManyAPIKeys) {
		t.Fatalf("key %d: err=%v want ErrTooManyAPIKeys", MaxAPIKeysPerUser+1, err)
	}
	keys, err := store.ListAPIKeys(ctx, u.ID)
	if err != nil || len(keys) != MaxAPIKeysPerUser {
		t.Fatalf("keys=%d err=%v", len(keys), err)
	}
	if err := store.DeleteAPIKey(ctx, u.ID, keys[0].ID); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, CreateAPIKeyParams{UserID: u.ID, Name: "k", TokenHash: fmt.Sprintf("h-%d-y", u.ID)}); err != nil {
		t.Fatalf("key after delete: %v", err)
	}

	now := time.Now()
	first := fmt.Sprintf("s-%d-0", u.ID)
	for i := range MaxSessionsPerUser {
		// Ascending expiry: the first session is the one to go.
		if _, err := store.CreateSession(ctx, u.ID, fmt.Sprintf("s-%d-%d", u.ID, i), now.Add(time.Hour+time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("session %d: %v", i, err)
		}
	}
	if _, err := store.LookupSession(ctx, first); err != nil {
		t.Fatalf("session 0 must still exist at the limit: %v", err)
	}
	if _, err := store.CreateSession(ctx, u.ID, fmt.Sprintf("s-%d-new", u.ID), now.Add(2*time.Hour)); err != nil {
		t.Fatalf("session %d: %v", MaxSessionsPerUser+1, err)
	}
	if _, err := store.LookupSession(ctx, first); !errors.Is(err, ErrNotFound) {
		t.Fatalf("soonest-expiring session must be evicted: err=%v", err)
	}
	if _, err := store.LookupSession(ctx, fmt.Sprintf("s-%d-1", u.ID)); err != nil {
		t.Fatalf("second session must survive: %v", err)
	}
	var n int
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1`, u.ID).Scan(&n); err != nil || n != MaxSessionsPerUser {
		t.Fatalf("sessions=%d err=%v want %d", n, err, MaxSessionsPerUser)
	}
}
