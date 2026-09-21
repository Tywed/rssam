package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
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
	// Rank orders by relevance among the SearchRankWindow newest hits; see
	// SearchEntries.
	Rank bool
	// WithTotal counts matches up to SearchTotalCap; a total equal to the
	// cap means "at least that many". Without it the total is
	// Offset+len(page), plus one when a further page exists.
	WithTotal bool
	// WithoutBody: see ListEntriesFilter.WithoutBody.
	WithoutBody bool
}

// SearchRankWindow is how many of the newest hits a ranked search orders
// by ts_rank_cd. Ranking needs the tsvector of every candidate, and on
// wide entries those live in TOAST: ranking all hits of a common word read
// 0.8–1.5 M buffers (200 000 entries, 626 MB; 355–750 ms even from cache).
// Ranking the newest 2 000 reads ≤ 25 k buffers (2–66 ms) and, for a feed
// reader, "most relevant among the recent" is the useful order anyway.
// 5 000 and 10 000 measured 86 and 110 ms for no visible gain.
const SearchRankWindow = 2000

// SearchTotalCap bounds the WithTotal count. Counting every hit of a
// common word costs as much as the search itself (345–360 ms on the set
// above); the UI and API only need the number to page and to say
// "10 000+".
const SearchTotalCap = 10001

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

	whereSQL := "\nWHERE " + strings.Join(where, " AND ")
	fetch := limit
	if !filter.WithTotal {
		fetch++
	}
	// The newest `window` hits are picked first and the page is cut from
	// them, sorted by date or, when Rank is set, by relevance. The ranked
	// window is fixed so that every page ranks the same candidate set;
	// paging past it yields nothing, like paging past the last hit. Only the GIN
	// index is allowed to find the hits: pg_stats keeps at most 1 000
	// common lexemes of a tsvector column, so any other word is estimated
	// at ~1 000 rows and the planner happily walks the whole table
	// through entries_user_sort_idx or a sequential scan, detoasting every
	// row on the way (405–856 ms and 0.8 M buffers for a word with 50
	// hits). With the two scan types off the same query runs the GIN
	// bitmap path in 0.5–58 ms; the settings are transaction-local.
	window := offset + fetch
	if filter.Rank {
		window = SearchRankWindow
	}
	orderBy := entryOrderClause(filter.Sort)
	q := "WITH hits AS MATERIALIZED (SELECT id FROM entries" + whereSQL +
		"\nORDER BY " + orderBy + "\nLIMIT " + fmt.Sprint(window) + ")\n" +
		"SELECT " + entryColumns(filter.WithoutBody) + "\nFROM entries\nWHERE id IN (SELECT id FROM hits)"
	if filter.Rank {
		orderBy = fmt.Sprintf("ts_rank_cd(search_vector, %s) DESC, ", tsq) + orderBy
	}
	q += "\nORDER BY " + orderBy +
		"\nLIMIT $" + fmt.Sprint(argN) + " OFFSET $" + fmt.Sprint(argN+1)

	var out []Entry
	total := 0
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off; SET LOCAL enable_indexscan = off"); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, q, append(args, fetch, offset)...)
		if err != nil {
			return err
		}
		if out, err = scanEntries(rows, fetch); err != nil {
			return err
		}
		if !filter.WithTotal {
			return nil
		}
		return tx.QueryRow(ctx, "SELECT count(*) FROM (SELECT 1 FROM entries"+whereSQL+
			"\nLIMIT "+fmt.Sprint(SearchTotalCap)+") c", args...).Scan(&total)
	})
	if err != nil {
		return nil, 0, fmt.Errorf("search entries: %w", err)
	}
	if !filter.WithTotal {
		out, total = trimPage(offset, limit, out)
	}
	return out, total, nil
}
