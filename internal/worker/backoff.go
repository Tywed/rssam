package worker

import (
	"time"
)

func clampDuration(d, min, max time.Duration) time.Duration {
	if min > 0 && d < min {
		return min
	}
	if max > 0 && d > max {
		return max
	}
	return d
}

// Backoff returns exponential backoff for the given attempt number (1..N).
func Backoff(attempt int, min, max time.Duration) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	if min <= 0 {
		min = time.Second
	}
	d := min
	for i := 1; i < attempt; i++ {
		if d > max/2 && max > 0 {
			d = max
			break
		}
		d *= 2
	}
	return clampDuration(d, min, max)
}
