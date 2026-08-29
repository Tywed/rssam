package storage

import "testing"

func TestFeedPollingBlocked(t *testing.T) {
	if FeedPollingBlocked(Feed{}) {
		t.Fatal("empty feed should not block polling")
	}
	if !FeedPollingBlocked(Feed{PollPaused: true}) {
		t.Fatal("circuit pause should block polling")
	}
	if !FeedPollingBlocked(Feed{ManualPaused: true}) {
		t.Fatal("manual pause should block polling")
	}
	if !FeedPollingBlocked(Feed{ManualPaused: true, PollPaused: true}) {
		t.Fatal("both pauses should block polling")
	}
}
