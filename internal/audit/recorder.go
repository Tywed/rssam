// Package audit writes the administrative audit trail. Recording is best
// effort: a failed insert is logged and never fails the action itself.
package audit

import (
	"context"
	"log/slog"
	"net/http"

	"rssam/internal/auth"
	"rssam/internal/http/middleware"
	"rssam/internal/storage"
)

type Recorder struct {
	Store storage.AuditLogStore
	Log   *slog.Logger
}

// Record writes one event on behalf of the request's principal. A nil
// Recorder or nil Store is a no-op so callers need no guards.
func (rec *Recorder) Record(r *http.Request, action, targetType string, targetID int64, details map[string]any) {
	if rec == nil || rec.Store == nil {
		return
	}
	ev := storage.AuditEvent{
		IP:         middleware.ClientIP(r),
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Details:    details,
	}
	if p, ok := auth.PrincipalFromContext(r.Context()); ok {
		ev.ActorID = p.UserID
		ev.ActorName = p.Username
	}
	// The request context may already be cancelled by a redirecting client;
	// the row must still land.
	if err := rec.Store.RecordAudit(context.WithoutCancel(r.Context()), ev); err != nil {
		log := rec.Log
		if log == nil {
			log = slog.Default()
		}
		log.Error("audit record failed", "action", action, "actor_id", ev.ActorID, "err", err)
	}
}
