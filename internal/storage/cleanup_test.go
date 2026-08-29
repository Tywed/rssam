package storage

import (
	"testing"
	"time"
)

func TestRetentionCutoff(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	got := RetentionCutoff(now, 30)
	want := time.Date(2026, 4, 22, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("RetentionCutoff()=%v, want %v", got, want)
	}
	if got.Location() != time.UTC {
		t.Fatalf("location=%v, want UTC", got.Location())
	}
}

func TestRetentionCutoff_oneDay(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	got := RetentionCutoff(now, 1)
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("RetentionCutoff()=%v, want %v", got, want)
	}
}

func TestValidateEntryRetentionDays(t *testing.T) {
	if err := ValidateEntryRetentionDays(nil); err != nil {
		t.Fatalf("nil: %v", err)
	}
	zero := 0
	if err := ValidateEntryRetentionDays(&zero); err != nil {
		t.Fatalf("zero: %v", err)
	}
	ok := 30
	if err := ValidateEntryRetentionDays(&ok); err != nil {
		t.Fatalf("30: %v", err)
	}
	bad := 99999
	if err := ValidateEntryRetentionDays(&bad); err == nil {
		t.Fatal("expected error for out of range")
	}
}
