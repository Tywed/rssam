package storage

import (
	"context"
	"fmt"
	"strings"
)

type FeedEntryDedupParams struct {
	Hash string
	URL  string
}

// FilterKnownEntryHashes returns hashes already seen for feedID (entries table or feed_entry_dedup).
func (s *PostgresStore) FilterKnownEntryHashes(ctx context.Context, feedID int64, hashes []string) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	if len(hashes) == 0 {
		return out, nil
	}
	const q = `
SELECT hash FROM feed_entry_dedup WHERE feed_id = $1 AND hash = ANY($2)
UNION
SELECT hash FROM entries WHERE feed_id = $1 AND hash = ANY($2)`
	rows, err := s.db.Query(ctx, q, feedID, hashes)
	if err != nil {
		return nil, fmt.Errorf("filter known entry hashes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("scan known hash: %w", err)
		}
		out[h] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate known hashes: %w", err)
	}
	return out, nil
}

// RecordFeedEntryDedup inserts dedup rows idempotently. Returns count of newly inserted rows.
func (s *PostgresStore) RecordFeedEntryDedup(ctx context.Context, feedID int64, items []FeedEntryDedupParams) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	var b strings.Builder
	args := make([]any, 0, 1+len(items)*2)
	args = append(args, feedID)
	argN := 2
	b.WriteString(`
INSERT INTO feed_entry_dedup(feed_id, hash, url)
VALUES `)
	for i, it := range items {
		if strings.TrimSpace(it.Hash) == "" {
			return 0, fmt.Errorf("dedup hash is required")
		}
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "\n($1, $%d, $%d)", argN, argN+1)
		argN += 2
		args = append(args, it.Hash, it.URL)
	}
	b.WriteString(`
ON CONFLICT (feed_id, hash) DO NOTHING`)
	tag, err := s.db.Exec(ctx, b.String(), args...)
	if err != nil {
		return 0, fmt.Errorf("record feed entry dedup: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// StripEntryPayloadAfterWebhook clears entry body fields after successful webhook delivery.
// The row (and hash) remain for FK references and poll dedup.
func (s *PostgresStore) StripEntryPayloadAfterWebhook(ctx context.Context, entryID int64) error {
	const q = `
UPDATE entries
SET title = '',
    content = '',
    original_content = '',
    author = NULL,
    content_fetched = FALSE,
    search_vector = ''::tsvector,
    status = 'removed',
    updated_at = now()
WHERE id = $1`
	cmd, err := s.db.Exec(ctx, q, entryID)
	if err != nil {
		return fmt.Errorf("strip entry payload after webhook: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	const dedupQ = `
INSERT INTO feed_entry_dedup(feed_id, hash, url)
SELECT feed_id, hash, url FROM entries WHERE id = $1
ON CONFLICT (feed_id, hash) DO NOTHING`
	if _, err := s.db.Exec(ctx, dedupQ, entryID); err != nil {
		return fmt.Errorf("record dedup after strip: %w", err)
	}
	return nil
}

const collapseEntriesBatchSize = 5000

// CollapseEntriesToHashes records hashes for poll dedup and deletes full entry rows.
func (s *PostgresStore) CollapseEntriesToHashes(ctx context.Context, params CollapseEntriesParams) (int64, error) {
	if params.UserID <= 0 {
		return 0, fmt.Errorf("user_id is required")
	}
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := s.collapseEntriesBatch(ctx, params)
		if err != nil {
			return total, err
		}
		total += n
		if n < collapseEntriesBatchSize {
			return total, nil
		}
	}
}

func (s *PostgresStore) collapseEntriesBatch(ctx context.Context, params CollapseEntriesParams) (int64, error) {
	var feedID any
	if params.FeedID != nil {
		feedID = *params.FeedID
	}
	var categoryID any
	if params.CategoryID != nil {
		categoryID = *params.CategoryID
	}

	const q = `
WITH doomed AS (
  SELECT e.id, e.feed_id, e.hash, COALESCE(e.url, '') AS url
  FROM entries e
  INNER JOIN feeds f ON f.id = e.feed_id
  WHERE f.user_id = $1
    AND ($2::bigint IS NULL OR e.feed_id = $2)
    AND (
      $3::bigint IS NULL
      OR ($3 = 0 AND f.category_id IS NULL)
      OR f.category_id = $3
    )
    AND e.starred = FALSE
    AND ($6::boolean OR NOT EXISTS (SELECT 1 FROM entry_labels el WHERE el.entry_id = e.id))
    AND NOT EXISTS (
      SELECT 1 FROM webhook_logs wl
      WHERE wl.entry_id = e.id AND wl.status NOT IN ('sent', 'dead')
    )
    AND (NOT $4::boolean OR f.store_hash_only)
  LIMIT $5
),
ins AS (
  INSERT INTO feed_entry_dedup (feed_id, hash, url)
  SELECT feed_id, hash, url FROM doomed
  WHERE hash <> ''
  ON CONFLICT (feed_id, hash) DO NOTHING
),
del AS (
  DELETE FROM entries e
  USING doomed d
  WHERE e.id = d.id
  RETURNING e.id
)
SELECT COUNT(*) FROM del`

	var n int64
	if err := s.db.QueryRow(ctx, q, params.UserID, feedID, categoryID, params.OnlyHashOnlyFeeds, collapseEntriesBatchSize, params.IncludeLabeled).Scan(&n); err != nil {
		return 0, fmt.Errorf("collapse entries to hashes: %w", err)
	}
	return n, nil
}
