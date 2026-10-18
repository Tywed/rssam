package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// entrySelectColumns reads an entry together with the per-user state: the
// optional user_entries exception row aliased ue (starred / removed) and
// the optional user_unread row aliased uu whose presence is the unread
// status. An entry without either row is read and not starred.
const entrySelectColumns = `e.id, e.feed_id, e.title, e.url, e.content, e.original_content, e.content_fetched, e.author, e.published_at, e.hash, ` + entryStatusExpr + `, ` + entryStarredExpr + `, e.created_at, e.updated_at`

const entryStatusExpr = `CASE WHEN uu.entry_id IS NOT NULL THEN '` + EntryStatusUnread + `' WHEN e.removed_at IS NOT NULL THEN '` + EntryStatusRemoved + `' ELSE COALESCE(ue.status, '` + EntryStatusRead + `') END`

const entryStarredExpr = `COALESCE(ue.starred, FALSE)`

// entryUserJoins attaches ue and uu to an entry alias e for user $1.
const entryUserJoins = `LEFT JOIN user_entries ue ON ue.entry_id = e.id AND ue.user_id = $1
LEFT JOIN user_unread uu ON uu.entry_id = e.id AND uu.user_id = $1`

// entryFromEntries reads entries e with the user's state attached.
const entryFromEntries = `FROM entries e
` + entryUserJoins

// entryFromUnread drives an unread list from user_unread, whose indexes
// order it; user_entries only contributes starred.
const entryFromUnread = `FROM user_unread uu
JOIN entries e ON e.id = uu.entry_id
LEFT JOIN user_entries ue ON ue.entry_id = uu.entry_id AND ue.user_id = uu.user_id`

// entryFromExceptions drives starred lists from user_entries.
const entryFromExceptions = `FROM user_entries ue
JOIN entries e ON e.id = ue.entry_id
LEFT JOIN user_unread uu ON uu.entry_id = ue.entry_id AND uu.user_id = ue.user_id`

// entryListColumns is entrySelectColumns with the two body columns replaced
// by empty literals, so the same scanner serves list pages that never render
// a body (the reader loads it per entry). On typical feeds the bodies are
// ~3 kB per row — 150 kB per 50-row page that was read from TOAST, sent and
// dropped.
const entryListColumns = `e.id, e.feed_id, e.title, e.url, '' AS content, '' AS original_content, e.content_fetched, e.author, e.published_at, e.hash, ` + entryStatusExpr + `, ` + entryStarredExpr + `, e.created_at, e.updated_at`

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

// entryScope is the SQL of one list. Lists driven by the user's own rows
// (uu for unread, ue for starred) are one query: list is the FROM/WHERE,
// alias the table whose index orders it. Lists driven by entries (all,
// read, removed) are two-stage: cand selects the page of ids from entries
// alone (its WHERE holds only subqueries on the small per-user tables, so
// the planner walks entries_sort_idx / entries_feed_sort_idx and stops at
// the page) and the per-user state is attached to those rows only. The
// one-query form joined user_entries and user_unread for every row it
// walked past: 5 500 buffers against 850 for the second page of 50.
type entryScope struct {
	list, cand, alias, count string
	args                     []any
}

// page is the query of one page: columns, ORDER BY and LIMIT $limitArg
// OFFSET $limitArg+1.
func (sc entryScope) page(columns, sort string, limitArg int) string {
	order := entryOrderClause(sort, sc.alias)
	limit := fmt.Sprintf("\nLIMIT $%d OFFSET $%d", limitArg, limitArg+1)
	if sc.cand == "" {
		return "SELECT " + columns + "\n" + sc.list + "\nORDER BY " + order + limit
	}
	return "SELECT " + columns + "\nFROM (SELECT e.id, e.sort_at " + sc.cand + "\nORDER BY " + order + limit + ") cand\n" +
		"JOIN entries e ON e.id = cand.id\n" + entryUserJoins + "\nORDER BY " + entryOrderClause(sort, "cand")
}

