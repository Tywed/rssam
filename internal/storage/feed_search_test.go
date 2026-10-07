package storage

import "testing"

func TestClampFeedSuggestLimit(t *testing.T) {
	if got := clampFeedSuggestLimit(0); got != DefaultFeedSuggestLimit {
		t.Fatalf("zero: got %d", got)
	}
	if got := clampFeedSuggestLimit(3); got != 3 {
		t.Fatalf("in range: got %d", got)
	}
	if got := clampFeedSuggestLimit(MaxFeedSearchLimit + 10); got != MaxFeedSearchLimit {
		t.Fatalf("cap: got %d", got)
	}
}
