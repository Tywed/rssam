package worker

import (
	"testing"
	"time"
)

func TestBackoff_ExponentialClamped(t *testing.T) {
	min := 10 * time.Second
	max := 2 * time.Minute

	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: 10 * time.Second},
		{attempt: 1, want: 10 * time.Second},
		{attempt: 2, want: 20 * time.Second},
		{attempt: 3, want: 40 * time.Second},
		{attempt: 4, want: 80 * time.Second},
		{attempt: 5, want: 2 * time.Minute}, // clamped
		{attempt: 6, want: 2 * time.Minute}, // stays clamped
	}

	for _, tc := range cases {
		if got := Backoff(tc.attempt, min, max); got != tc.want {
			t.Fatalf("attempt=%d: got %s, want %s", tc.attempt, got, tc.want)
		}
	}
}
