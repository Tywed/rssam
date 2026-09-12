package storage

import (
	"testing"
	"time"
)

func TestFeedNextCheckAt_OneMinuteInterval(t *testing.T) {
	from := time.Date(2026, 8, 13, 18, 28, 0, 0, time.UTC)
	next := FeedNextCheckAt(from, 1, time.Minute, 24*time.Hour)
	want := from.Add(time.Minute)
	if !next.Equal(want) {
		t.Fatalf("next=%s, want %s", next, want)
	}
}

func TestFeedNextCheckAt_ClampedToMin(t *testing.T) {
	from := time.Date(2026, 8, 13, 18, 28, 0, 0, time.UTC)
	next := FeedNextCheckAt(from, 0, time.Minute, 24*time.Hour)
	want := from.Add(time.Minute)
	if !next.Equal(want) {
		t.Fatalf("next=%s, want %s", next, want)
	}
}

func TestFeedErrorPollDelay_DoublesPerError(t *testing.T) {
	min := time.Minute
	max := 24 * time.Hour
	if got := FeedErrorPollDelay(5, 1, min, max); got != 10*time.Minute {
		t.Fatalf("1 error: got %s, want 10m", got)
	}
	if got := FeedErrorPollDelay(5, 2, min, max); got != 20*time.Minute {
		t.Fatalf("2 errors: got %s, want 20m", got)
	}
	if got := FeedErrorPollDelay(5, 3, min, max); got != 40*time.Minute {
		t.Fatalf("3 errors: got %s, want 40m", got)
	}
}

func TestFeedErrorPollDelay_ClampedToMax(t *testing.T) {
	max := time.Hour
	got := FeedErrorPollDelay(5, 10, time.Minute, max)
	if got != max {
		t.Fatalf("got %s, want max %s", got, max)
	}
}

func TestAdaptivePollInterval(t *testing.T) {
	base := 15 * time.Minute
	max := 12 * time.Hour
	cases := []struct {
		items int
		want  time.Duration
	}{
		{0, max},                // silent week → ceiling
		{1, max},                // 1/week → gap 7d, half 3.5d → ceiling
		{14, max},               // 2/day → gap 12h, half 6h
		{168, 30 * time.Minute}, // 1/hour → half 30m
		{336, base},             // 2/hour → 15m = base
		{100000, base},          // firehose → floor
	}
	for _, tc := range cases {
		got := AdaptivePollInterval(base, tc.items, max)
		want := tc.want
		if tc.items == 14 {
			want = 6 * time.Hour
		}
		if got != want {
			t.Errorf("items=%d: got %s want %s", tc.items, got, want)
		}
	}
	if got := AdaptivePollInterval(time.Hour, 0, time.Hour); got != time.Hour {
		t.Errorf("max<=base must return base, got %s", got)
	}
	if got := AdaptivePollInterval(time.Hour, 3, 30*time.Minute); got != time.Hour {
		t.Errorf("max<base must return base, got %s", got)
	}
}
