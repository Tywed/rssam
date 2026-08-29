package maxstat

import "time"

// ShouldSkipPost returns true when the post was already covered by the cursor.
func ShouldSkipPost(postUnix, lastEndTime int64) bool {
	return lastEndTime > 0 && postUnix <= lastEndTime
}

// MaxPublishedUnix returns the latest published_at unix in posts.
func MaxPublishedUnix(posts []post, parse func(string) (time.Time, bool)) int64 {
	var max int64
	for _, p := range posts {
		t, ok := parse(p.PublishedAt)
		if !ok {
			continue
		}
		u := t.Unix()
		if u > max {
			max = u
		}
	}
	return max
}
