package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	EntryStatusUnread  = "unread"
	EntryStatusRead    = "read"
	EntryStatusRemoved = "removed"
)

func IsValidEntryStatus(s string) bool {
	switch s {
	case EntryStatusUnread, EntryStatusRead, EntryStatusRemoved:
		return true
	default:
		return false
	}
}

// CreateEntries inserts the feed's new items once and fans them out to every
// subscriber as unread in the same statement. Returned entries carry the
// unread/unstarred state of a fresh row.
func (s *PostgresStore) CreateEntries(ctx context.Context, feedID int64, entries []CreateEntryParams) (inserted int, insertedEntries []Entry, err error) {
	if len(entries) == 0 {
		return 0, nil, nil
	}

	titles := make([]string, len(entries))
	urls := make([]string, len(entries))
	contents := make([]string, len(entries))
	authors := make([]string, len(entries))
	published := make([]*time.Time, len(entries))
	hashes := make([]string, len(entries))
	byHash := make(map[string]CreateEntryParams, len(entries))
	for i, e := range entries {
		if e.Hash == "" {
			return 0, nil, errors.New("entry hash is required")
		}
		titles[i] = e.Title
		urls[i] = e.URL
		contents[i] = e.Content
		if e.Author != nil {
			authors[i] = *e.Author
		}
		published[i] = e.PublishedAt
		hashes[i] = e.Hash
		byHash[e.Hash] = e
	}

	// feed_entry_dedup holds the hashes of rows that were collapsed or
	// deleted by retention; without this filter every such item would come
	// back as a fresh unread entry on the next poll. Same rule as
	// FilterKnownEntryHashes on the hash-only path.
	vecExpr := ftsVectorExprPlaceholders(s.ftsLanguage, "x.title", "x.content")
	q := `
WITH ins AS (
  INSERT INTO entries(feed_id, title, url, content, author, published_at, hash, search_vector)
  SELECT $1, x.title, x.url, x.content, NULLIF(x.author, ''), x.published_at, x.hash, ` + vecExpr + `
  FROM unnest($2::text[], $3::text[], $4::text[], $5::text[], $6::timestamptz[], $7::text[])
    AS x(title, url, content, author, published_at, hash)
  WHERE NOT EXISTS (SELECT 1 FROM feed_entry_dedup d WHERE d.feed_id = $1 AND d.hash = x.hash)
  ON CONFLICT (feed_id, hash) DO NOTHING
  RETURNING id, feed_id, title, url, author, published_at, hash, created_at, updated_at
), fan AS (
  INSERT INTO user_entries(user_id, entry_id, feed_id, status, starred, sort_at)
  SELECT s.user_id, ins.id, ins.feed_id, '` + EntryStatusUnread + `', FALSE, COALESCE(ins.published_at, ins.created_at)
  FROM ins JOIN subscriptions s ON s.feed_id = ins.feed_id
)
SELECT id, feed_id, title, url, author, published_at, hash, created_at, updated_at FROM ins`

	rows, err := s.db.Query(ctx, q, feedID, titles, urls, contents, authors, published, hashes)
	if err != nil {
		if isForeignKeyViolation(err) {
			return 0, nil, ErrNotFound
		}
		return 0, nil, fmt.Errorf("create entries: %w", err)
	}
	defer rows.Close()

	var encEntryIDs []int64
	var encURLs []string
	var encSizes []int64
	var encMIMEs []string
	for rows.Next() {
		e := Entry{Status: EntryStatusUnread}
		if err := rows.Scan(
			&e.ID,
			&e.FeedID,
			&e.Title,
			&e.URL,
			&e.Author,
			&e.PublishedAt,
			&e.Hash,
			&e.CreatedAt,
			&e.UpdatedAt,
		); err != nil {
			return 0, nil, fmt.Errorf("scan created entry: %w", err)
		}
		if src, ok := byHash[e.Hash]; ok {
			e.Content = src.Content
			for _, enc := range src.Enclosures {
				url := strings.TrimSpace(enc.URL)
				if url == "" {
					continue
				}
				encEntryIDs = append(encEntryIDs, e.ID)
				encURLs = append(encURLs, url)
				encSizes = append(encSizes, enc.Size)
				encMIMEs = append(encMIMEs, enc.MIMEType)
			}
		}
		inserted++
		insertedEntries = append(insertedEntries, e)
	}
	if err := rows.Err(); err != nil {
		if isForeignKeyViolation(err) {
			return 0, nil, ErrNotFound
		}
		return 0, nil, fmt.Errorf("iterate create entries: %w", err)
	}
	if err := s.createEnclosuresBatch(ctx, encEntryIDs, encURLs, encSizes, encMIMEs); err != nil {
		return inserted, insertedEntries, err
	}
	return inserted, insertedEntries, nil
}

func (s *PostgresStore) GetEntry(ctx context.Context, userID int64, id int64) (Entry, error) {
	q := `SELECT ` + entrySelectColumns + ` ` + entryFromUser + ` WHERE ue.user_id = $2 AND ue.entry_id = $1`
	return s.scanEntry(s.db.QueryRow(ctx, q, id, userID))
}

