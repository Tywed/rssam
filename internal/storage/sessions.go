package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const DefaultSessionTTL = 30 * 24 * time.Hour

// SessionTouchInterval bounds how often a sliding-expiry session is written
// back: a session is refreshed only when it was last touched more than this
// long ago (expires_at < now + TTL - interval). With a 30-day TTL an hour of
// slack is invisible to users and avoids an UPDATE on every request.
const SessionTouchInterval = time.Hour

// SessionNeedsTouch reports whether a session whose current expiry is
// expiresAt should be refreshed at now.
func SessionNeedsTouch(expiresAt, now time.Time) bool {
	return expiresAt.Before(now.Add(DefaultSessionTTL - SessionTouchInterval))
}

type Session struct {
	ID        int64
	UserID    int64
	SessionID string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type SessionStore interface {
	CreateSession(ctx context.Context, userID int64, sessionID string, expiresAt time.Time) (Session, error)
	LookupSession(ctx context.Context, sessionID string) (Session, error)
	TouchSession(ctx context.Context, sessionID string, expiresAt time.Time) error
	DeleteSession(ctx context.Context, sessionID string) error
	DeleteUserSessions(ctx context.Context, userID int64) error
	// DeleteUserSessionsExcept revokes every session of userID except keepSessionID
	// (used after a password change so the current browser stays signed in).
	DeleteUserSessionsExcept(ctx context.Context, userID int64, keepSessionID string) error
}

// DeleteExpiredSessions removes sessions whose expires_at has passed and
// returns the number of rows deleted. Called from the retention cleanup.
func (s *PostgresStore) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	return s.deleteInBatches(ctx, `
DELETE FROM sessions
WHERE id IN (
  SELECT id FROM sessions
  WHERE expires_at < $1
  LIMIT $2
)`, now.UTC(), deleteBatchSize)
}

func (s *PostgresStore) CreateSession(ctx context.Context, userID int64, sessionID string, expiresAt time.Time) (Session, error) {
	const q = `
INSERT INTO sessions(user_id, session_id, expires_at)
VALUES ($1, $2, $3)
RETURNING id, user_id, session_id, expires_at, created_at`
	var sess Session
	err := s.db.QueryRow(ctx, q, userID, sessionID, expiresAt).
		Scan(&sess.ID, &sess.UserID, &sess.SessionID, &sess.ExpiresAt, &sess.CreatedAt)
	if err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	return sess, nil
}

func (s *PostgresStore) LookupSession(ctx context.Context, sessionID string) (Session, error) {
	const q = `
SELECT id, user_id, session_id, expires_at, created_at
FROM sessions
WHERE session_id = $1 AND expires_at > now()`
	var sess Session
	err := s.db.QueryRow(ctx, q, sessionID).Scan(
		&sess.ID, &sess.UserID, &sess.SessionID, &sess.ExpiresAt, &sess.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("lookup session: %w", err)
	}
	return sess, nil
}

func (s *PostgresStore) TouchSession(ctx context.Context, sessionID string, expiresAt time.Time) error {
	cmd, err := s.db.Exec(ctx, `UPDATE sessions SET expires_at = $2 WHERE session_id = $1`, sessionID, expiresAt)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE session_id = $1`, sessionID)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *PostgresStore) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	return nil
}

func (s *PostgresStore) DeleteUserSessionsExcept(ctx context.Context, userID int64, keepSessionID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND session_id <> $2`, userID, keepSessionID)
	if err != nil {
		return fmt.Errorf("delete other user sessions: %w", err)
	}
	return nil
}
