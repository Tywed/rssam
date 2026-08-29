package filter

import (
	"testing"
	"time"

	"rssam/internal/storage"
)

func BenchmarkEngine_MatchEntry(b *testing.B) {
	eng := New(Config{MaxRulesPerFilter: 50, MaxRegexLength: 2048})
	now := time.Now().UTC()
	filters := []storage.Filter{
		{
			ID:        1,
			UpdatedAt: now,
			Rules: []storage.FilterRule{
				{ID: 1, Field: "title", Pattern: `(?i)news`, Op: "or"},
				{ID: 2, Field: "content", Pattern: `(?i)alert`, Op: "or"},
			},
		},
		{
			ID:        2,
			UpdatedAt: now,
			Rules: []storage.FilterRule{
				{ID: 3, Field: "title", Pattern: `release`, Op: "and"},
				{ID: 4, Field: "url", Pattern: `https?://`, Op: "and"},
			},
		},
	}
	entry := storage.Entry{
		Title:   "Breaking news: product release",
		Content: "Security alert for subscribers",
		URL:     "https://example.com/post/1",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = eng.MatchEntry(entry, filters)
	}
}
