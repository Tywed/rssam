package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.sql
var embedded embed.FS

// migrationLockKey is an arbitrary but fixed pg_advisory_lock key for rssam.
const migrationLockKey int64 = 0x7273_7361_6d6d_6967 // "rssammig"

func Apply(ctx context.Context, db *pgxpool.Pool, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}

	// Serialise concurrent migrators (two instances starting at once, or a
	// deploy hook racing the service). The session-level advisory lock is
	// held on a dedicated connection for the whole run and released on return.
	conn, err := db.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockKey)
	}()

	if err := ensureMigrationsTable(ctx, db); err != nil {
		return err
	}

	files, err := listSQLFiles(embedded)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		log.Info("no migrations to apply")
		return nil
	}

	applied, err := loadApplied(ctx, db)
	if err != nil {
		return err
	}

	for _, name := range files {
		if applied[name] {
			continue
		}

		body, err := fs.ReadFile(embedded, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		sql := strings.TrimSpace(string(body))
		if sql == "" {
			log.Warn("skipping empty migration", "name", name)
			if err := markApplied(ctx, db, name); err != nil {
				return err
			}
			continue
		}

		log.Info("applying migration", "name", name)
		if err := applyOne(ctx, db, name, sql); err != nil {
			return err
		}
	}

	return nil
}

func listSQLFiles(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func ensureMigrationsTable(ctx context.Context, db *pgxpool.Pool) error {
	const q = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);`
	_, err := db.Exec(ctx, q)
	if err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}
	return nil
}

func loadApplied(ctx context.Context, db *pgxpool.Pool) (map[string]bool, error) {
	rows, err := db.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("select schema_migrations: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		out[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}
	return out, nil
}

func applyOne(ctx context.Context, db *pgxpool.Pool, name, sql string) error {
	txCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	return withTx(txCtx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(txCtx, sql); err != nil {
			return fmt.Errorf("exec %s: %w", name, err)
		}
		if _, err := tx.Exec(txCtx, `INSERT INTO schema_migrations(version) VALUES ($1)`, name); err != nil {
			return fmt.Errorf("record %s: %w", name, err)
		}
		return nil
	})
}

func markApplied(ctx context.Context, db *pgxpool.Pool, name string) error {
	_, err := db.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1) ON CONFLICT DO NOTHING`, name)
	if err != nil {
		return fmt.Errorf("record %s: %w", name, err)
	}
	return nil
}

func withTx(ctx context.Context, db *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Source returns the SQL of one embedded migration; tests replay data
// migrations against rows created after they were applied.
func Source(name string) (string, error) {
	body, err := fs.ReadFile(embedded, name)
	if err != nil {
		return "", fmt.Errorf("read migration %s: %w", name, err)
	}
	return string(body), nil
}
