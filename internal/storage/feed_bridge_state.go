package storage

import (
	"context"
	"fmt"
)

// upsertBridgeState stores a feed's bridge cursor. feed_bridge_state is a
// logged table (unlike feed_poll_state): Telegram / MAX / VK cursors and
// page hashes survive a PostgreSQL crash. An unchanged document is filtered
// out before the INSERT: the ON CONFLICT ... WHERE form would still take a
// row lock and dirty the page (96 B of WAL against 56 B measured here).
// Only the poller that claimed the feed writes its row, so the check and
// the insert need no further serialisation.
func upsertBridgeState(ctx context.Context, db dbExecutor, feedID int64, state []byte) error {
	if len(state) == 0 {
		return nil
	}
	_, err := db.Exec(ctx, `
INSERT INTO feed_bridge_state (feed_id, state)
SELECT $1, $2::jsonb
WHERE NOT EXISTS (SELECT 1 FROM feed_bridge_state WHERE feed_id = $1 AND state = $2::jsonb)
ON CONFLICT (feed_id) DO UPDATE SET state = EXCLUDED.state`, feedID, state)
	if err != nil {
		return fmt.Errorf("upsert feed bridge state: %w", err)
	}
	return nil
}
