package storage

import (
	"context"
	"fmt"
	"strings"
)

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
	// WithTotal counts all matches; see ListEntriesFilter.WithTotal.
	WithTotal bool
	// WithoutBody: see ListEntriesFilter.WithoutBody.
	WithoutBody bool
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

	tsq := ftsWebsearchExpr(s.ftsLanguage, fmt.Sprintf("$%d", argN))
	args = append(args, query)
	argN++
	if !HasSearchOperators(query) {
		// Plain words also match as prefixes so that a stem the dictionary
		// does not know ("постгр", "kubern") or an unfinished word still
		// finds the entry. Both halves are answered by the GIN index; the
		// former `title/content ILIKE '%q%'` fallback forced a sequential
		// scan of the whole table (200 000 entries, 1 GB: 4.7–6.4 s and
		// 2.4 M buffers per search versus 0.4–25 ms and <10 k buffers now).
		tsq = fmt.Sprintf("(%s || %s)", tsq, ftsPrefixExpr(s.ftsLanguage, fmt.Sprintf("$%d", argN)))
		args = append(args, ftsPrefixQuery(query))
		argN++
	}
	where = append(where, "search_vector @@ "+tsq)

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
		orderBy = fmt.Sprintf("ts_rank_cd(search_vector, %s) DESC, ", tsq) + orderBy
	}

	whereSQL := "\nWHERE " + strings.Join(where, " AND ")
	fetch := limit
	if !filter.WithTotal {
		fetch++
	}
	q := "SELECT " + entryColumns(filter.WithoutBody) + "\nFROM entries" + whereSQL
	if !filter.Rank {
		// When the ORDER BY matches entries_user_sort_idx the planner walks
		// that index and tests search_vector on every row, betting the
		// LIMIT fills quickly; for a rare term that detoasts the whole
		// table (200 000 entries: 8–19 s). The materialized CTE pins the
		// plan to the GIN index instead (0.1–120 ms). A ranked ORDER BY
		// cannot use the sort index, so there the planner already picks
		// the GIN or a sequential scan on its own.
		q = "WITH hits AS MATERIALIZED (SELECT id FROM entries" + whereSQL + ")\n" +
			"SELECT " + entryColumns(filter.WithoutBody) + "\nFROM entries\nWHERE id IN (SELECT id FROM hits)"
	}
	q += "\nORDER BY " + orderBy +
		"\nLIMIT $" + fmt.Sprint(argN) + " OFFSET $" + fmt.Sprint(argN+1)

	rows, err := s.db.Query(ctx, q, append(args, fetch, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("search entries: %w", err)
	}
	out, err := scanEntries(rows, fetch)
	if err != nil {
		return nil, 0, fmt.Errorf("search entries: %w", err)
	}
	if !filter.WithTotal {
		out, total := trimPage(offset, limit, out)
		return out, total, nil
	}
	var total int
	if err := s.db.QueryRow(ctx, "SELECT count(*) FROM entries"+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count search entries: %w", err)
	}
	return out, total, nil
}
