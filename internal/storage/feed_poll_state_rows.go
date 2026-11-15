package storage

import (
	"context"
	"fmt"
)

// EnsureFeedPollStateRows gives every feed its feed_poll_state row. The
// table is UNLOGGED (0071): a PostgreSQL crash leaves it empty, and the
// catalog queries join it without an outer join, so the feeds would vanish
// from lists and from the poll schedule until the rows are back. New rows
// are due within the next 30 minutes at random: after a crash the whole
// fleet (2 250 feeds on the production instance) would otherwise be due at
// once. A feed created normally gets its row from the insert trigger and
// is not delayed. last_entry_at is rebuilt from the entries so that the
// silent / dormant tiers and their alerts do not misfire after the crash.
func (s *PostgresStore) EnsureFeedPollStateRows(ctx context.Context) (int64, error) {
	cmd, err := s.db.Exec(ctx, `
INSERT INTO feed_poll_state (feed_id, next_check_at, last_entry_at)
SELECT f.id, now() + random() * interval '30 minutes',
       (SELECT max(e.created_at) FROM entries e WHERE e.feed_id = f.id)
FROM feeds f WHERE NOT EXISTS (SELECT 1 FROM feed_poll_state p WHERE p.feed_id = f.id)`)
	if err != nil {
		return 0, fmt.Errorf("ensure feed poll state rows: %w", err)
	}
	return cmd.RowsAffected(), nil
}
