package storage

import (
	"context"
	"fmt"
)

// EnsureFeedPollStateRows gives every feed its feed_poll_state row. The
// table is UNLOGGED (0071): a PostgreSQL crash leaves it empty, and the
// catalog queries join it without an outer join, so the feeds would vanish
// from lists and from the poll schedule until the rows are back.
func (s *PostgresStore) EnsureFeedPollStateRows(ctx context.Context) (int64, error) {
	cmd, err := s.db.Exec(ctx, `
INSERT INTO feed_poll_state (feed_id)
SELECT f.id FROM feeds f WHERE NOT EXISTS (SELECT 1 FROM feed_poll_state p WHERE p.feed_id = f.id)`)
	if err != nil {
		return 0, fmt.Errorf("ensure feed poll state rows: %w", err)
	}
	return cmd.RowsAffected(), nil
}
