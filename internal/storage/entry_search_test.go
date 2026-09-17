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

func TestFTSPrefixQuery(t *testing.T) {
	for in, want := range map[string]string{
		"постгр":          `'постгр':*`,
		"  постгр   нов ": `'постгр':* & 'нов':*`,
		"it's":            `'it''s':*`,
		`a\b`:             `'a\\b':*`,
		"a:*b & !c":       `'a:*b':* & '&' & '!c'`,
		"ip адрес":        `'ip' & 'адрес':*`,
		"яяя":             `'яяя':*`,
		"":                ``,
	} {
		if got := ftsPrefixQuery(in); got != want {
			t.Errorf("ftsPrefixQuery(%q) = %q, want %q", in, got, want)
		}
	}
}
