package storage

import (
	"testing"
	"time"
)

func TestSessionNeedsTouch(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(DefaultSessionTTL)
	cases := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{"just created", fresh, false},
		{"touched 30 min ago", fresh.Add(-30 * time.Minute), false},
		{"touched 59 min ago", fresh.Add(-59 * time.Minute), false},
		{"touched 61 min ago", fresh.Add(-61 * time.Minute), true},
		{"touched a day ago", fresh.Add(-24 * time.Hour), true},
		{"almost expired", now.Add(time.Minute), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SessionNeedsTouch(tc.expiresAt, now); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
