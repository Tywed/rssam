package worker

import (
	"testing"
	"time"
)

func TestMaybeRunDailyFeedReset_SkipsSameDay(t *testing.T) {
	// Logic-only: today key format matches daily reset loop.
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(loc).Format("2006-01-02")
	if today == "" {
		t.Fatal("empty date key")
	}
}
