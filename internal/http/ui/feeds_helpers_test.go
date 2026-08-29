package ui

import (
	"testing"

	"rssam/internal/storage"
)

func TestFeedIDsInCategory(t *testing.T) {
	catA := int64(10)
	catB := int64(20)
	feeds := []storage.Feed{
		{ID: 1, CategoryID: &catA},
		{ID: 2, CategoryID: &catB},
		{ID: 3},
	}
	ids := feedIDsInCategory(feeds, catA)
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("catA: got %v", ids)
	}
	ids = feedIDsInCategory(feeds, 0)
	if len(ids) != 1 || ids[0] != 3 {
		t.Fatalf("uncategorized: got %v", ids)
	}
}
