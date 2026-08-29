package ui

import (
	"fmt"

	"rssam/internal/storage"
)

func feedHasError(f storage.Feed) bool {
	return f.LastError != "" || f.PollPaused || f.ManualPaused || f.ParsingErrorCount > 0
}

func feedIsInactive(f storage.Feed) bool {
	return f.PollPaused || f.ManualPaused
}

func categoryFeedCounts(feeds []storage.Feed) (map[int64]int, int) {
	counts := make(map[int64]int)
	uncategorized := 0
	for _, f := range feeds {
		if f.CategoryID == nil {
			uncategorized++
			continue
		}
		counts[*f.CategoryID]++
	}
	return counts, uncategorized
}

func filterFeeds(feeds []storage.Feed, filter string) []storage.Feed {
	if filter == "" {
		out := make([]storage.Feed, len(feeds))
		copy(out, feeds)
		return out
	}
	out := make([]storage.Feed, 0, len(feeds))
	for _, f := range feeds {
		switch filter {
		case "errors":
			if feedHasError(f) {
				out = append(out, f)
			}
		case "inactive":
			if feedIsInactive(f) {
				out = append(out, f)
			}
		}
	}
	return out
}

func countFeedStatuses(feeds []storage.Feed) (errors, inactive int) {
	for _, f := range feeds {
		if feedHasError(f) {
			errors++
		}
		if feedIsInactive(f) {
			inactive++
		}
	}
	return errors, inactive
}

func pluralChannels(n int) string {
	if n < 0 {
		n = 0
	}
	mod100 := n % 100
	if mod100 >= 11 && mod100 <= 19 {
		return fmt.Sprintf("(%d каналов)", n)
	}
	switch n % 10 {
	case 1:
		return fmt.Sprintf("(%d канал)", n)
	case 2, 3, 4:
		return fmt.Sprintf("(%d канала)", n)
	default:
		return fmt.Sprintf("(%d каналов)", n)
	}
}

func categoryFeedCount(counts map[int64]int, catID int64) int {
	if counts == nil {
		return 0
	}
	return counts[catID]
}

func feedIDsInCategory(feeds []storage.Feed, categoryID int64) []int64 {
	var ids []int64
	for _, f := range feeds {
		if categoryID == 0 {
			if f.CategoryID == nil {
				ids = append(ids, f.ID)
			}
			continue
		}
		if f.CategoryID != nil && *f.CategoryID == categoryID {
			ids = append(ids, f.ID)
		}
	}
	return ids
}
