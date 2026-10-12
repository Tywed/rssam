package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// entrySelectColumns reads an entry together with the per-user state: the
// user_entries row aliased ue and, left-joined on it, the user_unread row
// aliased uu whose presence is the unread status (entryFromUser, or an
// UPDATE ... FROM pair).
const entrySelectColumns = `e.id, e.feed_id, e.title, e.url, e.content, e.original_content, e.content_fetched, e.author, e.published_at, e.hash, ` + entryStatusExpr + `, ue.starred, e.created_at, e.updated_at`

const entryStatusExpr = `CASE WHEN uu.entry_id IS NULL THEN ue.status ELSE '` + EntryStatusUnread + `' END`

// entryStatusExprOuter is entryStatusExpr for contexts where the
// user_entries row itself may be missing (then the entry counts as unread,
// as a row returned by the poll does).
const entryStatusExprOuter = `CASE WHEN ue.entry_id IS NULL OR uu.entry_id IS NOT NULL THEN '` + EntryStatusUnread + `' ELSE ue.status END`

const entryUnreadJoin = `LEFT JOIN user_unread uu ON uu.entry_id = ue.entry_id AND uu.user_id = ue.user_id`

const entryFromUser = `FROM user_entries ue JOIN entries e ON e.id = ue.entry_id ` + entryUnreadJoin

// entryFromUnread drives an unread list from user_unread, whose indexes
// order it; user_entries only contributes starred.
const entryFromUnread = `FROM user_unread uu JOIN user_entries ue ON ue.entry_id = uu.entry_id AND ue.user_id = uu.user_id JOIN entries e ON e.id = ue.entry_id`

// entryListColumns is entrySelectColumns with the two body columns replaced
// by empty literals, so the same scanner serves list pages that never render
// a body (the reader loads it per entry). On typical feeds the bodies are
// ~3 kB per row — 150 kB per 50-row page that was read from TOAST, sent and
// dropped.
const entryListColumns = `e.id, e.feed_id, e.title, e.url, '' AS content, '' AS original_content, e.content_fetched, e.author, e.published_at, e.hash, ` + entryStatusExpr + `, ue.starred, e.created_at, e.updated_at`

func entryColumns(withoutBody bool) string {
	if withoutBody {
		return entryListColumns
	}
	return entrySelectColumns
}

// entryListConds are the optional filters of a list or count; the user is
// always bound as $1.
type entryListConds struct {
	FeedID, CategoryID, LabelID *int64
	Starred                     *bool
}

// entryScope is the SQL of one list: the page query (list, without
// ORDER BY and LIMIT), the alias of the table that drives it (ue or uu),
// the matching count and the bound arguments of both.
type entryScope struct {
	list, alias, count string
	args               []any
}

// where renders the conditions against the driving table alias; starred
// lives in user_entries and is always ue.starred (the caller joins ue when
// uu drives). Spelled-out status words let the planner see the partial
// index WHERE status <> 'removed'.
func (c entryListConds) where(alias string, status string) string {
	conds := []string{alias + ".user_id = $1"}
	argN := 2
	if c.FeedID != nil {
		conds = append(conds, fmt.Sprintf("%s.feed_id = $%d", alias, argN))
		argN++
	}
	if c.CategoryID != nil {
		conds = append(conds, fmt.Sprintf("%s.feed_id IN (SELECT s.feed_id FROM subscriptions s JOIN feeds f ON f.id = s.feed_id WHERE s.user_id = $1 AND f.category_id = $%d)", alias, argN))
		argN++
	}
	if status != "" {
		conds = append(conds, status)
	}
	if c.Starred != nil {
		conds = append(conds, fmt.Sprintf("ue.starred = $%d", argN))
		argN++
	}
	if c.LabelID != nil {
		conds = append(conds, fmt.Sprintf("%s.entry_id IN (SELECT entry_id FROM entry_labels WHERE label_id = $%d)", alias, argN))
	}
	return "\nWHERE " + strings.Join(conds, " AND ")
}

func (c entryListConds) args(userID int64) []any {
	args := []any{userID}
	if c.FeedID != nil {
		args = append(args, *c.FeedID)
	}
	if c.CategoryID != nil {
		args = append(args, *c.CategoryID)
	}
	if c.Starred != nil {
		args = append(args, *c.Starred)
	}
	if c.LabelID != nil {
		args = append(args, *c.LabelID)
	}
	return args
}

