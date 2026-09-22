package migrations

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

const sharedFeedsMigration = "0050_shared_feeds.sql"

// SharedFeedsMerge describes one feed URL that 0050 collapses into a single
// catalog row: the row that stays, the rows that go and how many of their
// entries are the same item (equal hash) as one the keeper already has.
type SharedFeedsMerge struct {
	FeedURL      string
	KeepID       int64
	KeepOwnerID  int64
	LoseIDs      []int64
	LoseOwnerIDs []int64
	LoseEntries  int64
	SameEntries  int64
}

// SharedFeedsPlan lists what 0050 would merge. It is empty when the
// migration already ran (feeds.user_id is gone) or when no URL is shared.
func SharedFeedsPlan(ctx context.Context, db *pgxpool.Pool) ([]SharedFeedsMerge, error) {
	var legacy bool
	if err := db.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM information_schema.columns
  WHERE table_schema = current_schema() AND table_name = 'feeds' AND column_name = 'user_id')`).Scan(&legacy); err != nil {
		return nil, fmt.Errorf("check feeds schema: %w", err)
	}
	if !legacy {
		return nil, nil
	}
	rows, err := db.Query(ctx, `
WITH keep AS (
  SELECT DISTINCT ON (feed_url) id, user_id, feed_url FROM feeds ORDER BY feed_url, user_id, id
), lose AS (
  SELECT f.id, f.user_id, k.id AS keep_id, k.user_id AS keep_user_id, f.feed_url
  FROM feeds f JOIN keep k ON k.feed_url = f.feed_url AND k.id <> f.id
)
SELECT l.feed_url, l.keep_id, l.keep_user_id,
       array_agg(l.id ORDER BY l.id), array_agg(l.user_id ORDER BY l.id),
       COALESCE(sum((SELECT count(*) FROM entries e WHERE e.feed_id = l.id)), 0),
       COALESCE(sum((SELECT count(*) FROM entries le JOIN entries ke ON ke.feed_id = l.keep_id AND ke.hash = le.hash WHERE le.feed_id = l.id)), 0)
FROM lose l
GROUP BY l.feed_url, l.keep_id, l.keep_user_id
ORDER BY l.keep_id`)
	if err != nil {
		return nil, fmt.Errorf("plan shared feeds: %w", err)
	}
	defer rows.Close()
	var out []SharedFeedsMerge
	for rows.Next() {
		var m SharedFeedsMerge
		if err := rows.Scan(&m.FeedURL, &m.KeepID, &m.KeepOwnerID, &m.LoseIDs, &m.LoseOwnerIDs, &m.LoseEntries, &m.SameEntries); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func logSharedFeedsPlan(log *slog.Logger, plan []SharedFeedsMerge) {
	if len(plan) == 0 {
		log.Info("shared feeds: no duplicate URLs to merge")
		return
	}
	var feeds, entries, same int64
	for _, m := range plan {
		feeds += int64(len(m.LoseIDs))
		entries += m.LoseEntries
		same += m.SameEntries
		log.Info("shared feeds: merge", "url", m.FeedURL, "keep_id", m.KeepID, "keep_owner", m.KeepOwnerID,
			"merge_ids", m.LoseIDs, "merge_owners", m.LoseOwnerIDs, "entries_moved", m.LoseEntries-m.SameEntries, "entries_collapsed", m.SameEntries)
	}
	log.Info("shared feeds: summary", "urls", len(plan), "feeds_removed", feeds, "entries_moved", entries-same, "entries_collapsed", same)
}
