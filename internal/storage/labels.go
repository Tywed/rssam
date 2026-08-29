package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ListLabels(ctx context.Context, userID int64, limit, offset int) ([]Label, int, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
	}
	if offset < 0 {
		offset = 0
	}
	const q = `
SELECT id, user_id, caption, fg_color, bg_color, created_at, updated_at, count(*) OVER()
FROM labels
WHERE user_id = $1
ORDER BY caption ASC, id ASC
LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, q, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list labels: %w", err)
	}
	defer rows.Close()

	out := make([]Label, 0, limit)
	total := 0
	for rows.Next() {
		var l Label
		if err := rows.Scan(&l.ID, &l.UserID, &l.Caption, &l.FgColor, &l.BgColor, &l.CreatedAt, &l.UpdatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("scan labels: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate labels: %w", err)
	}
	return out, total, nil
}

func (s *PostgresStore) CreateLabel(ctx context.Context, params CreateLabelParams) (Label, error) {
	params.Caption = strings.TrimSpace(params.Caption)
	if params.Caption == "" {
		return Label{}, errors.New("caption is required")
	}
	if strings.TrimSpace(params.FgColor) == "" {
		params.FgColor = "#ffffff"
	}
	if strings.TrimSpace(params.BgColor) == "" {
		params.BgColor = "#2980b9"
	}
	const q = `
INSERT INTO labels(user_id, caption, fg_color, bg_color)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, caption, fg_color, bg_color, created_at, updated_at`
	var l Label
	err := s.db.QueryRow(ctx, q, params.UserID, params.Caption, params.FgColor, params.BgColor).
		Scan(&l.ID, &l.UserID, &l.Caption, &l.FgColor, &l.BgColor, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return Label{}, fmt.Errorf("create label: %w", err)
	}
	return l, nil
}

func (s *PostgresStore) GetLabel(ctx context.Context, userID int64, id int64) (Label, error) {
	const q = `
SELECT id, user_id, caption, fg_color, bg_color, created_at, updated_at
FROM labels
WHERE id = $1 AND user_id = $2`
	var l Label
	err := s.db.QueryRow(ctx, q, id, userID).Scan(&l.ID, &l.UserID, &l.Caption, &l.FgColor, &l.BgColor, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Label{}, ErrNotFound
		}
		return Label{}, fmt.Errorf("get label: %w", err)
	}
	return l, nil
}

func (s *PostgresStore) UpdateLabel(ctx context.Context, params UpdateLabelParams) (Label, error) {
	params.Caption = strings.TrimSpace(params.Caption)
	if params.Caption == "" {
		return Label{}, errors.New("caption is required")
	}
	const q = `
UPDATE labels
SET caption = $3, fg_color = $4, bg_color = $5, updated_at = now()
WHERE id = $1 AND user_id = $2
RETURNING id, user_id, caption, fg_color, bg_color, created_at, updated_at`
	var l Label
	err := s.db.QueryRow(ctx, q, params.ID, params.UserID, params.Caption, params.FgColor, params.BgColor).
		Scan(&l.ID, &l.UserID, &l.Caption, &l.FgColor, &l.BgColor, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Label{}, ErrNotFound
		}
		return Label{}, fmt.Errorf("update label: %w", err)
	}
	return l, nil
}

func (s *PostgresStore) DeleteLabel(ctx context.Context, userID int64, id int64) error {
	cmd, err := s.db.Exec(ctx, `DELETE FROM labels WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete label: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) AssignEntryLabel(ctx context.Context, entryID, labelID int64) error {
	const q = `
INSERT INTO entry_labels(entry_id, label_id)
VALUES ($1, $2)
ON CONFLICT (entry_id, label_id) DO NOTHING`
	_, err := s.db.Exec(ctx, q, entryID, labelID)
	if err != nil {
		return fmt.Errorf("assign entry label: %w", err)
	}
	return nil
}

func (s *PostgresStore) EntryCountsByLabel(ctx context.Context, userID int64) (map[int64]int, error) {
	const q = `
SELECT el.label_id, count(*)
FROM entry_labels el
JOIN entries e ON e.id = el.entry_id
WHERE e.user_id = $1 AND e.status <> $2
GROUP BY el.label_id`
	rows, err := s.db.Query(ctx, q, userID, EntryStatusRemoved)
	if err != nil {
		return nil, fmt.Errorf("entry counts by label: %w", err)
	}
	defer rows.Close()

	out := map[int64]int{}
	for rows.Next() {
		var labelID int64
		var n int
		if err := rows.Scan(&labelID, &n); err != nil {
			return nil, fmt.Errorf("scan entry counts by label: %w", err)
		}
		out[labelID] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entry counts by label: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) UnreadCountsByLabel(ctx context.Context, userID int64) (map[int64]int, error) {
	const q = `
SELECT el.label_id, count(*)
FROM entry_labels el
JOIN entries e ON e.id = el.entry_id
WHERE e.user_id = $1 AND e.status = $2
GROUP BY el.label_id`
	rows, err := s.db.Query(ctx, q, userID, EntryStatusUnread)
	if err != nil {
		return nil, fmt.Errorf("unread counts by label: %w", err)
	}
	defer rows.Close()

	out := map[int64]int{}
	for rows.Next() {
		var labelID int64
		var n int
		if err := rows.Scan(&labelID, &n); err != nil {
			return nil, fmt.Errorf("scan unread counts by label: %w", err)
		}
		out[labelID] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unread counts by label: %w", err)
	}
	return out, nil
}
