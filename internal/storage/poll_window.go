package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// PollWindow is a daily time-of-day window in local time; Start == End is
// not allowed, Start > End wraps past midnight ("22:00-06:00").
type PollWindow struct {
	Start int // minutes since midnight
	End   int
}

var ErrInvalidPollHours = errors.New("poll_hours must be HH:MM-HH:MM with different start and end")

// ParsePollHours parses "HH:MM-HH:MM"; empty input yields ok=false (no window).
func ParsePollHours(s string) (w PollWindow, ok bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return PollWindow{}, false, nil
	}
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return PollWindow{}, false, ErrInvalidPollHours
	}
	start, err := parseMinuteOfDay(parts[0])
	if err != nil {
		return PollWindow{}, false, err
	}
	end, err := parseMinuteOfDay(parts[1])
	if err != nil {
		return PollWindow{}, false, err
	}
	if start == end {
		return PollWindow{}, false, ErrInvalidPollHours
	}
	return PollWindow{Start: start, End: end}, true, nil
}

func parseMinuteOfDay(s string) (int, error) {
	var h, m int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, ErrInvalidPollHours
	}
	return h*60 + m, nil
}

// NormalizePollHours returns the canonical "HH:MM-HH:MM" form or "" for empty.
func NormalizePollHours(s string) (string, error) {
	w, ok, err := ParsePollHours(s)
	if err != nil || !ok {
		return "", err
	}
	return fmt.Sprintf("%02d:%02d-%02d:%02d", w.Start/60, w.Start%60, w.End/60, w.End%60), nil
}

// Contains reports whether t (converted to local time) falls inside the window.
func (w PollWindow) Contains(t time.Time) bool {
	m := minuteOfDay(t.Local())
	if w.Start < w.End {
		return m >= w.Start && m < w.End
	}
	return m >= w.Start || m < w.End
}

// NextOpen returns t itself when the window is open at t, otherwise the next
// moment it opens.
func (w PollWindow) NextOpen(t time.Time) time.Time {
	if w.Contains(t) {
		return t
	}
	lt := t.Local()
	open := time.Date(lt.Year(), lt.Month(), lt.Day(), w.Start/60, w.Start%60, 0, 0, lt.Location())
	if !open.After(lt) {
		open = open.AddDate(0, 0, 1)
	}
	return open
}

func minuteOfDay(t time.Time) int { return t.Hour()*60 + t.Minute() }

// CategoryPollHoursStore reads/writes the per-category polling window.
type CategoryPollHoursStore interface {
	GetCategoryPollHours(ctx context.Context, categoryID int64) (string, error)
	SetCategoryPollHours(ctx context.Context, userID, categoryID int64, pollHours string) error
}

func (s *PostgresStore) GetCategoryPollHours(ctx context.Context, categoryID int64) (string, error) {
	var v string
	if err := s.db.QueryRow(ctx, `SELECT poll_hours FROM categories WHERE id = $1`, categoryID).Scan(&v); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get category poll_hours: %w", err)
	}
	return v, nil
}

func (s *PostgresStore) SetCategoryPollHours(ctx context.Context, userID, categoryID int64, pollHours string) error {
	norm, err := NormalizePollHours(pollHours)
	if err != nil {
		return err
	}
	cmd, err := s.db.Exec(ctx, `UPDATE categories SET poll_hours = $3, updated_at = now() WHERE id = $1 AND user_id = $2 AND poll_hours <> $3`, categoryID, userID, norm)
	if err != nil {
		return fmt.Errorf("set category poll_hours: %w", err)
	}
	if cmd.RowsAffected() > 0 {
		// Feeds parked at the old window's opening come back to their own
		// interval; the new window is applied by the next poll attempt.
		if _, err := s.db.Exec(ctx, `
UPDATE feeds SET next_check_at = now() + interval_minutes * interval '1 minute', updated_at = now()
WHERE category_id = $1 AND user_id = $2 AND next_check_at > now() + interval_minutes * interval '1 minute'`, categoryID, userID); err != nil {
			return fmt.Errorf("reset feeds after poll_hours change: %w", err)
		}
	}
	if cmd.RowsAffected() == 0 {
		var exists bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM categories WHERE id = $1 AND user_id = $2)`, categoryID, userID).Scan(&exists); err != nil {
			return fmt.Errorf("check category: %w", err)
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}
