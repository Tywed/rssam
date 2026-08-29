package httpserver

import (
	"context"

	"rssam/internal/storage"
)

type enclosureDTO struct {
	URL      string `json:"url"`
	Size     int64  `json:"size"`
	MIMEType string `json:"mime_type"`
}

func (s *Server) entryToDTO(ctx context.Context, userID int64, e storage.Entry) (entryDTO, error) {
	m, err := s.entries.ListEnclosuresByEntryIDs(ctx, userID, []int64{e.ID})
	if err != nil {
		return entryDTO{}, err
	}
	return toEntryDTO(e, m[e.ID]), nil
}

func (s *Server) entriesToDTOs(ctx context.Context, userID int64, entries []storage.Entry) ([]entryDTO, error) {
	if len(entries) == 0 {
		return []entryDTO{}, nil
	}
	ids := make([]int64, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	m, err := s.entries.ListEnclosuresByEntryIDs(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]entryDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, toEntryDTO(e, m[e.ID]))
	}
	return out, nil
}

func toEnclosureDTOs(encs []storage.Enclosure) []enclosureDTO {
	if len(encs) == 0 {
		return nil
	}
	out := make([]enclosureDTO, 0, len(encs))
	for _, enc := range encs {
		out = append(out, enclosureDTO{
			URL:      enc.URL,
			Size:     enc.Size,
			MIMEType: enc.MIMEType,
		})
	}
	return out
}
