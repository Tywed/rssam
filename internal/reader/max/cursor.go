package max

import (
	"crypto/sha1"
	"encoding/hex"
	"time"
)

const (
	defaultLookback = 24 * time.Hour
	overlap         = 2 * time.Minute
)

// CursorParams configures incremental fetch windows.
type CursorParams struct {
	DefaultLookback time.Duration
	Overlap         time.Duration
	NowTime         time.Time
}

func (p CursorParams) lookback() time.Duration {
	if p.DefaultLookback > 0 {
		return p.DefaultLookback
	}
	return defaultLookback
}

func (p CursorParams) overlap() time.Duration {
	if p.Overlap > 0 {
		return p.Overlap
	}
	return overlap
}

func (p CursorParams) now() time.Time {
	if !p.NowTime.IsZero() {
		return p.NowTime.UTC()
	}
	return time.Now().UTC()
}

// ComputeAfter returns the `after` query parameter (milliseconds) and endTimeMs for this fetch.
func ComputeAfter(lastEndTimeMs int64, p CursorParams) (afterMs, endTimeMs int64) {
	now := p.now()
	endTimeMs = now.UnixMilli()
	if lastEndTimeMs > 0 {
		afterMs = max(lastEndTimeMs-p.overlap().Milliseconds(), 0)
		return afterMs, endTimeMs
	}
	afterMs = max(endTimeMs-p.lookback().Milliseconds(), 0)
	return afterMs, endTimeMs
}

// ShouldSkipMessage returns true when the message was already covered by the cursor.
func ShouldSkipMessage(msgTimeMs, lastEndTimeMs int64) bool {
	return lastEndTimeMs > 0 && msgTimeMs <= lastEndTimeMs
}

// CursorStateKey returns the PHP-compatible cache key (for logging/tests).
func CursorStateKey(channelName string) string {
	sum := sha1.Sum([]byte(channelName))
	return "cursor_" + hex.EncodeToString(sum[:])
}