// The feeds visible to user $1 and the feeds of category $n, kept as two
// single-table subqueries: the planner memoizes each per feed_id while it
// walks the ordered entries index, whereas a joined subquery was probed
// twice per row (8 800 buffers against 400 for the first page of a
// category).
const subscribedFeeds = "SELECT s.feed_id FROM subscriptions s WHERE s.user_id = $1"

func categoryFeeds(categoryArg int) string {
	return fmt.Sprintf("SELECT f.id FROM feeds f WHERE f.category_id = $%d", categoryArg)
}

// argNums returns the parameter numbers of the optional filters, in the
// order args binds them.
func (c entryListConds) argNums() (feedArg, catArg, starArg, labelArg int) {
	n := 2
	if c.FeedID != nil {
		feedArg = n
		n++
	}
	if c.CategoryID != nil {
		catArg = n
		n++
	}
	if c.Starred != nil {
		starArg = n
		n++
	}
	if c.LabelID != nil {
		labelArg = n
	}
	return
}

// where renders the conditions of a list driven by the user's own table
// (alias uu or ue, both already the user's): feed and category narrow its
// feed_id, extra is appended as is.
func (c entryListConds) where(alias, extra string) string {
	feedArg, catArg, starArg, labelArg := c.argNums()
	conds := []string{alias + ".user_id = $1"}
	if feedArg != 0 {
		conds = append(conds, fmt.Sprintf("%s.feed_id = $%d", alias, feedArg))
	}
	if catArg != 0 {
		conds = append(conds, fmt.Sprintf("%s.feed_id IN (%s)", alias, categoryFeeds(catArg)))
	}
	if extra != "" {
		conds = append(conds, extra)
	}
	if starArg != 0 {
		conds = append(conds, fmt.Sprintf("%s = $%d", entryStarredExpr, starArg))
	}
	if labelArg != 0 {
		conds = append(conds, fmt.Sprintf("e.id IN (SELECT entry_id FROM entry_labels WHERE label_id = $%d)", labelArg))
	}
	return "\nWHERE " + strings.Join(conds, " AND ")
}

