package storage

import "time"

func clampPollDuration(d, min, max time.Duration) time.Duration {
	if min > 0 && d < min {
		return min
	}
	if max > 0 && d > max {
		return max
	}
	return d
}

// FeedNextCheckAt returns when a feed should be polled again after a successful refresh.
func FeedNextCheckAt(from time.Time, intervalMinutes int, min, max time.Duration) time.Time {
	base := feedBasePollInterval(intervalMinutes, min, max)
	return from.UTC().Add(base)
}

// FeedErrorPollDelay returns backoff duration after a poll failure: base interval × 2^errorCount.
// errorCount is parsing_error_count after the failure was recorded (1 → 2×, 2 → 4×, …).
func FeedErrorPollDelay(intervalMinutes, errorCount int, min, max time.Duration) time.Duration {
	if errorCount <= 0 {
		errorCount = 1
	}
	base := feedBasePollInterval(intervalMinutes, min, max)
	shift := errorCount
	if shift > 30 {
		shift = 30
	}
	mult := int64(1) << uint(shift)
	delay := time.Duration(int64(base) * mult)
	return clampPollDuration(delay, min, max)
}

// FeedNextCheckAfterError returns when to retry after errorCount consecutive failures.
func FeedNextCheckAfterError(from time.Time, intervalMinutes, errorCount int, min, max time.Duration) time.Time {
	return from.UTC().Add(FeedErrorPollDelay(intervalMinutes, errorCount, min, max))
}

func feedBasePollInterval(intervalMinutes int, min, max time.Duration) time.Duration {
	base := time.Duration(intervalMinutes) * time.Minute
	return clampPollDuration(base, min, max)
}

// AdaptivePollWindow is the look-back used to estimate a feed's publishing rate.
const AdaptivePollWindow = 7 * 24 * time.Hour

// AdaptivePollInterval stretches base towards max for feeds that publish
// rarely: the interval is half the average gap between items over the last
// week (so a new item waits at most ~half a gap on average), never below
// base and never above max. A feed with no items in the window sits at max;
// max <= base disables the stretch.
func AdaptivePollInterval(base time.Duration, weeklyItems int, max time.Duration) time.Duration {
	if max <= base {
		return base
	}
	if weeklyItems <= 0 {
		return max
	}
	d := time.Duration(int64(AdaptivePollWindow) / int64(weeklyItems) / 2)
	return clampPollDuration(d, base, max)
}
