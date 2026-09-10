//go:build integration

package storage

import (
	"context"
	"testing"
	"time"
)

func TestIntegration_AuditLogRecordListRetention(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	actor := newIntegrationUser(t, store, "audit_actor")
	tag := actor.Username

	events := []AuditEvent{
		{ActorID: actor.ID, ActorName: tag, IP: "10.0.0.1", Action: AuditUserCreate, TargetType: "user", TargetID: 42, Details: map[string]any{"username": "x"}},
		{ActorID: actor.ID, ActorName: tag, IP: "10.0.0.1", Action: AuditAPIKeyCreate, TargetType: "api_key", TargetID: 7},
		{ActorName: tag + "_other", Action: AuditServiceRestart},
	}
	for _, ev := range events {
		if err := store.RecordAudit(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RecordAudit(ctx, AuditEvent{ActorName: tag}); err == nil {
		t.Fatal("empty action must be rejected")
	}

	rows, total, err := store.ListAuditLog(ctx, AuditListParams{Actor: tag})
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("list by actor: total=%d rows=%d err=%v", total, len(rows), err)
	}
	// Newest first; details, target and nullable columns round-trip.
	if rows[0].Action != AuditAPIKeyCreate || rows[1].Action != AuditUserCreate {
		t.Fatalf("order: %s, %s", rows[0].Action, rows[1].Action)
	}
	if rows[1].Details["username"] != "x" || rows[1].TargetID == nil || *rows[1].TargetID != 42 || rows[1].ActorID == nil || *rows[1].ActorID != actor.ID || rows[1].IP != "10.0.0.1" {
		t.Fatalf("roundtrip: %+v", rows[1])
	}
	if len(rows[0].Details) != 0 {
		t.Fatalf("empty details must decode empty, got %v", rows[0].Details)
	}
	if _, total, _ = store.ListAuditLog(ctx, AuditListParams{Actor: tag, Action: AuditUserCreate}); total != 1 {
		t.Fatalf("list by actor+action: total=%d", total)
	}
	other, _, _ := store.ListAuditLog(ctx, AuditListParams{Actor: tag + "_other"})
	if len(other) != 1 || other[0].ActorID != nil {
		t.Fatalf("anonymous actor: %+v", other)
	}

	// Deleting the actor keeps the history with actor_id nulled.
	if err := store.DeleteUser(ctx, actor.ID); err != nil {
		t.Fatal(err)
	}
	rows, _, _ = store.ListAuditLog(ctx, AuditListParams{Actor: tag})
	if len(rows) != 2 || rows[0].ActorID != nil || rows[0].ActorName != tag {
		t.Fatalf("after user delete: %+v", rows)
	}

	// Retention deletes by age; rows newer than the cutoff stay.
	if _, err := store.db.Exec(ctx, `UPDATE audit_log SET at = now() - interval '400 days' WHERE actor_name = $1`, tag); err != nil {
		t.Fatal(err)
	}
	res, err := store.RunRetentionCleanup(ctx, RetentionCleanupOpts{
		RemovedEntriesBefore: time.Now().Add(-100 * 365 * 24 * time.Hour),
		WebhookLogsBefore:    time.Now().Add(-100 * 365 * 24 * time.Hour),
		AuditLogBefore:       RetentionCutoff(time.Now(), 180),
	})
	if err != nil || res.AuditLog < 2 {
		t.Fatalf("cleanup: %+v err=%v", res, err)
	}
	if _, total, _ = store.ListAuditLog(ctx, AuditListParams{Actor: tag}); total != 0 {
		t.Fatalf("old rows survived: %d", total)
	}
	if _, total, _ = store.ListAuditLog(ctx, AuditListParams{Actor: tag + "_other"}); total != 1 {
		t.Fatalf("fresh row deleted")
	}
	_, _ = store.db.Exec(ctx, `DELETE FROM audit_log WHERE actor_name = $1`, tag+"_other")
}
