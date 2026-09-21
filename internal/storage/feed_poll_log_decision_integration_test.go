//go:build integration

package storage

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The one-statement RecordFeedPoll keeps the event semantics: a new row on
// every state change, repeat_count on an identical failure, nothing at all
// on a success after a success — not even WAL.
func TestIntegration_FeedPollLogDecision(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "polldecide")
	feed, _ := newIntegrationFeedWithEntries(t, store, owner.ID, 0)

	now := time.Now().UTC()
	record := func(i int, ok bool, errText string) {
		t.Helper()
		if err := store.RecordFeedPoll(ctx, RecordFeedPollParams{FeedID: feed.ID, At: now.Add(time.Duration(i) * time.Minute), OK: ok, Error: errText, Inserted: i}); err != nil {
			t.Fatal(err)
		}
	}
	list := func() []FeedPollLogEntry {
		t.Helper()
		got, err := store.ListFeedPollLog(ctx, feed.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	wal := func() string {
		var lsn string
		if err := store.db.QueryRow(ctx, "SELECT pg_current_wal_insert_lsn()::text").Scan(&lsn); err != nil {
			t.Fatal(err)
		}
		return lsn
	}

	record(0, true, "")
	if got := list(); len(got) != 1 || !got[0].OK || got[0].RepeatCount != 1 {
		t.Fatalf("first success: %+v", got)
	}
	before := wal()
	record(1, true, "")
	if after := wal(); after != before {
		t.Fatalf("success after success wrote WAL: %s → %s", before, after)
	}
	if got := list(); len(got) != 1 || got[0].Inserted != 0 {
		t.Fatalf("success after success must be skipped: %+v", got)
	}

	record(2, false, "boom")
	record(3, false, "boom")
	got := list()
	if len(got) != 2 || got[0].OK || got[0].RepeatCount != 2 || got[0].Inserted != 3 || got[0].Error != "boom" {
		t.Fatalf("same failure must coalesce: %+v", got)
	}
	record(4, false, "other")
	if got = list(); len(got) != 3 || got[0].Error != "other" || got[0].RepeatCount != 1 {
		t.Fatalf("different failure is a new event: %+v", got)
	}
	record(5, true, "")
	if got = list(); len(got) != 4 || !got[0].OK {
		t.Fatalf("failure then success is a new event: %+v", got)
	}
	record(6, false, "boom")
	if got = list(); len(got) != 5 || got[0].Error != "boom" || got[0].RepeatCount != 1 {
		t.Fatalf("success then failure is a new event: %+v", got)
	}

	// Error text is capped before it is compared, so an over-long repeat
	// still coalesces.
	long := strings.Repeat("x", MaxFeedPollLogError+100)
	record(7, false, long)
	record(8, false, long+"tail")
	if got = list(); len(got) != 6 || got[0].RepeatCount != 2 || len(got[0].Error) != MaxFeedPollLogError {
		t.Fatalf("capped error must coalesce: len=%d rc=%d errlen=%d", len(got), got[0].RepeatCount, len(got[0].Error))
	}

	// Each state change counts against the per-feed cap in the same
	// statement: never more than MaxFeedPollLogPerFeed rows survive.
	for i := 10; i < 10+MaxFeedPollLogPerFeed+5; i++ {
		record(i, i%2 == 0, "e")
	}
	if got = list(); len(got) != MaxFeedPollLogPerFeed || got[0].Inserted != 10+MaxFeedPollLogPerFeed+4 {
		t.Fatalf("cap: len=%d newest inserted=%d", len(got), got[0].Inserted)
	}
}
