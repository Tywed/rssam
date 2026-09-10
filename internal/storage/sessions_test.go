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
			if got := SessionNeedsTouch(tc.expiresAt, now, DefaultSessionTTL); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// With a short SESSION_MAX_AGE the hour of slack would swallow the whole
// lifetime; the slack shrinks to half the ttl so sliding expiry still works.
func TestSessionNeedsTouch_ShortTTL(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ttl := 10 * time.Minute
	if SessionNeedsTouch(now.Add(ttl), now, ttl) {
		t.Fatal("fresh session must not need a touch")
	}
	if !SessionNeedsTouch(now.Add(4*time.Minute), now, ttl) {
		t.Fatal("session past half its lifetime must be refreshed")
	}
}
