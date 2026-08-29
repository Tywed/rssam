package filter

import (
	"strings"
	"testing"
	"time"

	"rssam/internal/storage"
)

func TestEngine_ClipsLongFields(t *testing.T) {
	eng := New(Config{})
	now := time.Now().UTC()
	f := storage.Filter{
		ID:        1,
		UpdatedAt: now,
		Rules: []storage.FilterRule{
			{ID: 1, Field: "content", Pattern: "^hay", Op: "and"},
		},
	}
	entry := storage.Entry{Content: "hay" + strings.Repeat("x", maxMatchFieldBytes)}
	matches, err := eng.MatchEntryStrict(entry, []storage.Filter{f})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected match on clipped prefix, got %d", len(matches))
	}
}