// entryListScope picks the tables of a list by its status filter. Unread
// lists are driven by user_unread, whose indexes order them; everything
// else by user_entries, where "read" means no user_unread row. Counts
// never touch entries: the unread count is user_unread alone, the read
// count the difference of the two index-only counts (an anti-join of
// 200 000 rows took twice as long).
func entryListScope(userID int64, status *string, c entryListConds) (entryScope, error) {
	unreadFrom := "FROM user_unread uu"
	if c.Starred != nil {
		unreadFrom += " JOIN user_entries ue ON ue.entry_id = uu.entry_id AND ue.user_id = uu.user_id"
	}
	unreadCount := "SELECT count(*) " + unreadFrom + c.where("uu", "")
	allCount := "SELECT count(*) FROM user_entries ue" + c.where("ue", "ue.status <> '"+EntryStatusRemoved+"'")
	scope := entryScope{alias: "ue", args: c.args(userID)}
	if status == nil {
		scope.list = entryFromUser + c.where("ue", "ue.status <> '"+EntryStatusRemoved+"'")
		scope.count = allCount
		return scope, nil
	}
	switch *status {
	case EntryStatusUnread:
		scope.alias = "uu"
		scope.list = entryFromUnread + c.where("uu", "")
		scope.count = unreadCount
	case EntryStatusRead:
		scope.list = entryFromUser + c.where("ue", "ue.status = '"+EntryStatusRead+"' AND uu.entry_id IS NULL")
		scope.count = "SELECT (" + allCount + ") - (" + unreadCount + ")"
	case EntryStatusRemoved:
		scope.list = entryFromUser + c.where("ue", "ue.status = '"+EntryStatusRemoved+"'")
		scope.count = "SELECT count(*) FROM user_entries ue" + c.where("ue", "ue.status = '"+EntryStatusRemoved+"'")
	default:
		return entryScope{}, fmt.Errorf("invalid entry status: %q", *status)
	}
	return scope, nil
}

// Status writes. Unread is the presence of a user_unread row, so marking
// read is a DELETE and marking unread an INSERT copied from user_entries;
// user_entries itself is only written for starred and removed. Each helper
// is a CTE fragment named by its caller.
const (
	entryMarkRead   = `DELETE FROM user_unread WHERE user_id = $1 AND entry_id = ANY($2::bigint[]) RETURNING entry_id`
	entryMarkUnread = `INSERT INTO user_unread(entry_id, user_id, feed_id, sort_at)
  SELECT entry_id, user_id, feed_id, sort_at FROM user_entries
  WHERE user_id = $1 AND entry_id = ANY($2::bigint[]) AND status = '` + EntryStatusRead + `'
  ON CONFLICT DO NOTHING RETURNING entry_id`
)

