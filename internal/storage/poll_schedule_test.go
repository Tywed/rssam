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
		got := AdaptivePollInterval(base, tc.items, 0, max, 24*time.Hour)
		want := tc.want
		if tc.items == 14 {
			want = 6 * time.Hour
		}
		if got != want {
			t.Errorf("items=%d: got %s want %s", tc.items, got, want)
		}
	}
	if got := AdaptivePollInterval(time.Hour, 0, 0, time.Hour, 24*time.Hour); got != time.Hour {
		t.Errorf("max<=base must return base, got %s", got)
	}
	if got := AdaptivePollInterval(time.Hour, 3, 0, 30*time.Minute, 24*time.Hour); got != time.Hour {
		t.Errorf("max<base must return base, got %s", got)
	}
}

func TestAdaptivePollInterval_SilenceDoublesTowardsHardMax(t *testing.T) {
	const base, max, hardMax = 15 * time.Minute, 6 * time.Hour, 24 * time.Hour
	day := 24 * time.Hour
	cases := []struct {
		silent time.Duration
		want   time.Duration
	}{
		{0, max},        // count 0 but silence unknown → ceiling
		{6 * day, max},  // inside the first silent week
		{13 * day, max}, // still one full week
		{14 * day, 12 * time.Hour},
		{21 * day, hardMax},  // 6h → 12h → 24h
		{365 * day, hardMax}, // never past MAX_POLL_INTERVAL
	}
	for _, tc := range cases {
		if got := AdaptivePollInterval(base, 0, tc.silent, max, hardMax); got != tc.want {
			t.Errorf("silent=%s: got %s want %s", tc.silent, got, tc.want)
		}
	}
	// A hard max below the ceiling never lowers it.
	if got := AdaptivePollInterval(base, 0, 30*day, max, time.Hour); got != max {
		t.Errorf("hardMax<max: got %s want %s", got, max)
	}
	// One item in the window disables the silence stretch.
	if got := AdaptivePollInterval(base, 1, 30*day, max, hardMax); got != max {
		t.Errorf("items=1: got %s want %s", got, max)
	}
}
