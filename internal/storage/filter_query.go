package storage

import (
	"context"
	"fmt"
)

// QueryMatchItem is one entry to test against websearch queries. EntryID > 0
// reuses the stored search_vector; otherwise the vector is computed from
// Title/Content on the fly and never written.
type QueryMatchItem struct {
	EntryID int64
	Title   string
	Content string
}

// MatchEntryQueries returns hits[i][j] = item i matches queries[j], using the
// same text search configuration as the entries index.
func (s *PostgresStore) MatchEntryQueries(ctx context.Context, items []QueryMatchItem, queries []string) ([][]bool, error) {
	out := make([][]bool, len(items))
	for i := range out {
		out[i] = make([]bool, len(queries))
	}
	if len(items) == 0 || len(queries) == 0 {
		return out, nil
	}
	ids := make([]int64, len(items))
	titles := make([]string, len(items))
	contents := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.EntryID
		if it.EntryID <= 0 {
			titles[i] = it.Title
			contents[i] = it.Content
		}
	}
	// Each query is parsed once (MATERIALIZED), each stored entry is fetched
	// by primary key: ~1 ms for 50 entries × 3 queries on a 500k-row table.
	q := fmt.Sprintf(`
WITH q AS MATERIALIZED (
  SELECT %s AS tsq, ord FROM unnest($4::text[]) WITH ORDINALITY AS u(query, ord)
)
SELECT x.ord, q.ord
FROM unnest($1::bigint[], $2::text[], $3::text[]) WITH ORDINALITY AS x(id, title, content, ord)
LEFT JOIN entries e ON x.id > 0 AND e.id = x.id
CROSS JOIN q
WHERE COALESCE(e.search_vector, %s) @@ q.tsq`,
		ftsWebsearchExpr(s.ftsLanguage, "u.query"),
		ftsVectorExprPlaceholders(s.ftsLanguage, "x.title", "x.content"))
	rows, err := s.db.Query(ctx, q, ids, titles, contents, queries)
	if err != nil {
		return nil, fmt.Errorf("match entry queries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var i, j int
		if err := rows.Scan(&i, &j); err != nil {
			return nil, fmt.Errorf("scan entry query hit: %w", err)
		}
		if i >= 1 && i <= len(items) && j >= 1 && j <= len(queries) {
			out[i-1][j-1] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entry query hits: %w", err)
	}
	return out, nil
}
