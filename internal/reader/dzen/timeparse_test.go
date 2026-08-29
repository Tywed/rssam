package dzen

import (
	"testing"
	"time"
)

func TestParsePublishedTime(t *testing.T) {
	now := time.Date(2026, 8, 12, 20, 0, 0, 0, MoscowLocation)
	cases := []struct {
		raw  string
		want time.Time
	}{
		{
			raw:  "Сегодня в 19:24",
			want: time.Date(2026, 8, 12, 19, 24, 0, 0, MoscowLocation),
		},
		{
			raw:  "Вчера в 08:05",
			want: time.Date(2026, 8, 11, 8, 5, 0, 0, MoscowLocation),
		},
		{
			raw:  "10 августа в 17:54",
			want: time.Date(2026, 8, 10, 17, 54, 0, 0, MoscowLocation),
		},
	}
	for _, tc := range cases {
		got := ParsePublishedTime(tc.raw, now)
		if !got.Equal(tc.want) {
			t.Fatalf("%q: got %v want %v", tc.raw, got, tc.want)
		}
	}
}

func TestParsePublishedTimeFromUnix(t *testing.T) {
	item := NewsItem{PubDateUnix: 1786373691}
	now := time.Now()
	var pub *time.Time
	if item.PubDateUnix > 0 {
		t := time.Unix(item.PubDateUnix, 0).UTC()
		pub = &t
	} else if t := ParsePublishedTime(item.TimeText, now); !t.IsZero() {
		utc := t.UTC()
		pub = &utc
	}
	if pub == nil {
		t.Fatal("expected pub date from unix")
	}
}
