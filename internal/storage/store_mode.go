package storage

import (
	"fmt"
	"strings"
)

// Storage modes of a feed. full keeps every item; filtered keeps an item
// only when it matches a subscriber's filter and the hash of the rest;
// relay keeps a matched item only until its webhooks are delivered, with no
// search vector, no per-user rows and no filter_matches.
const (
	StoreModeFull     = "full"
	StoreModeFiltered = "filtered"
	StoreModeRelay    = "relay"
)

func NormalizeStoreMode(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "":
		return StoreModeFull, nil
	case StoreModeFull, StoreModeFiltered, StoreModeRelay:
		return s, nil
	default:
		return "", fmt.Errorf("store_mode must be full, filtered, or relay")
	}
}
