package max

import (
	"testing"
	"time"
)

func TestComputeAfter_WithCursor(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	lastEnd := now.Add(-1 * time.Hour).UnixMilli()

	after, end := ComputeAfter(lastEnd, CursorParams{
		Overlap: 2 * time.Minute,
		NowTime: now,
	})
	wantAfter := lastEnd - 2*time.Minute.Milliseconds()
	wantEnd := now.UnixMilli()

	if after != wantAfter {
		t.Fatalf("after=%d want %d", after, wantAfter)
	}
	if end != wantEnd {
		t.Fatalf("end=%d want %d", end, wantEnd)
	}
}

func TestComputeAfter_WithoutCursor(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	after, end := ComputeAfter(0, CursorParams{
		DefaultLookback: 24 * time.Hour,
		NowTime:         now,
	})
	wantAfter := now.Add(-24 * time.Hour).UnixMilli()
	if after != wantAfter {
		t.Fatalf("after=%d want %d", after, wantAfter)
	}
	if end != now.UnixMilli() {
		t.Fatalf("end=%d", end)
	}
}

func TestShouldSkipMessage(t *testing.T) {
	if !ShouldSkipMessage(1000, 1000) {
		t.Fatal("expected skip at boundary")
	}
	if ShouldSkipMessage(1001, 1000) {
		t.Fatal("expected no skip")
	}
	if ShouldSkipMessage(500, 0) {
		t.Fatal("no cursor should not skip")
	}
}

func TestCursorStateKey(t *testing.T) {
	k := CursorStateKey("rosgvard_krd")
	if k == "" || k == "cursor_" {
		t.Fatalf("key=%q", k)
	}
}
