package storage

import (
	"context"
	"fmt"
)

func (s *PostgresStore) createEnclosuresBatch(ctx context.Context, entryIDs []int64, urls []string, sizes []int64, mimeTypes []string) error {
	if len(entryIDs) == 0 {
		return nil
	}
	const q = `
INSERT INTO enclosures(entry_id, url, size, mime_type)
SELECT x.entry_id, x.url, x.size, x.mime_type
FROM unnest($1::bigint[], $2::text[], $3::bigint[], $4::text[]) AS x(entry_id, url, size, mime_type)`
	if _, err := s.db.Exec(ctx, q, entryIDs, urls, sizes, mimeTypes); err != nil {
		return fmt.Errorf("create enclosures: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListEnclosuresByEntryIDs(ctx context.Context, entryIDs []int64) (map[int64][]Enclosure, error) {
	out := make(map[int64][]Enclosure)
	if len(entryIDs) == 0 {
		return out, nil
	}

	const q = `
SELECT id, entry_id, url, size, mime_type, media_progression
FROM enclosures
WHERE entry_id = ANY($1::bigint[])
ORDER BY id ASC`

	rows, err := s.db.Query(ctx, q, entryIDs)
	if err != nil {
		return nil, fmt.Errorf("list enclosures: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var enc Enclosure
		if err := rows.Scan(&enc.ID, &enc.EntryID, &enc.URL, &enc.Size, &enc.MIMEType, &enc.MediaProgression); err != nil {
			return nil, fmt.Errorf("scan enclosure: %w", err)
		}
		out[enc.EntryID] = append(out[enc.EntryID], enc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enclosures: %w", err)
	}
	return out, nil
}
