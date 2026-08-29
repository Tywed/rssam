package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const filterSelectCols = `id, user_id, name, enabled, match_any_rule, inverse, order_id, feed_scope, match_count, created_at, updated_at`

func normalizeFeedScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case FilterFeedScopeInclude, FilterFeedScopeExclude:
		return strings.ToLower(strings.TrimSpace(scope))
	default:
		return FilterFeedScopeAll
	}
}

func (s *PostgresStore) ListFilters(ctx context.Context, userID int64, limit, offset int) ([]Filter, int, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
	}
	if offset < 0 {
		offset = 0
	}
	q := `
SELECT ` + filterSelectCols + `, count(*) OVER()
FROM filters
WHERE user_id = $1
ORDER BY order_id ASC, id ASC
LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, q, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list filters: %w", err)
	}
	defer rows.Close()

	out := make([]Filter, 0, limit)
	total := 0
	for rows.Next() {
		var f Filter
		if err := rows.Scan(&f.ID, &f.UserID, &f.Name, &f.Enabled, &f.MatchAnyRule, &f.Inverse, &f.OrderID, &f.FeedScope, &f.MatchCount, &f.CreatedAt, &f.UpdatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("scan filters: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate filters: %w", err)
	}
	return out, total, nil
}

func (s *PostgresStore) CreateFilter(ctx context.Context, params CreateFilterParams) (Filter, error) {
	params.Name = strings.TrimSpace(params.Name)
	if params.Name == "" {
		return Filter{}, errors.New("name is required")
	}
	feedScope := normalizeFeedScope(params.FeedScope)

	var out Filter
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		const q = `
INSERT INTO filters(user_id, name, enabled, match_any_rule, inverse, order_id, feed_scope)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING ` + filterSelectCols
		if err := tx.QueryRow(ctx, q, params.UserID, params.Name, params.Enabled, params.MatchAnyRule, params.Inverse, params.OrderID, feedScope).
			Scan(&out.ID, &out.UserID, &out.Name, &out.Enabled, &out.MatchAnyRule, &out.Inverse, &out.OrderID, &out.FeedScope, &out.MatchCount, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return fmt.Errorf("insert filter: %w", err)
		}
		if len(params.Rules) > 0 {
			if err := insertFilterRules(ctx, tx, out.ID, params.Rules); err != nil {
				return err
			}
		}
		if len(params.ScopeItems) > 0 {
			if err := insertFilterScopeItems(ctx, tx, out.ID, params.ScopeItems); err != nil {
				return err
			}
		}
		if len(params.Actions) > 0 {
			if err := insertFilterActions(ctx, tx, out.ID, params.Actions); err != nil {
				return err
			}
		}
		return s.loadFilterPartsTx(ctx, tx, &out)
	})
	if err != nil {
		return Filter{}, fmt.Errorf("create filter: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) GetFilter(ctx context.Context, userID int64, id int64) (Filter, error) {
	q := `
SELECT ` + filterSelectCols + `
FROM filters
WHERE id = $1 AND user_id = $2`
	var f Filter
	err := s.db.QueryRow(ctx, q, id, userID).Scan(&f.ID, &f.UserID, &f.Name, &f.Enabled, &f.MatchAnyRule, &f.Inverse, &f.OrderID, &f.FeedScope, &f.MatchCount, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Filter{}, ErrNotFound
		}
		return Filter{}, fmt.Errorf("get filter: %w", err)
	}
	if err := s.loadFilterParts(ctx, &f); err != nil {
		return Filter{}, err
	}
	return f, nil
}

func (s *PostgresStore) UpdateFilter(ctx context.Context, params UpdateFilterParams) (Filter, error) {
	params.Name = strings.TrimSpace(params.Name)
	if params.Name == "" {
		return Filter{}, errors.New("name is required")
	}
	feedScope := normalizeFeedScope(params.FeedScope)

	var out Filter
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		const q = `
UPDATE filters
SET name = $2,
    enabled = $3,
    match_any_rule = $4,
    inverse = $5,
    order_id = $6,
    feed_scope = $7,
    updated_at = now()
