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

func (s *PostgresStore) CreateEntries(ctx context.Context, feedID int64, entries []CreateEntryParams) (inserted int, insertedEntries []Entry, err error) {
	if len(entries) == 0 {
		return 0, nil, nil
	}

	var feedUserID int64
	if err := s.db.QueryRow(ctx, `SELECT user_id FROM feeds WHERE id = $1`, feedID).Scan(&feedUserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, ErrNotFound
		}
		return 0, nil, fmt.Errorf("lookup feed user_id: %w", err)
	}

	titles := make([]string, len(entries))
	urls := make([]string, len(entries))
	contents := make([]string, len(entries))
	authors := make([]string, len(entries))
	published := make([]*time.Time, len(entries))
	hashes := make([]string, len(entries))
	statuses := make([]string, len(entries))
	byHash := make(map[string]CreateEntryParams, len(entries))
	for i, e := range entries {
		if e.Status == "" {
			e.Status = EntryStatusUnread
		}
		if !IsValidEntryStatus(e.Status) {
			return 0, nil, fmt.Errorf("invalid entry status: %q", e.Status)
		}
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
		statuses[i] = e.Status
		byHash[e.Hash] = e
	}

	vecExpr := ftsVectorExprPlaceholders(s.ftsLanguage, "x.title", "x.content")
	q := `
INSERT INTO entries(feed_id, user_id, title, url, content, author, published_at, hash, status, search_vector)
SELECT $1, $2, x.title, x.url, x.content, NULLIF(x.author, ''), x.published_at, x.hash, x.status, ` + vecExpr + `
FROM unnest($3::text[], $4::text[], $5::text[], $6::text[], $7::timestamptz[], $8::text[], $9::text[])
  AS x(title, url, content, author, published_at, hash, status)
ON CONFLICT (feed_id, hash) DO NOTHING
RETURNING id, feed_id, title, url, author, published_at, hash, status, starred, created_at, updated_at`

	rows, err := s.db.Query(ctx, q, feedID, feedUserID, titles, urls, contents, authors, published, hashes, statuses)
	if err != nil {
		return 0, nil, fmt.Errorf("create entries: %w", err)
	}
	defer rows.Close()

	var encEntryIDs []int64
	var encURLs []string
	var encSizes []int64
	var encMIMEs []string
	for rows.Next() {
		var e Entry
		if err := rows.Scan(
			&e.ID,
			&e.FeedID,
			&e.Title,
			&e.URL,
			&e.Author,
			&e.PublishedAt,
			&e.Hash,
			&e.Status,
			&e.Starred,
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
		return 0, nil, fmt.Errorf("iterate create entries: %w", err)
	}
	if err := s.createEnclosuresBatch(ctx, feedUserID, encEntryIDs, encURLs, encSizes, encMIMEs); err != nil {
		return inserted, insertedEntries, err
	}
	return inserted, insertedEntries, nil
}

func (s *PostgresStore) GetEntry(ctx context.Context, userID int64, id int64) (Entry, error) {
	const q = `
SELECT ` + entrySelectColumns + `
FROM entries
WHERE id = $1 AND user_id = $2`
	return s.scanEntry(s.db.QueryRow(ctx, q, id, userID))
}

