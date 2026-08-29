package vk

import (
	"testing"
	"time"
)

func TestComputeTimeWindow_WithCursor(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	lastEnd := now.Add(-1 * time.Hour).Unix()

	win := ComputeTimeWindow(lastEnd, 24*time.Hour, 2*time.Minute, now)
	wantStart := lastEnd - 120
	if win.StartTime != wantStart {
		t.Fatalf("start=%d want %d", win.StartTime, wantStart)
	}
	if win.EndTime != now.Unix() {
		t.Fatalf("end=%d want %d", win.EndTime, now.Unix())
	}
}

func TestComputeTimeWindow_WithoutCursor(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	win := ComputeTimeWindow(0, 24*time.Hour, 2*time.Minute, now)
	wantStart := now.Add(-24 * time.Hour).Unix()
	if win.StartTime != wantStart {
		t.Fatalf("start=%d want %d", win.StartTime, wantStart)
	}
}

func TestShouldSkipPost(t *testing.T) {
	if !ShouldSkipPost(1000, 1000) {
		t.Fatal("expected skip at boundary")
	}
	if ShouldSkipPost(1001, 1000) {
		t.Fatal("expected no skip")
	}
}
