package vk

import "time"

// TimeWindow is the newsfeed.search start_time / end_time pair (unix seconds).
type TimeWindow struct {
	StartTime int64
	EndTime   int64
}

// ComputeTimeWindow returns the API time range for this fetch.
// First run: end=now, start=now-lookback. Next: start=lastEnd-overlap, end=now.
func ComputeTimeWindow(lastEndTime int64, lookback, overlap time.Duration, now time.Time) TimeWindow {
	now = now.UTC()
	end := now.Unix()
	if lastEndTime > 0 {
		start := lastEndTime - int64(overlap.Seconds())
		if start < 0 {
			start = 0
		}
		return TimeWindow{StartTime: start, EndTime: end}
	}
	lookbackSec := int64(lookback.Seconds())
	if lookbackSec <= 0 {
		lookbackSec = 86400
	}
	start := end - lookbackSec
	if start < 0 {
		start = 0
	}
	return TimeWindow{StartTime: start, EndTime: end}
}

// ShouldSkipPost returns true when the post was already covered by the cursor.
func ShouldSkipPost(postDate, lastEndTime int64) bool {
	return lastEndTime > 0 && postDate <= lastEndTime
}
