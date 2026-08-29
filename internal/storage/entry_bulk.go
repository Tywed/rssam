package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const entrySelectColumns = `id, feed_id, title, url, content, original_content, content_fetched, author, published_at, hash, status, starred, created_at, updated_at`

// BulkUpdateEntries updates status and/or starred for entries owned by userID.
func (s *PostgresStore) BulkUpdateEntries(ctx context.Context, userID int64, entryIDs []int64, update BulkEntryUpdate) (int, error) {
	if len(entryIDs) == 0 {
		return 0, nil
	}
	if update.Status == nil && update.Starred == nil {
		return 0, fmt.Errorf("nothing to update")
	}

	setParts := make([]string, 0, 2)
	args := []any{userID, entryIDs}
	argN := 3

	if update.Status != nil {
		status := strings.TrimSpace(*update.Status)
		if !IsValidEntryStatus(status) || status == EntryStatusRemoved {
			return 0, fmt.Errorf("invalid entry status: %q", status)
		}
		setParts = append(setParts, fmt.Sprintf("status = $%d", argN))
		args = append(args, status)
		argN++
	}
	if update.Starred != nil {
		setParts = append(setParts, fmt.Sprintf("starred = $%d", argN))
		args = append(args, *update.Starred)
	}
	setParts = append(setParts, "updated_at = now()")

	q := `
UPDATE entries
SET ` + strings.Join(setParts, ", ") + `
WHERE user_id = $1 AND id = ANY($2::bigint[]) AND status <> '` + EntryStatusRemoved + `'`

	cmd, err := s.db.Exec(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("bulk update entries: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// MarkAllFeedEntriesRead marks all unread entries in a feed as read.
func (s *PostgresStore) MarkAllFeedEntriesRead(ctx context.Context, userID, feedID int64) (int, error) {
	if _, err := s.GetFeed(ctx, userID, feedID); err != nil {
		return 0, err
	}
	const q = `
UPDATE entries
SET status = $4, updated_at = now()
WHERE feed_id = $1 AND user_id = $2 AND status = $3`
	cmd, err := s.db.Exec(ctx, q, feedID, userID, EntryStatusUnread, EntryStatusRead)
	if err != nil {
		return 0, fmt.Errorf("mark all feed entries read: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// MarkAllCategoryEntriesRead marks all unread entries in a category as read.
func (s *PostgresStore) MarkAllCategoryEntriesRead(ctx context.Context, userID, categoryID int64) (int, error) {
	const qCat = `SELECT id FROM categories WHERE id = $1 AND user_id = $2`
	var catID int64
	if err := s.db.QueryRow(ctx, qCat, categoryID, userID).Scan(&catID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("lookup category: %w", err)
	}
	const q = `
UPDATE entries e
SET status = $4, updated_at = now()
FROM feeds f
WHERE e.feed_id = f.id
  AND f.category_id = $1
  AND e.user_id = $2
  AND e.status = $3`
	cmd, err := s.db.Exec(ctx, q, categoryID, userID, EntryStatusUnread, EntryStatusRead)
	if err != nil {
		return 0, fmt.Errorf("mark all category entries read: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// MarkAllEntriesRead marks all unread entries for a user as read.
func (s *PostgresStore) MarkAllEntriesRead(ctx context.Context, userID int64) (int, error) {
	const q = `
UPDATE entries
SET status = $2, updated_at = now()
WHERE user_id = $1 AND status = $3`
	cmd, err := s.db.Exec(ctx, q, userID, EntryStatusRead, EntryStatusUnread)
	if err != nil {
		return 0, fmt.Errorf("mark all entries read: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// UpdateEntry updates status and/or starred for a single entry scoped to a feed.
func (s *PostgresStore) UpdateEntry(ctx context.Context, userID, feedID, entryID int64, params UpdateEntryParams) (Entry, error) {
	if params.Status == nil && params.Starred == nil {
		return Entry{}, fmt.Errorf("nothing to update")
	}

	setParts := make([]string, 0, 2)
	args := []any{entryID, userID, feedID}
	argN := 4

	if params.Status != nil {
		status := strings.TrimSpace(*params.Status)
		if !IsValidEntryStatus(status) {
			return Entry{}, fmt.Errorf("invalid entry status: %q", status)
		}
		setParts = append(setParts, fmt.Sprintf("status = $%d", argN))
		args = append(args, status)
		argN++
	}
	if params.Starred != nil {
		setParts = append(setParts, fmt.Sprintf("starred = $%d", argN))
		args = append(args, *params.Starred)
	}
	setParts = append(setParts, "updated_at = now()")

	q := `
UPDATE entries
SET ` + strings.Join(setParts, ", ") + `
WHERE id = $1 AND user_id = $2 AND feed_id = $3
RETURNING ` + entrySelectColumns

	return s.scanEntry(s.db.QueryRow(ctx, q, args...))
}

// GetFeedEntry returns an entry scoped to feed and user.
func (s *PostgresStore) GetFeedEntry(ctx context.Context, userID, feedID, entryID int64) (Entry, error) {
	q := `SELECT ` + entrySelectColumns + ` FROM entries WHERE id = $1 AND user_id = $2 AND feed_id = $3`
	return s.scanEntry(s.db.QueryRow(ctx, q, entryID, userID, feedID))
}
