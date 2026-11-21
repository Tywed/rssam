//go:build integration

package storage

import (
	"context"
	"errors"
	"testing"
)

func TestIntegration_FilterActionRefsOwnership(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "far_owner")
	other := newIntegrationUser(t, store, "far_other")
	_, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 1)

	wh, err := store.CreateWebhook(ctx, CreateWebhookParams{
		UserID: owner.ID, Name: "hook", URL: "https://example.com/hook", Method: "POST",
		Headers: []byte(`{}`), Enabled: true, Kind: WebhookKindHTTP,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	label, err := store.CreateLabel(ctx, CreateLabelParams{UserID: owner.ID, Caption: "mine"})
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	mk := func(userID int64, actionType string, param string) error {
		_, err := store.CreateFilter(ctx, CreateFilterParams{
			UserID: userID, Name: "f", Enabled: true,
			Rules:   []CreateFilterRuleParams{{Field: "title", Pattern: "x", Op: "regex"}},
			Actions: []CreateFilterActionParams{{ActionType: actionType, ActionParam: param}},
		})
		return err
	}

	for _, tc := range []struct {
		name   string
		user   int64
		typ    string
		param  string
		wantOK bool
	}{
		{"own webhook", owner.ID, FilterActionWebhook, itoa(wh.ID), true},
		{"own label", owner.ID, FilterActionLabel, itoa(label.ID), true},
		{"delete has no ref", other.ID, FilterActionDelete, "", true},
		{"foreign webhook", other.ID, FilterActionWebhook, itoa(wh.ID), false},
		{"foreign label", other.ID, FilterActionLabel, itoa(label.ID), false},
		{"garbage id", owner.ID, FilterActionWebhook, "abc", false},
		{"missing id", owner.ID, FilterActionWebhook, "999999999", false},
	} {
		err := mk(tc.user, tc.typ, tc.param)
		if tc.wantOK && err != nil {
			t.Fatalf("%s: unexpected error %v", tc.name, err)
		}
		if !tc.wantOK && !errors.Is(err, ErrInvalidReference) {
			t.Fatalf("%s: err=%v want ErrInvalidReference", tc.name, err)
		}
	}

	f, err := store.CreateFilter(ctx, CreateFilterParams{UserID: other.ID, Name: "upd", Enabled: true,
		Rules: []CreateFilterRuleParams{{Field: "title", Pattern: "x", Op: "regex"}}})
	if err != nil {
		t.Fatalf("create filter for update: %v", err)
	}
	_, err = store.UpdateFilter(ctx, UpdateFilterParams{ID: f.ID, UserID: other.ID, Name: "upd", Enabled: true,
		Rules:   []CreateFilterRuleParams{{Field: "title", Pattern: "x", Op: "regex"}},
		Actions: []CreateFilterActionParams{{ActionType: FilterActionWebhook, ActionParam: itoa(wh.ID)}}})
	if !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("update with foreign webhook: err=%v want ErrInvalidReference", err)
	}
	got, err := store.GetFilter(ctx, other.ID, f.ID)
	if err != nil {
		t.Fatalf("get filter after failed update: %v", err)
	}
	if len(got.Actions) != 0 {
		t.Fatalf("failed update leaked actions: %+v", got.Actions)
	}

	// Runtime guard for rows that predate the save-time check.
	if err := store.EnqueueOwnedWebhookLog(ctx, other.ID, wh.ID, entries[0].ID); err != nil {
		t.Fatalf("enqueue owned (foreign): %v", err)
	}
	if _, total, err := store.ListWebhookLogs(ctx, wh.ID, 10, 0); err != nil || total != 0 {
		t.Fatalf("foreign enqueue: total=%d err=%v want 0", total, err)
	}
	if err := store.EnqueueOwnedWebhookLog(ctx, owner.ID, wh.ID, entries[0].ID); err != nil {
		t.Fatalf("enqueue owned: %v", err)
	}
	if _, total, err := store.ListWebhookLogs(ctx, wh.ID, 10, 0); err != nil || total != 1 {
		t.Fatalf("own enqueue: total=%d err=%v want 1", total, err)
	}
}
