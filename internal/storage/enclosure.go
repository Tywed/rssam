package storage

import (
	"context"
	"fmt"
	"strings"
)

func (s *PostgresStore) CreateEnclosures(ctx context.Context, userID int64, entryID int64, enclosures []CreateEnclosureParams) error {
	if len(enclosures) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(enclosures))
	urls := make([]string, 0, len(enclosures))
	sizes := make([]int64, 0, len(enclosures))
	mimes := make([]string, 0, len(enclosures))
	for _, enc := range enclosures {
		url := strings.TrimSpace(enc.URL)
		if url == "" {
			continue
		}
		ids = append(ids, entryID)
		urls = append(urls, url)
		sizes = append(sizes, enc.Size)
		mimes = append(mimes, enc.MIMEType)
	}
	return s.createEnclosuresBatch(ctx, userID, ids, urls, sizes, mimes)
}

func (s *PostgresStore) createEnclosuresBatch(ctx context.Context, userID int64, entryIDs []int64, urls []string, sizes []int64, mimeTypes []string) error {
	if len(entryIDs) == 0 {
		return nil
	}
	const q = `
INSERT INTO enclosures(user_id, entry_id, url, size, mime_type)
SELECT $1, x.entry_id, x.url, x.size, x.mime_type
FROM unnest($2::bigint[], $3::text[], $4::bigint[], $5::text[]) AS x(entry_id, url, size, mime_type)`
	if _, err := s.db.Exec(ctx, q, userID, entryIDs, urls, sizes, mimeTypes); err != nil {
		return fmt.Errorf("create enclosures: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListEnclosuresByEntryIDs(ctx context.Context, userID int64, entryIDs []int64) (map[int64][]Enclosure, error) {
	out := make(map[int64][]Enclosure)
	if len(entryIDs) == 0 {
		return out, nil
	}

	const q = `
SELECT id, user_id, entry_id, url, size, mime_type, media_progression
FROM enclosures
WHERE user_id = $1 AND entry_id = ANY($2::bigint[])
ORDER BY id ASC`

	rows, err := s.db.Query(ctx, q, userID, entryIDs)
	if err != nil {
		return nil, fmt.Errorf("list enclosures: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var enc Enclosure
		if err := rows.Scan(&enc.ID, &enc.UserID, &enc.EntryID, &enc.URL, &enc.Size, &enc.MIMEType, &enc.MediaProgression); err != nil {
			return nil, fmt.Errorf("scan enclosure: %w", err)
		}
		out[enc.EntryID] = append(out[enc.EntryID], enc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enclosures: %w", err)
	}
	return out, nil
}
