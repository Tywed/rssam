package service

import (
	"context"
	"testing"

	"rssam/internal/storage"
)

type memRelayEntries struct {
	memEntryBulk
	dropped [][]int64
}

func (m *memRelayEntries) DropUndeliveredRelayEntries(_ context.Context, ids []int64) (int, error) {
	m.dropped = append(m.dropped, ids)
	return len(ids), nil
}

// After the subscribers' filters and webhooks ran, a relay poll hands every
// new entry to the orphan drop (the store keeps the ones with a delivery
// row); a full-mode poll never does.
func TestAfterStore_RelayDropsUndeliveredEntries(t *testing.T) {
	entries := &memRelayEntries{}
	r := &FeedRefresher{Entries: entries}
	subs := fixedSubs(r, 1, storage.Subscription{UserID: 1, FeedID: 1})
	stored := []storage.Entry{{ID: 10, FeedID: 1}, {ID: 11, FeedID: 1}}

	r.afterStore(context.Background(), storage.Feed{ID: 1, OwnerID: 1, StoreMode: storage.StoreModeRelay}, subs, stored)
	if len(entries.dropped) != 1 || len(entries.dropped[0]) != 2 || entries.dropped[0][1] != 11 {
		t.Fatalf("relay orphan drop: %v", entries.dropped)
	}

	entries.dropped = nil
	r.afterStore(context.Background(), storage.Feed{ID: 1, OwnerID: 1, StoreMode: storage.StoreModeFull}, subs, stored)
	if entries.dropped != nil {
		t.Fatalf("full mode must not drop entries: %v", entries.dropped)
	}
}
