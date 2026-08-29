package storage

import (
	"context"
	"testing"
)

func TestSearchEntries_requiresQuery(t *testing.T) {
	s := &PostgresStore{ftsLanguage: "simple"}
	_, _, err := s.SearchEntries(context.Background(), 1, SearchEntriesFilter{Query: "  "})
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}
