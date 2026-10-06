package reader

import (
	"encoding/json"
	"maps"
)

// BridgeState is feeds.bridge_state: one document per bridge, keyed by the
// bridge name (= feed type). The core never looks inside a document; Adapt
// hands each bridge its own and stores the replacement, the others survive
// a rewrite untouched.
type BridgeState map[string]json.RawMessage

func ParseBridgeState(raw []byte) BridgeState {
	if len(raw) == 0 {
		return nil
	}
	var st BridgeState
	_ = json.Unmarshal(raw, &st)
	return st
}

// IsEmpty reports that no bridge has stored anything for the feed.
func (s BridgeState) IsEmpty() bool { return len(s) == 0 }

// Raw returns the document stored under key (a bridge name), nil if none.
func (s BridgeState) Raw(key string) json.RawMessage { return s[key] }

// With returns a copy of s where key holds raw (dropped when raw is empty).
func (s BridgeState) With(key string, raw json.RawMessage) BridgeState {
	out := make(BridgeState, len(s)+1)
	maps.Copy(out, s)
	if len(raw) == 0 {
		delete(out, key)
	} else {
		out[key] = raw
	}
	return out
}

func (s BridgeState) Marshal() []byte {
	if s.IsEmpty() {
		return []byte("{}")
	}
	b, err := json.Marshal(s)
	if err != nil {
		return []byte("{}")
	}
	return b
}
