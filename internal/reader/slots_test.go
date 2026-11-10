package reader

import (
	"testing"
	"time"

	"rssam/internal/reader/telegram"
)

// The registry exposes slot-bounded bridges (through the contract adapter
// too) and answers "unlimited" for the others.
func TestHandlerRegistry_SlotBridges(t *testing.T) {
	tg := telegram.NewHandler(nil, nil, telegram.Config{ConcurrentSlots: 3})
	reg := NewHandlerRegistry(Adapt(&fakeBridge{}), Adapt(tg))
	if got := reg.SlotBridges(); len(got) != 1 || got[0] != "telegram" {
		t.Fatalf("SlotBridges=%v, want [telegram]", got)
	}
	free, _, limited := reg.FreeSlots("telegram", time.Now(), 0)
	if !limited || free != 3 {
		t.Fatalf("telegram: free=%d limited=%v", free, limited)
	}
	if _, _, limited := reg.FreeSlots("fake", time.Now(), 0); limited {
		t.Fatal("fake must not be slot-limited")
	}
	if _, _, limited := reg.FreeSlots("nosuch", time.Now(), 0); limited {
		t.Fatal("unknown bridge must not be slot-limited")
	}
}
