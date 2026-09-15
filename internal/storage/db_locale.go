package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DatabaseLocale describes the database's default character handling.
// LowerOK is the property the Russian text search configuration and ILIKE
// actually depend on: lower() folding a Cyrillic letter under the database
// ctype. It is probed directly because the answer depends on the locale
// provider (libc "C" fails, builtin "C.UTF-8" works) and not on the names.
type DatabaseLocale struct {
	Encoding string
	Collate  string
	Ctype    string
	LowerOK  bool
}

func ProbeDatabaseLocale(ctx context.Context, db *pgxpool.Pool) (DatabaseLocale, error) {
	const q = `SELECT pg_encoding_to_char(encoding), datcollate, datctype, lower('Ж') = 'ж'
FROM pg_database WHERE datname = current_database()`
	var l DatabaseLocale
	if err := db.QueryRow(ctx, q).Scan(&l.Encoding, &l.Collate, &l.Ctype, &l.LowerOK); err != nil {
		return DatabaseLocale{}, fmt.Errorf("probe database locale: %w", err)
	}
	return l, nil
}