// whereEntries renders the conditions of a list driven by entries e: a
// subscription to the feed, the state subqueries in extra, and the
// optional filters as subqueries on the user's rows.
func (c entryListConds) whereEntries(extra string) string {
	feedArg, catArg, starArg, labelArg := c.argNums()
	var conds []string
	if feedArg != 0 {
		conds = append(conds, fmt.Sprintf("e.feed_id = $%d", feedArg))
	}
	if catArg != 0 {
		conds = append(conds, fmt.Sprintf("e.feed_id IN (%s)", categoryFeeds(catArg)))
	}
	conds = append(conds, "e.feed_id IN ("+subscribedFeeds+")")
	if extra != "" {
		conds = append(conds, extra)
	}
	if starArg != 0 {
		conds = append(conds, fmt.Sprintf("$%d = EXISTS (SELECT 1 FROM user_entries x WHERE x.entry_id = e.id AND x.user_id = $1 AND x.starred)", starArg))
	}
	if labelArg != 0 {
		conds = append(conds, fmt.Sprintf("e.id IN (SELECT entry_id FROM entry_labels WHERE label_id = $%d)", labelArg))
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

// State of entry e for user $1 as subqueries on the per-user tables.
const (
	entryNotRemoved = `(ue.status IS NULL OR ue.status <> '` + EntryStatusRemoved + `') AND e.removed_at IS NULL`
	entryNoRemoval  = `e.removed_at IS NULL AND NOT EXISTS (SELECT 1 FROM user_entries x WHERE x.entry_id = e.id AND x.user_id = $1 AND x.status = '` + EntryStatusRemoved + `')`
	entryNoUnread   = `NOT EXISTS (SELECT 1 FROM user_unread x WHERE x.entry_id = e.id AND x.user_id = $1)`
	// Removed covers the user's own removals and entries stripped after
	// webhook delivery; both sets are small and indexed.
	entryRemovedSet = `e.id IN (SELECT x.entry_id FROM user_entries x WHERE x.user_id = $1 AND x.status = '` + EntryStatusRemoved + `'
      UNION ALL SELECT r.id FROM entries r WHERE r.removed_at IS NOT NULL)`
)

// entryListScope picks the tables of a list by its status filter. Unread
// lists are driven by user_unread and starred ones by user_entries, whose
// indexes order them; everything else walks entries by (feed_id, sort_at)
// or (sort_at) and filters on the subscription.
func entryListScope(userID int64, status *string, c entryListConds) (entryScope, error) {
	scope := entryScope{alias: "e", args: c.args(userID)}
	st := ""
	if status != nil {
		st = *status
	}
	switch st {
	case "", EntryStatusRead:
		if c.Starred != nil && *c.Starred {
			extra := entryNotRemoved
			if st == EntryStatusRead {
				extra += " AND uu.entry_id IS NULL"
			}
			scope.alias = "ue"
			scope.list = entryFromExceptions + c.where("ue", "ue.starred AND "+extra)
			scope.count = cappedCount(scope.list)
			return scope, nil
		}
		extra := entryNoRemoval
		if st == EntryStatusRead {
			extra += " AND " + entryNoUnread
		}
		scope.cand = "FROM entries e" + c.whereEntries(extra)
		scope.count = cappedCount(scope.cand)
	case EntryStatusUnread:
		scope.alias = "uu"
		scope.list = entryFromUnread + c.where("uu", "")
		scope.count = cappedCount(scope.list)
	case EntryStatusRemoved:
		scope.cand = "FROM entries e" + c.whereEntries(entryRemovedSet)
		scope.count = cappedCount(scope.cand)
	default:
		return entryScope{}, fmt.Errorf("invalid entry status: %q", *status)
	}
	return scope, nil
}

// ListTotalCap bounds the WithTotal count of a list, like SearchTotalCap
// for search: a total equal to the cap means "at least that many". The
// exact count grew with the list - 42 ms for 60 000 unread rows, 24 ms
// for 200 000 entries - on every API page; walking the list's own index
// to the cap is 3-9 ms whatever the size.
const ListTotalCap = 10001

func cappedCount(fromWhere string) string {
	return "SELECT count(*) FROM (SELECT 1 " + fromWhere + "\nLIMIT " + fmt.Sprint(ListTotalCap) + ") c"
}

// Status writes. Unread is the presence of a user_unread row, so marking
// read is a DELETE and marking unread an INSERT copied from entries;
// user_entries holds only exceptions, so starring inserts a row and
// unstarring deletes it; a removed row keeps its star, as before, so the
// removal is never lost.
const (
	entryMarkRead   = `DELETE FROM user_unread WHERE user_id = $1 AND entry_id = ANY($2::bigint[]) RETURNING entry_id`
	entryMarkUnread = `INSERT INTO user_unread(entry_id, user_id, feed_id, sort_at)
  SELECT e.id, $1, e.feed_id, e.sort_at FROM entries e
  WHERE e.id = ANY($2::bigint[]) AND e.removed_at IS NULL
    AND e.feed_id IN (SELECT feed_id FROM subscriptions WHERE user_id = $1)
    AND NOT EXISTS (SELECT 1 FROM user_entries x WHERE x.entry_id = e.id AND x.user_id = $1 AND x.status = '` + EntryStatusRemoved + `')
  ON CONFLICT DO NOTHING RETURNING entry_id`
	entryStar = `INSERT INTO user_entries(entry_id, user_id, starred, sort_at)
  SELECT e.id, $1, TRUE, e.sort_at FROM entries e
  WHERE e.id = ANY($2::bigint[]) AND e.removed_at IS NULL
    AND e.feed_id IN (SELECT feed_id FROM subscriptions WHERE user_id = $1)
  ON CONFLICT (entry_id, user_id) DO UPDATE SET starred = TRUE, updated_at = now()
  WHERE NOT user_entries.starred AND user_entries.status <> '` + EntryStatusRemoved + `'
  RETURNING entry_id`
	entryUnstar = `DELETE FROM user_entries
  WHERE user_id = $1 AND entry_id = ANY($2::bigint[]) AND starred AND status <> '` + EntryStatusRemoved + `'
  RETURNING entry_id`
	entryRemove = `INSERT INTO user_entries(entry_id, user_id, status, sort_at)
  SELECT e.id, $1, '` + EntryStatusRemoved + `', e.sort_at FROM entries e
  WHERE e.id = ANY($2::bigint[])
    AND e.feed_id IN (SELECT feed_id FROM subscriptions WHERE user_id = $1)
  ON CONFLICT (entry_id, user_id) DO UPDATE SET status = '` + EntryStatusRemoved + `', updated_at = now()
  WHERE user_entries.status <> '` + EntryStatusRemoved + `'
  RETURNING entry_id`
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
		if *update.Starred {
			ctes = append(ctes, "sr AS ("+entryStar+")")
		} else {
			ctes = append(ctes, "sr AS ("+entryUnstar+")")
		}
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
	if err := s.db.QueryRow(ctx, q, userID, entryIDs).Scan(&n); err != nil {
		return 0, fmt.Errorf("bulk update entries: %w", err)
	}
	return n, nil
}

// MarkEntriesRemoved records a removal for the user (soft delete used by
// the filter "delete" action). BulkUpdateEntries deliberately refuses the
// removed status because it backs the user-facing bulk API; this is the
// internal counterpart. Already removed rows are left untouched.
func (s *PostgresStore) MarkEntriesRemoved(ctx context.Context, userID int64, entryIDs []int64) (int, error) {
	if len(entryIDs) == 0 {
		return 0, nil
	}
	const q = `
WITH un AS (
  DELETE FROM user_unread WHERE user_id = $1 AND entry_id = ANY($2::bigint[])
), rm AS (` + entryRemove + `)
SELECT count(*) FROM rm`
	var n int
	if err := s.db.QueryRow(ctx, q, userID, entryIDs).Scan(&n); err != nil {
		return 0, fmt.Errorf("mark entries removed: %w", err)
	}
	return n, nil
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
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := s.scanEntry(tx.QueryRow(ctx, entryByFeedQuery, userID, entryID, feedID)); err != nil {
			return err
		}
		ids := []int64{entryID}
		var stmt string
		switch status {
		case EntryStatusRead:
			stmt = entryMarkRead
		case EntryStatusUnread:
			stmt = entryMarkUnread
		case EntryStatusRemoved:
			stmt = "WITH un AS (DELETE FROM user_unread WHERE user_id = $1 AND entry_id = ANY($2::bigint[])) " + entryRemove
		}
		if stmt != "" {
			if _, err := tx.Exec(ctx, stmt, userID, ids); err != nil {
				return err
			}
		}
		if params.Starred != nil {
			stmt = entryUnstar
			if *params.Starred {
				stmt = entryStar
			}
			if _, err := tx.Exec(ctx, stmt, userID, ids); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		return s.GetFeedEntry(ctx, userID, feedID, entryID)
	}
	if errors.Is(err, ErrNotFound) {
		return Entry{}, ErrNotFound
	}
	return Entry{}, fmt.Errorf("update entry: %w", err)
}

const entryByFeedQuery = `SELECT ` + entrySelectColumns + ` ` + entryFromEntries + `
WHERE e.id = $2 AND e.feed_id = $3 AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.user_id = $1 AND s.feed_id = $3)`

// GetFeedEntry returns an entry scoped to feed and user.
func (s *PostgresStore) GetFeedEntry(ctx context.Context, userID, feedID, entryID int64) (Entry, error) {
	return s.scanEntry(s.db.QueryRow(ctx, entryByFeedQuery, userID, entryID, feedID))
}