func (s *PostgresStore) GetEntryByID(ctx context.Context, id int64) (Entry, error) {
	const q = `
SELECT ` + entrySelectColumns + `
FROM entries
WHERE id = $1`
	return s.scanEntry(s.db.QueryRow(ctx, q, id))
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
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	var (
		where []string
		args  []any
	)
	argN := 1
	where = append(where, fmt.Sprintf("user_id = $%d", argN))
	args = append(args, userID)
	argN++
	if filter.FeedID != nil {
		where = append(where, fmt.Sprintf("feed_id = $%d", argN))
		args = append(args, *filter.FeedID)
		argN++
	}
	if filter.CategoryID != nil {
		where = append(where, fmt.Sprintf("feed_id IN (SELECT id FROM feeds WHERE user_id = $1 AND category_id = $%d)", argN))
		args = append(args, *filter.CategoryID)
		argN++
	}
	if filter.Status != nil {
		if !IsValidEntryStatus(*filter.Status) {
			return nil, 0, fmt.Errorf("invalid entry status: %q", *filter.Status)
		}
		where = append(where, fmt.Sprintf("status = $%d", argN))
		args = append(args, *filter.Status)
		argN++
	}
	if filter.Starred != nil {
		where = append(where, fmt.Sprintf("starred = $%d", argN))
		args = append(args, *filter.Starred)
		argN++
	}
	if filter.LabelID != nil {
		where = append(where, fmt.Sprintf("id IN (SELECT entry_id FROM entry_labels WHERE label_id = $%d)", argN))
		args = append(args, *filter.LabelID)
		argN++
	}
	args = append(args, limit, offset)

	q := `
SELECT ` + entrySelectColumns + `, count(*) OVER()
FROM entries`
	if len(where) > 0 {
		q += "\nWHERE " + strings.Join(where, " AND ")
	}
	q += "\nORDER BY " + entryOrderClause(filter.Sort) + "\nLIMIT $" + fmt.Sprint(argN) + " OFFSET $" + fmt.Sprint(argN+1)

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()

	out := make([]Entry, 0, limit)
	total := 0
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
			&total,
		); err != nil {
			return nil, 0, fmt.Errorf("scan entries: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate entries: %w", err)
	}
	return out, total, nil
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
    updated_at = now()
WHERE id = $1`
	cmd, err := s.db.Exec(ctx, q, params.ID, params.ETag, params.LastModified, params.LastCheckedAt, params.LastError, bridgeState)
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

func (s *PostgresStore) CountUnreadByFeed(ctx context.Context, feedID int64) (int, error) {
	const q = `SELECT count(*) FROM entries WHERE feed_id = $1 AND status = $2`
	var total int
	if err := s.db.QueryRow(ctx, q, feedID, EntryStatusUnread).Scan(&total); err != nil {
		return 0, fmt.Errorf("count unread by feed: %w", err)
	}
	return total, nil
}

func (s *PostgresStore) CountUnreadByCategory(ctx context.Context, categoryID int64) (int, error) {
	const q = `
SELECT count(*)
FROM entries e
JOIN feeds f ON f.id = e.feed_id
WHERE f.category_id = $1
  AND e.status = $2`
	var total int
	if err := s.db.QueryRow(ctx, q, categoryID, EntryStatusUnread).Scan(&total); err != nil {
		return 0, fmt.Errorf("count unread by category: %w", err)
	}
	return total, nil
}

func (s *PostgresStore) UpdateEntryContent(ctx context.Context, userID int64, params UpdateEntryContentParams) (Entry, error) {
	vecExpr := ftsVectorExpr(s.ftsLanguage)
	q := `
UPDATE entries
SET content = $3,
    original_content = $4,
    content_fetched = $5,
    search_vector = ` + vecExpr + `,
    updated_at = now()
WHERE id = $1 AND user_id = $2
RETURNING ` + entrySelectColumns
	return s.scanEntry(s.db.QueryRow(ctx, q, params.ID, userID, params.Content, params.OriginalContent, params.ContentFetched))
}

func (s *PostgresStore) CountUnreadGlobal(ctx context.Context) (int, error) {
	const q = `SELECT count(*) FROM entries WHERE status = $1`
	var total int
	if err := s.db.QueryRow(ctx, q, EntryStatusUnread).Scan(&total); err != nil {
		return 0, fmt.Errorf("count unread global: %w", err)
	}
	return total, nil
}

func (s *PostgresStore) UnreadCountsForUser(ctx context.Context, userID int64) (map[int64]int, map[int64]int, error) {
	feedCounts := make(map[int64]int)
	categoryCounts := make(map[int64]int)

	rows, err := s.db.Query(ctx, `
SELECT e.feed_id, f.category_id, count(*)::int
FROM entries e
JOIN feeds f ON f.id = e.feed_id
WHERE e.user_id = $1 AND e.status = $2
GROUP BY e.feed_id, f.category_id`, userID, EntryStatusUnread)
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
