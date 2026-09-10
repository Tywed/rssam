package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Audit actions. Keep the list short: every entry is a row per human action.
const (
	AuditUserCreate       = "user.create"
	AuditUserDelete       = "user.delete"
	AuditPasswordChange   = "user.password_change"
	AuditLogoutOthers     = "session.logout_others"
	AuditAPIKeyCreate     = "api_key.create"
	AuditAPIKeyDelete     = "api_key.delete"
	AuditEnvUpdate        = "env.update"
	AuditBridgeUpdate     = "bridge.update"
	AuditWorkersPause     = "workers.pause"
	AuditWorkersResume    = "workers.resume"
	AuditServiceRestart   = "service.restart"
	AuditServiceUpdate    = "service.update"
	AuditFeedsRefreshAll  = "feeds.refresh_all"
	AuditEntriesCollapse  = "entries.collapse"
	AuditRetentionCleanup = "retention.cleanup"
)

// AuditActions lists every action in display order (filter dropdown).
var AuditActions = []string{
	AuditUserCreate, AuditUserDelete, AuditPasswordChange, AuditLogoutOthers,
	AuditAPIKeyCreate, AuditAPIKeyDelete,
	AuditEnvUpdate, AuditBridgeUpdate,
	AuditWorkersPause, AuditWorkersResume, AuditServiceRestart, AuditServiceUpdate,
	AuditFeedsRefreshAll, AuditEntriesCollapse, AuditRetentionCleanup,
}

type AuditEvent struct {
	ActorID    int64
	ActorName  string
	IP         string
	Action     string
	TargetType string
	TargetID   int64
	Details    map[string]any
}

type AuditEntry struct {
	ID         int64
	At         time.Time
	ActorID    *int64
	ActorName  string
	IP         string
	Action     string
	TargetType string
	TargetID   *int64
	Details    map[string]any
}

type AuditListParams struct {
	Actor  string // exact actor_name match, "" = any
	Action string // exact action, "" = any
	Limit  int
	Offset int
}

type AuditLogStore interface {
	RecordAudit(ctx context.Context, ev AuditEvent) error
	ListAuditLog(ctx context.Context, p AuditListParams) ([]AuditEntry, int, error)
	CountAuditLog(ctx context.Context) (int, error)
}

func (s *PostgresStore) RecordAudit(ctx context.Context, ev AuditEvent) error {
	if strings.TrimSpace(ev.Action) == "" {
		return fmt.Errorf("record audit: action is required")
	}
	details := []byte("{}")
	if len(ev.Details) > 0 {
		b, err := json.Marshal(ev.Details)
		if err != nil {
			return fmt.Errorf("record audit: encode details: %w", err)
		}
		details = b
	}
	var actorID *int64
	if ev.ActorID > 0 {
		actorID = &ev.ActorID
	}
	var targetID *int64
	if ev.TargetID > 0 {
		targetID = &ev.TargetID
	}
	_, err := s.db.Exec(ctx, `
INSERT INTO audit_log(actor_id, actor_name, ip, action, target_type, target_id, details)
VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		actorID, ev.ActorName, ev.IP, ev.Action, ev.TargetType, targetID, details)
	if err != nil {
		return fmt.Errorf("record audit: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListAuditLog(ctx context.Context, p AuditListParams) ([]AuditEntry, int, error) {
	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset := max(p.Offset, 0)
	var total int
	if err := s.db.QueryRow(ctx, `
SELECT count(*) FROM audit_log
WHERE ($1 = '' OR actor_name = $1) AND ($2 = '' OR action = $2)`, p.Actor, p.Action).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit log: %w", err)
	}
	rows, err := s.db.Query(ctx, `
SELECT id, at, actor_id, actor_name, ip, action, target_type, target_id, details
FROM audit_log
WHERE ($1 = '' OR actor_name = $1) AND ($2 = '' OR action = $2)
ORDER BY id DESC
LIMIT $3 OFFSET $4`, p.Actor, p.Action, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list audit log: %w", err)
	}
	defer rows.Close()
	out := make([]AuditEntry, 0, limit)
	for rows.Next() {
		var e AuditEntry
		var details []byte
		if err := rows.Scan(&e.ID, &e.At, &e.ActorID, &e.ActorName, &e.IP, &e.Action, &e.TargetType, &e.TargetID, &details); err != nil {
			return nil, 0, fmt.Errorf("scan audit log: %w", err)
		}
		if len(details) > 0 {
			_ = json.Unmarshal(details, &e.Details)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate audit log: %w", err)
	}
	return out, total, nil
}

func (s *PostgresStore) CountAuditLog(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count audit log: %w", err)
	}
	return n, nil
}
