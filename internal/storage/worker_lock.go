package storage

import (
	"context"
	"fmt"
)

// workerLockKey is the first half of the advisory-lock key; the second is
// hashtext(current_schema()) so isolated test schemas do not contend.
const workerLockKey int32 = 0x7273_776b // "rswk"

// TryWorkerLock takes the session-level advisory lock that marks this
// process as the one running the schedulers and worker pools against this
// database. The lock lives on a dedicated pooled connection until release
// is called; if that connection drops the lock is gone with it, so this
// guards against a duplicated deployment, not against a split brain.
func (s *PostgresStore) TryWorkerLock(ctx context.Context) (release func(), ok bool, err error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire worker lock connection: %w", err)
	}
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1, hashtext(current_schema()))`, workerLockKey).Scan(&ok); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("try worker lock: %w", err)
	}
	if !ok {
		conn.Release()
		return nil, false, nil
	}
	release = func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1, hashtext(current_schema()))`, workerLockKey)
		conn.Release()
	}
	return release, true, nil
}