func (s *PostgresStore) scanEntry(row pgx.Row) (Entry, error) {
	var e Entry
	err := row.Scan(
		&e.ID,
		&e.FeedID,
		&e.Title,
		&e.URL,
		&e.Content,
		&e.OriginalContent,
		&e.ContentFetched,
		&e.Author,
		&e.PublishedAt,
		&e.Hash,
		&e.Status,
		&e.Starred,
		&e.CreatedAt,
		&e.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, fmt.Errorf("scan entry: %w", err)
	}
	return e, nil
}

func (s *PostgresStore) ListEntries(ctx context.Context, userID int64, filter ListEntriesFilter) ([]Entry, int, error) {
	return s.listEntries(ctx, userID, filter)
}

func (s *PostgresStore) ListFeedEntries(ctx context.Context, userID int64, feedID int64, filter ListEntriesFilter) ([]Entry, int, error) {
	filter.FeedID = &feedID
	return s.listEntries(ctx, userID, filter)
}

func (s *PostgresStore) listEntries(ctx context.Context, userID int64, filter ListEntriesFilter) ([]Entry, int, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
	}
	offset := max(filter.Offset, 0)

	var (
		where []string
		args  []any
	)
	argN := 1
	where = append(where, fmt.Sprintf("ue.user_id = $%d", argN))
	args = append(args, userID)
	argN++
	if filter.FeedID != nil {
		where = append(where, fmt.Sprintf("ue.feed_id = $%d", argN))
		args = append(args, *filter.FeedID)
		argN++
	}
	if filter.CategoryID != nil {
		where = append(where, fmt.Sprintf("ue.feed_id IN (SELECT feed_id FROM subscriptions WHERE user_id = $1 AND category_id = $%d)", argN))
		args = append(args, *filter.CategoryID)
		argN++
	}
	if filter.Status != nil {
		if !IsValidEntryStatus(*filter.Status) {
			return nil, 0, fmt.Errorf("invalid entry status: %q", *filter.Status)
		}
		where = append(where, fmt.Sprintf("ue.status = $%d", argN))
		args = append(args, *filter.Status)
		argN++
	} else {
		where = append(where, fmt.Sprintf("ue.status <> $%d", argN))
		args = append(args, EntryStatusRemoved)
		argN++
	}
	if filter.Starred != nil {
		where = append(where, fmt.Sprintf("ue.starred = $%d", argN))
		args = append(args, *filter.Starred)
		argN++
	}
	if filter.LabelID != nil {
		where = append(where, fmt.Sprintf("ue.entry_id IN (SELECT entry_id FROM entry_labels WHERE label_id = $%d)", argN))
		args = append(args, *filter.LabelID)
		argN++
	}
	whereSQL := "\nWHERE " + strings.Join(where, " AND ")
	fetch := limit
	if !filter.WithTotal {
		fetch++
	}
	q := "SELECT " + entryColumns(filter.WithoutBody) + "\n" + entryFromUser + whereSQL +
		"\nORDER BY " + entryOrderClause(filter.Sort) +
		"\nLIMIT $" + fmt.Sprint(argN) + " OFFSET $" + fmt.Sprint(argN+1)

	rows, err := s.db.Query(ctx, q, append(args, fetch, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list entries: %w", err)
	}
	out, err := scanEntries(rows, fetch)
	if err != nil {
		return nil, 0, fmt.Errorf("list entries: %w", err)
	}
	if !filter.WithTotal {
		out, total := trimPage(offset, limit, out)
		return out, total, nil
	}
	var total int
	if err := s.db.QueryRow(ctx, "SELECT count(*) FROM user_entries ue"+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count entries: %w", err)
	}
	return out, total, nil
}

// trimPage handles a page fetched with LIMIT limit+1: the extra row is
// dropped and its presence adds one to the total so callers can render a
// "next" link without counting the whole set.
func trimPage(offset, limit int, page []Entry) ([]Entry, int) {
	if len(page) > limit {
		return page[:limit], offset + limit + 1
	}
	return page, offset + len(page)
}

