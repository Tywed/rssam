package storage

import (
	"context"
	"fmt"
	"strings"
)

func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// SearchEntriesFilter controls FTS search over entries. Removed rows are
// excluded unless Status asks for them explicitly.
type SearchEntriesFilter struct {
	Query   string
	FeedID  *int64
	Status  *string
	Starred *bool
	Sort    string
	Limit   int
	Offset  int
	// Rank orders by ts_rank when true (default for search).
	Rank bool
}

func (s *PostgresStore) SearchEntries(ctx context.Context, userID int64, filter SearchEntriesFilter) ([]Entry, int, error) {
	query := strings.TrimSpace(filter.Query)
	if query == "" {
		return nil, 0, fmt.Errorf("search query is required")
	}

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

	where = append(where, fmt.Sprintf("user_id = $%d", argN))
	args = append(args, userID)
	argN++

	queryArg := fmt.Sprintf("$%d", argN)
	tsq := ftsWebsearchExpr(s.ftsLanguage, queryArg)
	args = append(args, query)
	argN++

	likeArg := fmt.Sprintf("$%d", argN)
	likePattern := "%" + escapeLikePattern(query) + "%"
	// FTS + ILIKE: plainto_tsquery does not error on quotes/operators;
	// ILIKE covers substrings that the stemmer misses (especially Cyrillic with simple config).
	where = append(where, fmt.Sprintf(`(
  search_vector @@ %s
  OR title ILIKE %s ESCAPE '\'
  OR coalesce(content, '') ILIKE %s ESCAPE '\'
)`, tsq, likeArg, likeArg))
	args = append(args, likePattern)
	argN++

	if filter.FeedID != nil {
		where = append(where, fmt.Sprintf("feed_id = $%d", argN))
		args = append(args, *filter.FeedID)
		argN++
	}
	if filter.Status != nil {
		if !IsValidEntryStatus(*filter.Status) {
			return nil, 0, fmt.Errorf("invalid entry status: %q", *filter.Status)
		}
		where = append(where, fmt.Sprintf("status = $%d", argN))
		args = append(args, *filter.Status)
		argN++
	} else {
		where = append(where, fmt.Sprintf("status <> $%d", argN))
		args = append(args, EntryStatusRemoved)
		argN++
	}
	if filter.Starred != nil {
		where = append(where, fmt.Sprintf("starred = $%d", argN))
		args = append(args, *filter.Starred)
		argN++
	}

	orderBy := entryOrderClause(filter.Sort)
	if filter.Rank {
		orderBy = fmt.Sprintf("CASE WHEN search_vector @@ %s THEN ts_rank_cd(search_vector, %s) ELSE 0 END DESC, ", tsq, ftsWebsearchExpr(s.ftsLanguage, queryArg)) + orderBy
	}

	args = append(args, limit, offset)

	q := `
SELECT ` + entrySelectColumns + `, count(*) OVER()
FROM entries
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY ` + orderBy + `
LIMIT $` + fmt.Sprint(argN) + ` OFFSET $` + fmt.Sprint(argN+1)

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("search entries: %w", err)
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
			return nil, 0, fmt.Errorf("scan search entries: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate search entries: %w", err)
	}
	return out, total, nil
}