WHERE id = $1 AND user_id = $8
RETURNING ` + filterSelectCols
		if err := tx.QueryRow(ctx, q, params.ID, params.Name, params.Enabled, params.MatchAnyRule, params.Inverse, params.OrderID, feedScope, params.UserID).
			Scan(&out.ID, &out.UserID, &out.Name, &out.Enabled, &out.MatchAnyRule, &out.Inverse, &out.OrderID, &out.FeedScope, &out.MatchCount, &out.CreatedAt, &out.UpdatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("update filter: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM filter_rules WHERE filter_id = $1`, out.ID); err != nil {
			return fmt.Errorf("delete filter rules: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM filter_scope_items WHERE filter_id = $1`, out.ID); err != nil {
			return fmt.Errorf("delete filter scope items: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM filter_actions WHERE filter_id = $1`, out.ID); err != nil {
			return fmt.Errorf("delete filter actions: %w", err)
		}
		if len(params.Rules) > 0 {
			if err := insertFilterRules(ctx, tx, out.ID, params.Rules); err != nil {
				return err
			}
		}
		if len(params.ScopeItems) > 0 {
			if err := insertFilterScopeItems(ctx, tx, out.ID, params.ScopeItems); err != nil {
				return err
			}
		}
		if len(params.Actions) > 0 {
			if err := insertFilterActions(ctx, tx, out.ID, params.Actions); err != nil {
				return err
			}
		}
		return s.loadFilterPartsTx(ctx, tx, &out)
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Filter{}, ErrNotFound
		}
		return Filter{}, fmt.Errorf("update filter: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) DeleteFilter(ctx context.Context, userID int64, id int64) error {
	cmd, err := s.db.Exec(ctx, `DELETE FROM filters WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete filter: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) ListEnabledFilters(ctx context.Context, userID int64, limit int) ([]Filter, error) {
	if limit <= 0 {
		limit = 1000
	}
	const q = `
SELECT f.id, f.user_id, f.name, f.enabled, f.match_any_rule, f.inverse, f.order_id, f.feed_scope, f.match_count, f.created_at, f.updated_at,
       r.id, r.field, r.pattern, r.negate, r.op, r.priority, r.created_at
FROM filters f
LEFT JOIN filter_rules r ON r.filter_id = f.id
WHERE f.user_id = $1 AND f.enabled = TRUE
ORDER BY f.order_id ASC, f.id ASC, r.priority ASC, r.id ASC
LIMIT $2`
	rows, err := s.db.Query(ctx, q, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list enabled filters: %w", err)
	}
	defer rows.Close()

	byID := map[int64]*Filter{}
	order := make([]int64, 0, 32)
	for rows.Next() {
		var (
			fID int64
			f   Filter
			rID *int64
			r   FilterRule
		)
		var (
			rField    *string
			rPattern  *string
			rNegate   *bool
			rOp       *string
			rPriority *int
			rCreated  *time.Time
		)
		if err := rows.Scan(
			&fID, &f.UserID, &f.Name, &f.Enabled, &f.MatchAnyRule, &f.Inverse, &f.OrderID, &f.FeedScope, &f.MatchCount, &f.CreatedAt, &f.UpdatedAt,
			&rID, &rField, &rPattern, &rNegate, &rOp, &rPriority, &rCreated,
		); err != nil {
			return nil, fmt.Errorf("scan enabled filters: %w", err)
		}
		f.ID = fID

		ptr := byID[fID]
		if ptr == nil {
			cpy := f
			cpy.Rules = nil
			byID[fID] = &cpy
			order = append(order, fID)
			ptr = &cpy
			byID[fID] = ptr
		}
		if rID != nil {
			r.ID = *rID
			r.FilterID = fID
			if rField != nil {
				r.Field = *rField
			}
			if rPattern != nil {
				r.Pattern = *rPattern
			}
			if rNegate != nil {
				r.Negate = *rNegate
			}
			if rOp != nil {
				r.Op = *rOp
			}
			if rPriority != nil {
				r.Priority = *rPriority
			}
			if rCreated != nil {
				r.CreatedAt = *rCreated
			}
			ptr.Rules = append(ptr.Rules, r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled filters: %w", err)
	}

	out := make([]Filter, 0, len(order))
	for _, id := range order {
		f := *byID[id]
		if err := s.loadFilterScopeAndActions(ctx, &f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

func (s *PostgresStore) loadFilterParts(ctx context.Context, f *Filter) error {
	rules, err := s.getFilterRules(ctx, f.ID)
	if err != nil {
		return err
	}
	f.Rules = rules
	return s.loadFilterScopeAndActions(ctx, f)
}

func (s *PostgresStore) loadFilterPartsTx(ctx context.Context, tx pgx.Tx, f *Filter) error {
	rules, err := s.getFilterRulesTx(ctx, tx, f.ID)
	if err != nil {
		return err
	}
	f.Rules = rules
	scope, err := s.getFilterScopeItemsTx(ctx, tx, f.ID)
	if err != nil {
		return err
	}
	f.ScopeItems = scope
	actions, err := s.getFilterActionsTx(ctx, tx, f.ID)
	if err != nil {
		return err
	}
	f.Actions = actions
	return nil
}

func (s *PostgresStore) loadFilterScopeAndActions(ctx context.Context, f *Filter) error {
	scope, err := s.getFilterScopeItems(ctx, f.ID)
	if err != nil {
		return err
	}
	f.ScopeItems = scope
	actions, err := s.getFilterActions(ctx, f.ID)
	if err != nil {
		return err
	}
	f.Actions = actions
	return nil
}

func (s *PostgresStore) CreateFilterMatches(ctx context.Context, matches []CreateFilterMatchParams) (int, error) {
	if len(matches) == 0 {
		return 0, nil
	}
	var b strings.Builder
	args := make([]any, 0, 1+len(matches)*4)
	argN := 1
	b.WriteString(`INSERT INTO filter_matches(filter_id, entry_id, matched_at, details) VALUES `)
	for i, m := range matches {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "\n($%d, $%d, $%d, $%d::jsonb)", argN, argN+1, argN+2, argN+3)
		argN += 4
		var details any
		if len(m.Details) > 0 {
			details = m.Details
		}
		args = append(args, m.FilterID, m.EntryID, m.MatchedAt, details)
	}
	b.WriteString("\nON CONFLICT (filter_id, entry_id) DO NOTHING RETURNING filter_id")
	rows, err := s.db.Query(ctx, b.String(), args...)
	if err != nil {
		return 0, fmt.Errorf("create filter matches: %w", err)
	}
	defer rows.Close()

	counts := make(map[int64]int64)
	for rows.Next() {
		var filterID int64
		if err := rows.Scan(&filterID); err != nil {
			return 0, fmt.Errorf("scan filter match returning: %w", err)
		}
		counts[filterID]++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate filter match returning: %w", err)
	}
	inserted := 0
	for filterID, n := range counts {
		inserted += int(n)
		if err := s.IncrementFilterMatchCount(ctx, filterID, n); err != nil {
			return inserted, err
		}
	}
	return inserted, nil
}

func (s *PostgresStore) IncrementFilterMatchCount(ctx context.Context, filterID int64, delta int64) error {
	if filterID <= 0 || delta <= 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, `UPDATE filters SET match_count = match_count + $2 WHERE id = $1`, filterID, delta)
	if err != nil {
		return fmt.Errorf("increment filter match count: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListFilterMatches(ctx context.Context, filterID int64, limit, offset int) ([]FilterMatchWithEntry, int, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
	}
	if offset < 0 {
		offset = 0
	}
	const q = `
SELECT fm.id, fm.filter_id, fm.entry_id, fm.matched_at, fm.details,
       e.id, e.feed_id, e.title, e.url, e.content, e.author, e.published_at, e.hash, e.status, e.created_at, e.updated_at,
       count(*) OVER()
FROM filter_matches fm
JOIN entries e ON e.id = fm.entry_id
WHERE fm.filter_id = $1
ORDER BY fm.matched_at DESC, fm.id DESC
LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, q, filterID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list filter matches: %w", err)
	}
	defer rows.Close()

	out := make([]FilterMatchWithEntry, 0, limit)
	total := 0
	for rows.Next() {
		var row FilterMatchWithEntry
		if err := rows.Scan(
			&row.Match.ID,
			&row.Match.FilterID,
			&row.Match.EntryID,
			&row.Match.MatchedAt,
			&row.Match.Details,
			&row.Entry.ID,
			&row.Entry.FeedID,
			&row.Entry.Title,
			&row.Entry.URL,
			&row.Entry.Content,
			&row.Entry.Author,
			&row.Entry.PublishedAt,
			&row.Entry.Hash,
			&row.Entry.Status,
			&row.Entry.CreatedAt,
			&row.Entry.UpdatedAt,
			&total,
		); err != nil {
			return nil, 0, fmt.Errorf("scan filter matches: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate filter matches: %w", err)
	}
	return out, total, nil
}

func (s *PostgresStore) getFilterRules(ctx context.Context, filterID int64) ([]FilterRule, error) {
	const q = `
SELECT id, filter_id, field, pattern, negate, op, priority, created_at
FROM filter_rules
WHERE filter_id = $1
ORDER BY priority ASC, id ASC`
	rows, err := s.db.Query(ctx, q, filterID)
	if err != nil {
		return nil, fmt.Errorf("get filter rules: %w", err)
	}
	defer rows.Close()
	out := make([]FilterRule, 0, 16)
	for rows.Next() {
		var r FilterRule
		if err := rows.Scan(&r.ID, &r.FilterID, &r.Field, &r.Pattern, &r.Negate, &r.Op, &r.Priority, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan filter rule: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate filter rules: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) getFilterRulesTx(ctx context.Context, tx pgx.Tx, filterID int64) ([]FilterRule, error) {
	const q = `
SELECT id, filter_id, field, pattern, negate, op, priority, created_at
FROM filter_rules
WHERE filter_id = $1
ORDER BY priority ASC, id ASC`
	rows, err := tx.Query(ctx, q, filterID)
	if err != nil {
		return nil, fmt.Errorf("get filter rules: %w", err)
	}
	defer rows.Close()
	out := make([]FilterRule, 0, 16)
	for rows.Next() {
		var r FilterRule
		if err := rows.Scan(&r.ID, &r.FilterID, &r.Field, &r.Pattern, &r.Negate, &r.Op, &r.Priority, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan filter rule: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate filter rules: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) getFilterScopeItems(ctx context.Context, filterID int64) ([]FilterScopeItem, error) {
	const q = `
SELECT id, filter_id, feed_id, category_id, created_at
FROM filter_scope_items
WHERE filter_id = $1
ORDER BY id ASC`
	rows, err := s.db.Query(ctx, q, filterID)
	if err != nil {
		return nil, fmt.Errorf("get filter scope items: %w", err)
	}
	defer rows.Close()
	return scanFilterScopeItems(rows)
}

func (s *PostgresStore) getFilterScopeItemsTx(ctx context.Context, tx pgx.Tx, filterID int64) ([]FilterScopeItem, error) {
	const q = `
SELECT id, filter_id, feed_id, category_id, created_at
FROM filter_scope_items
WHERE filter_id = $1
ORDER BY id ASC`
	rows, err := tx.Query(ctx, q, filterID)
	if err != nil {
		return nil, fmt.Errorf("get filter scope items: %w", err)
	}
	defer rows.Close()
	return scanFilterScopeItems(rows)
}

func scanFilterScopeItems(rows pgx.Rows) ([]FilterScopeItem, error) {
	out := make([]FilterScopeItem, 0, 8)
	for rows.Next() {
		var item FilterScopeItem
		if err := rows.Scan(&item.ID, &item.FilterID, &item.FeedID, &item.CategoryID, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan filter scope item: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate filter scope items: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) getFilterActions(ctx context.Context, filterID int64) ([]FilterAction, error) {
	const q = `
SELECT id, filter_id, action_type, action_param, priority, created_at
FROM filter_actions
WHERE filter_id = $1
ORDER BY priority ASC, id ASC`
	rows, err := s.db.Query(ctx, q, filterID)
	if err != nil {
		return nil, fmt.Errorf("get filter actions: %w", err)
	}
	defer rows.Close()
	return scanFilterActions(rows)
}

func (s *PostgresStore) getFilterActionsTx(ctx context.Context, tx pgx.Tx, filterID int64) ([]FilterAction, error) {
	const q = `
SELECT id, filter_id, action_type, action_param, priority, created_at
FROM filter_actions
WHERE filter_id = $1
ORDER BY priority ASC, id ASC`
	rows, err := tx.Query(ctx, q, filterID)
	if err != nil {
		return nil, fmt.Errorf("get filter actions: %w", err)
	}
	defer rows.Close()
	return scanFilterActions(rows)
}

func scanFilterActions(rows pgx.Rows) ([]FilterAction, error) {
	out := make([]FilterAction, 0, 8)
	for rows.Next() {
		var a FilterAction
		if err := rows.Scan(&a.ID, &a.FilterID, &a.ActionType, &a.ActionParam, &a.Priority, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan filter action: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate filter actions: %w", err)
	}
	return out, nil
}

func insertFilterRules(ctx context.Context, tx pgx.Tx, filterID int64, rules []CreateFilterRuleParams) error {
	var b strings.Builder
	args := make([]any, 0, len(rules)*6)
	argN := 1
	b.WriteString(`INSERT INTO filter_rules(filter_id, field, pattern, negate, op, priority) VALUES `)
	for i, r := range rules {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "\n($%d, $%d, $%d, $%d, $%d, $%d)", argN, argN+1, argN+2, argN+3, argN+4, argN+5)
		argN += 6
		args = append(args, filterID, strings.TrimSpace(r.Field), r.Pattern, r.Negate, strings.ToLower(strings.TrimSpace(r.Op)), r.Priority)
	}
	if _, err := tx.Exec(ctx, b.String(), args...); err != nil {
		return fmt.Errorf("insert filter rules: %w", err)
	}
	return nil
}

func insertFilterScopeItems(ctx context.Context, tx pgx.Tx, filterID int64, items []CreateFilterScopeItemParams) error {
	for _, item := range items {
		if item.FeedID == nil && item.CategoryID == nil {
			continue
		}
		const q = `INSERT INTO filter_scope_items(filter_id, feed_id, category_id) VALUES ($1, $2, $3)`
		if _, err := tx.Exec(ctx, q, filterID, item.FeedID, item.CategoryID); err != nil {
			return fmt.Errorf("insert filter scope item: %w", err)
		}
	}
	return nil
}

func insertFilterActions(ctx context.Context, tx pgx.Tx, filterID int64, actions []CreateFilterActionParams) error {
	for _, a := range actions {
		actionType := strings.ToLower(strings.TrimSpace(a.ActionType))
		if actionType == "" {
			continue
		}
		const q = `INSERT INTO filter_actions(filter_id, action_type, action_param, priority) VALUES ($1, $2, $3, $4)`
		if _, err := tx.Exec(ctx, q, filterID, actionType, strings.TrimSpace(a.ActionParam), a.Priority); err != nil {
			return fmt.Errorf("insert filter action: %w", err)
		}
	}
	return nil
}