func scanEntries(rows pgx.Rows, capHint int) ([]Entry, error) {
	defer rows.Close()
	out := make([]Entry, 0, capHint)
	for rows.Next() {
		var e Entry
		if err := rows.Scan(
			&e.ID,
			&e.FeedID,
			&e.Title,
			&e.URL,
			&e.Content,
			&e.OriginalContent,
			&e.ContentFetched,
			&e.Author,
			&e.PublishedAt,
			&e.Hash,
			&e.Status,
			&e.Starred,
			&e.CreatedAt,
			&e.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan entries: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entries: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) UpdateFeedRefreshMeta(ctx context.Context, params UpdateFeedRefreshMetaParams) error {
	bridgeState := params.BridgeState
	if bridgeState == nil {
		bridgeState = []byte("{}")
	}
	const q = `
UPDATE feeds
SET etag = $2,
    last_modified = $3,
    last_checked_at = $4,
    last_error = $5,
    bridge_state = COALESCE($6::jsonb, bridge_state),
    parsing_error_count = CASE WHEN $5 = '' THEN 0 ELSE parsing_error_count END,
    poll_paused = CASE WHEN $5 = '' THEN FALSE ELSE poll_paused END,
    next_check_at = COALESCE($7, next_check_at),
    last_entry_at = CASE WHEN $8 THEN $4 ELSE last_entry_at END,
    feed_url = CASE WHEN $9 <> '' AND NOT EXISTS (SELECT 1 FROM feeds o WHERE o.feed_url = $9)
               THEN $9 ELSE feed_url END,
    updated_at = now()
WHERE id = $1`
	cmd, err := s.db.Exec(ctx, q, params.ID, params.ETag, params.LastModified, params.LastCheckedAt, params.LastError, bridgeState, params.NextCheckAt, params.NewEntries > 0, params.NewFeedURL)
	if err != nil {
		return fmt.Errorf("update feed refresh meta: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) SetFeedNextCheckAt(ctx context.Context, feedID int64, nextCheckAt time.Time) error {
	const q = `
UPDATE feeds
SET next_check_at = $2,
    updated_at = now()
WHERE id = $1`
	cmd, err := s.db.Exec(ctx, q, feedID, nextCheckAt)
	if err != nil {
		return fmt.Errorf("set feed next_check_at: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) CountUnreadByFeed(ctx context.Context, userID, feedID int64) (int, error) {
	const q = `SELECT count(*) FROM user_entries WHERE user_id = $1 AND feed_id = $2 AND status = $3`
	var total int
	if err := s.db.QueryRow(ctx, q, userID, feedID, EntryStatusUnread).Scan(&total); err != nil {
		return 0, fmt.Errorf("count unread by feed: %w", err)
	}
	return total, nil
}

func (s *PostgresStore) CountUnreadByCategory(ctx context.Context, userID, categoryID int64) (int, error) {
	const q = `
SELECT count(*)
FROM user_entries ue
JOIN subscriptions s ON s.user_id = ue.user_id AND s.feed_id = ue.feed_id
WHERE ue.user_id = $1 AND s.category_id = $2 AND ue.status = $3`
	var total int
	if err := s.db.QueryRow(ctx, q, userID, categoryID, EntryStatusUnread).Scan(&total); err != nil {
		return 0, fmt.Errorf("count unread by category: %w", err)
	}
	return total, nil
}

func (s *PostgresStore) UpdateEntryContent(ctx context.Context, userID int64, params UpdateEntryContentParams) (Entry, error) {
	// Column references inside SET see the row before the update, so the
	// vector must be built from the $3 parameter, not from `content`.
	vecExpr := ftsVectorExprPlaceholders(s.ftsLanguage, "e.title", "$3")
	q := `
UPDATE entries e
SET content = $3,
    original_content = $4,
    content_fetched = $5,
    search_vector = ` + vecExpr + `,
    updated_at = now()
FROM user_entries ue
WHERE e.id = $1 AND ue.entry_id = e.id AND ue.user_id = $2
RETURNING ` + entrySelectColumns
	return s.scanEntry(s.db.QueryRow(ctx, q, params.ID, userID, params.Content, params.OriginalContent, params.ContentFetched))
}

// CountUnreadGlobalForUser counts the user's unread entries.
func (s *PostgresStore) CountUnreadGlobalForUser(ctx context.Context, userID int64) (int, error) {
	const q = `SELECT count(*) FROM user_entries WHERE user_id = $1 AND status = $2`
	var total int
	if err := s.db.QueryRow(ctx, q, userID, EntryStatusUnread).Scan(&total); err != nil {
		return 0, fmt.Errorf("count unread global for user: %w", err)
	}
	return total, nil
}

func (s *PostgresStore) UnreadCountsForUser(ctx context.Context, userID int64) (map[int64]int, map[int64]int, error) {
	feedCounts := make(map[int64]int)
	categoryCounts := make(map[int64]int)

	rows, err := s.db.Query(ctx, `
SELECT c.feed_id, s.category_id, c.n
FROM (
	SELECT feed_id, count(*)::int AS n
	FROM user_entries
	WHERE user_id = $1 AND status = $2
	GROUP BY feed_id
) c
JOIN subscriptions s ON s.user_id = $1 AND s.feed_id = c.feed_id`, userID, EntryStatusUnread)
	if err != nil {
		return nil, nil, fmt.Errorf("unread counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var feedID int64
		var catID *int64
		var n int
		if err := rows.Scan(&feedID, &catID, &n); err != nil {
			return nil, nil, fmt.Errorf("scan unread counts: %w", err)
		}
		feedCounts[feedID] = n
		if catID != nil {
			categoryCounts[*catID] += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate unread counts: %w", err)
	}
	return feedCounts, categoryCounts, nil
}
