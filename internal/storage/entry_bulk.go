package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// entrySelectColumns reads an entry together with the per-user state of the
// user_entries row aliased ue (entryFromUser, or an UPDATE ... FROM pair).
const entrySelectColumns = `e.id, e.feed_id, e.title, e.url, e.content, e.original_content, e.content_fetched, e.author, e.published_at, e.hash, ue.status, ue.starred, e.created_at, e.updated_at`

const entryFromUser = `FROM user_entries ue JOIN entries e ON e.id = ue.entry_id`

// entryListColumns is entrySelectColumns with the two body columns replaced
// by empty literals, so the same scanner serves list pages that never render
// a body (the reader loads it per entry). On typical feeds the bodies are
// ~3 kB per row — 150 kB per 50-row page that was read from TOAST, sent and
// dropped.
const entryListColumns = `e.id, e.feed_id, e.title, e.url, '' AS content, '' AS original_content, e.content_fetched, e.author, e.published_at, e.hash, ue.status, ue.starred, e.created_at, e.updated_at`

func entryColumns(withoutBody bool) string {
	if withoutBody {
		return entryListColumns
	}
	return entrySelectColumns
}

// BulkUpdateEntries updates the user's status and/or starred flag. Rows
// that already have the requested values are skipped: a repeated "mark
// read" writes nothing.
func (s *PostgresStore) BulkUpdateEntries(ctx context.Context, userID int64, entryIDs []int64, update BulkEntryUpdate) (int, error) {
	if len(entryIDs) == 0 {
		return 0, nil
	}
	if update.Status == nil && update.Starred == nil {
		return 0, fmt.Errorf("nothing to update")
	}

	setParts := make([]string, 0, 2)
	changed := make([]string, 0, 2)
	args := []any{userID, entryIDs}
	argN := 3

	if update.Status != nil {
		status := strings.TrimSpace(*update.Status)
		if !IsValidEntryStatus(status) || status == EntryStatusRemoved {
			return 0, fmt.Errorf("invalid entry status: %q", status)
		}
		setParts = append(setParts, fmt.Sprintf("status = $%d", argN))
		changed = append(changed, fmt.Sprintf("status <> $%d", argN))
		args = append(args, status)
		argN++
	}
	if update.Starred != nil {
		setParts = append(setParts, fmt.Sprintf("starred = $%d", argN))
		changed = append(changed, fmt.Sprintf("starred <> $%d", argN))
		args = append(args, *update.Starred)
	}
	setParts = append(setParts, "updated_at = now()")

	q := `
UPDATE user_entries
SET ` + strings.Join(setParts, ", ") + `
WHERE user_id = $1 AND entry_id = ANY($2::bigint[]) AND status <> '` + EntryStatusRemoved + `'
  AND (` + strings.Join(changed, " OR ") + `)`

	cmd, err := s.db.Exec(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("bulk update entries: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// MarkEntriesRemoved sets status=removed on the user's entries (soft delete
// used by the filter "delete" action). BulkUpdateEntries deliberately refuses
// the removed status because it backs the user-facing bulk API; this is the
// internal counterpart. Already removed rows are left untouched.
func (s *PostgresStore) MarkEntriesRemoved(ctx context.Context, userID int64, entryIDs []int64) (int, error) {
	if len(entryIDs) == 0 {
		return 0, nil
	}
	const q = `
UPDATE user_entries
SET status = $3,
    updated_at = now()
WHERE user_id = $1 AND entry_id = ANY($2::bigint[]) AND status <> $3`
	cmd, err := s.db.Exec(ctx, q, userID, entryIDs, EntryStatusRemoved)
	if err != nil {
		return 0, fmt.Errorf("mark entries removed: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// MarkAllFeedEntriesRead marks all unread entries in a feed as read.
func (s *PostgresStore) MarkAllFeedEntriesRead(ctx context.Context, userID, feedID int64) (int, error) {
	if _, err := s.GetFeed(ctx, userID, feedID); err != nil {
		return 0, err
	}
	const q = `
UPDATE user_entries
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
UPDATE user_entries ue
SET status = $4, updated_at = now()
FROM subscriptions s
WHERE s.user_id = $2
  AND s.category_id = $1
  AND ue.user_id = $2
  AND ue.feed_id = s.feed_id
  AND ue.status = $3`
	cmd, err := s.db.Exec(ctx, q, categoryID, userID, EntryStatusUnread, EntryStatusRead)
	if err != nil {
		return 0, fmt.Errorf("mark all category entries read: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// MarkAllLabelEntriesRead marks all unread entries carrying a label as read.
func (s *PostgresStore) MarkAllLabelEntriesRead(ctx context.Context, userID, labelID int64) (int, error) {
	const qLabel = `SELECT id FROM labels WHERE id = $1 AND user_id = $2`
	var id int64
	if err := s.db.QueryRow(ctx, qLabel, labelID, userID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("lookup label: %w", err)
	}
	const q = `
UPDATE user_entries ue
SET status = $4, updated_at = now()
FROM entry_labels el
WHERE el.entry_id = ue.entry_id
  AND el.label_id = $1
  AND ue.user_id = $2
  AND ue.status = $3`
	cmd, err := s.db.Exec(ctx, q, labelID, userID, EntryStatusUnread, EntryStatusRead)
	if err != nil {
		return 0, fmt.Errorf("mark all label entries read: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// MarkAllEntriesRead marks all unread entries for a user as read.
func (s *PostgresStore) MarkAllEntriesRead(ctx context.Context, userID int64) (int, error) {
	const q = `
UPDATE user_entries
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
UPDATE user_entries ue
SET ` + strings.Join(setParts, ", ") + `
FROM entries e
WHERE e.id = ue.entry_id AND ue.entry_id = $1 AND ue.user_id = $2 AND ue.feed_id = $3
RETURNING ` + entrySelectColumns

	return s.scanEntry(s.db.QueryRow(ctx, q, args...))
}

// GetFeedEntry returns an entry scoped to feed and user.
func (s *PostgresStore) GetFeedEntry(ctx context.Context, userID, feedID, entryID int64) (Entry, error) {
	q := `SELECT ` + entrySelectColumns + ` ` + entryFromUser + ` WHERE ue.entry_id = $1 AND ue.user_id = $2 AND ue.feed_id = $3`
	return s.scanEntry(s.db.QueryRow(ctx, q, entryID, userID, feedID))
}
