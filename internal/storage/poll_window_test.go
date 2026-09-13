package storage

import (
	"testing"
	"time"
)

func TestParsePollHours(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  bool
		none bool
	}{
		{in: "", none: true},
		{in: "   ", none: true},
		{in: "08:00-22:00", want: "08:00-22:00"},
		{in: " 8:5 - 22:30 ", want: "08:05-22:30"},
		{in: "22:00-06:00", want: "22:00-06:00"},
		{in: "10:00-10:00", err: true},
		{in: "24:00-06:00", err: true},
		{in: "08:60-09:00", err: true},
		{in: "08:00", err: true},
		{in: "08:00-09:00-10:00", err: true},
		{in: "morning", err: true},
	}
	for _, c := range cases {
		got, err := NormalizePollHours(c.in)
		if c.err {
			if err == nil {
				t.Errorf("%q: want error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if c.none && got != "" {
			t.Errorf("%q: want empty, got %q", c.in, got)
		}
		if !c.none && got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}

func TestPollWindow_ContainsAndNextOpen(t *testing.T) {
	loc := time.Local
	at := func(h, m int) time.Time { return time.Date(2026, 9, 13, h, m, 0, 0, loc) }

	day, _, _ := ParsePollHours("08:00-22:00")
	night, _, _ := ParsePollHours("22:00-06:00")

	if !day.Contains(at(8, 0)) || !day.Contains(at(21, 59)) || day.Contains(at(22, 0)) || day.Contains(at(3, 0)) {
		t.Fatal("day window membership")
	}
	if !night.Contains(at(22, 0)) || !night.Contains(at(3, 0)) || night.Contains(at(6, 0)) || night.Contains(at(12, 0)) {
		t.Fatal("night window membership")
	}

	if got := day.NextOpen(at(12, 30)); !got.Equal(at(12, 30)) {
		t.Fatalf("open window must return t itself, got %s", got)
	}
	if got := day.NextOpen(at(23, 0)); !got.Equal(at(8, 0).AddDate(0, 0, 1)) {
		t.Fatalf("after close → next morning, got %s", got)
	}
	if got := day.NextOpen(at(3, 0)); !got.Equal(at(8, 0)) {
		t.Fatalf("before open → same morning, got %s", got)
	}
	if got := night.NextOpen(at(12, 0)); !got.Equal(at(22, 0)) {
		t.Fatalf("night window opens tonight, got %s", got)
	}
	// UTC input is evaluated in local time.
	if got := day.NextOpen(at(23, 0).UTC()); !got.Equal(at(8, 0).AddDate(0, 0, 1)) {
		t.Fatalf("utc input, got %s", got)
	}
}