// BulkUpdateEntries updates the user's status and/or starred flag and
// returns how many entries changed. Rows that already have the requested
// values are skipped: a repeated "mark read" writes nothing.
func (s *PostgresStore) BulkUpdateEntries(ctx context.Context, userID int64, entryIDs []int64, update BulkEntryUpdate) (int, error) {
	if len(entryIDs) == 0 {
		return 0, nil
	}
	if update.Status == nil && update.Starred == nil {
		return 0, fmt.Errorf("nothing to update")
	}

	var ctes []string
	args := []any{userID, entryIDs}
	if update.Status != nil {
		switch strings.TrimSpace(*update.Status) {
		case EntryStatusRead:
			ctes = append(ctes, "st AS ("+entryMarkRead+")")
		case EntryStatusUnread:
			ctes = append(ctes, "st AS ("+entryMarkUnread+")")
		default:
			return 0, fmt.Errorf("invalid entry status: %q", *update.Status)
		}
	}
	if update.Starred != nil {
		ctes = append(ctes, `sr AS (
  UPDATE user_entries SET starred = $3, updated_at = now()
  WHERE user_id = $1 AND entry_id = ANY($2::bigint[]) AND status <> '`+EntryStatusRemoved+`' AND starred <> $3
  RETURNING entry_id)`)
		args = append(args, *update.Starred)
	}
	q := "WITH " + strings.Join(ctes, ",\n") + "\nSELECT count(DISTINCT entry_id) FROM ("
	switch {
	case update.Status != nil && update.Starred != nil:
		q += "SELECT entry_id FROM st UNION ALL SELECT entry_id FROM sr"
	case update.Status != nil:
		q += "SELECT entry_id FROM st"
	default:
		q += "SELECT entry_id FROM sr"
	}
	q += ") c"

	var n int
	if err := s.db.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("bulk update entries: %w", err)
	}
	return n, nil
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
WITH un AS (
  DELETE FROM user_unread WHERE user_id = $1 AND entry_id = ANY($2::bigint[])
)
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
	const q = `DELETE FROM user_unread WHERE user_id = $1 AND feed_id = $2`
	cmd, err := s.db.Exec(ctx, q, userID, feedID)
	if err != nil {
		return 0, fmt.Errorf("mark all feed entries read: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// MarkAllCategoryEntriesRead marks all unread entries in a category as read.
func (s *PostgresStore) MarkAllCategoryEntriesRead(ctx context.Context, userID, categoryID int64) (int, error) {
	if err := s.lookupCategory(ctx, categoryID); err != nil {
		return 0, err
	}
	const q = `
DELETE FROM user_unread uu
USING feeds f
WHERE f.category_id = $1
  AND uu.user_id = $2
  AND uu.feed_id = f.id`
	cmd, err := s.db.Exec(ctx, q, categoryID, userID)
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
DELETE FROM user_unread uu
USING entry_labels el
WHERE el.entry_id = uu.entry_id
  AND el.label_id = $1
  AND uu.user_id = $2`
	cmd, err := s.db.Exec(ctx, q, labelID, userID)
	if err != nil {
		return 0, fmt.Errorf("mark all label entries read: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// MarkAllEntriesRead marks all unread entries for a user as read.
func (s *PostgresStore) MarkAllEntriesRead(ctx context.Context, userID int64) (int, error) {
	const q = `DELETE FROM user_unread WHERE user_id = $1`
	cmd, err := s.db.Exec(ctx, q, userID)
	if err != nil {
		return 0, fmt.Errorf("mark all entries read: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// UpdateEntry updates status and/or starred for a single entry scoped to a
// feed and returns it as it is afterwards.
func (s *PostgresStore) UpdateEntry(ctx context.Context, userID, feedID, entryID int64, params UpdateEntryParams) (Entry, error) {
	if params.Status == nil && params.Starred == nil {
		return Entry{}, fmt.Errorf("nothing to update")
	}
	var status string
	if params.Status != nil {
		status = strings.TrimSpace(*params.Status)
		if !IsValidEntryStatus(status) {
			return Entry{}, fmt.Errorf("invalid entry status: %q", status)
		}
	}
	var out Entry
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		switch status {
		case EntryStatusRead:
			_, err = tx.Exec(ctx, `DELETE FROM user_unread WHERE user_id = $1 AND entry_id = $2 AND feed_id = $3`, userID, entryID, feedID)
		case EntryStatusUnread:
			_, err = tx.Exec(ctx, `INSERT INTO user_unread(entry_id, user_id, feed_id, sort_at)
  SELECT entry_id, user_id, feed_id, sort_at FROM user_entries
  WHERE user_id = $1 AND entry_id = $2 AND feed_id = $3 AND status = '`+EntryStatusRead+`'
  ON CONFLICT DO NOTHING`, userID, entryID, feedID)
		case EntryStatusRemoved:
			_, err = tx.Exec(ctx, `WITH un AS (
  DELETE FROM user_unread WHERE user_id = $1 AND entry_id = $2 AND feed_id = $3
)
UPDATE user_entries SET status = '`+EntryStatusRemoved+`', updated_at = now()
WHERE user_id = $1 AND entry_id = $2 AND feed_id = $3 AND status <> '`+EntryStatusRemoved+`'`, userID, entryID, feedID)
		}
		if err != nil {
			return err
		}
		if params.Starred != nil {
			if _, err := tx.Exec(ctx, `UPDATE user_entries SET starred = $4, updated_at = now()
WHERE user_id = $1 AND entry_id = $2 AND feed_id = $3 AND starred <> $4`, userID, entryID, feedID, *params.Starred); err != nil {
				return err
			}
		}
		q := `SELECT ` + entrySelectColumns + ` ` + entryFromUser + ` WHERE ue.entry_id = $1 AND ue.user_id = $2 AND ue.feed_id = $3`
		out, err = s.scanEntry(tx.QueryRow(ctx, q, entryID, userID, feedID))
		return err
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, fmt.Errorf("update entry: %w", err)
	}
	return out, nil
}

// GetFeedEntry returns an entry scoped to feed and user.
func (s *PostgresStore) GetFeedEntry(ctx context.Context, userID, feedID, entryID int64) (Entry, error) {
	q := `SELECT ` + entrySelectColumns + ` ` + entryFromUser + ` WHERE ue.entry_id = $1 AND ue.user_id = $2 AND ue.feed_id = $3`
	return s.scanEntry(s.db.QueryRow(ctx, q, entryID, userID, feedID))
}
