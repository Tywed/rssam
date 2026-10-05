package reader

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

// BridgeState is persisted per feed (JSONB): one key per bridge. The typed
// fields are the bridges the core itself reads (Telegram form, Max reset);
// Extra carries the documents of contract bridges (Adapt) verbatim, so a
// bridge added later needs no field here.
type BridgeState struct {
	Max      *MaxBridgeState            `json:"max,omitempty"`
	Maxstat  *MaxstatBridgeState        `json:"maxstat,omitempty"`
	Telegram *TelegramBridgeState       `json:"telegram,omitempty"`
	VKSearch *VKSearchBridgeState       `json:"vk_search,omitempty"`
	Rutube   *RutubeBridgeState         `json:"rutube,omitempty"`
	DzenNews *DzenNewsBridgeState       `json:"dzen_news,omitempty"`
	Smotrim  *SmotrimBridgeState        `json:"smotrim,omitempty"`
	Page     *PageBridgeState           `json:"page,omitempty"`
	Extra    map[string]json.RawMessage `json:"-"`
}

type bridgeStateTyped BridgeState

func (s BridgeState) MarshalJSON() ([]byte, error) {
	typed := bridgeStateTyped(s)
	typed.Extra = nil
	b, err := json.Marshal(typed)
	if err != nil {
		return nil, err
	}
	if len(s.Extra) == 0 {
		return b, nil
	}
	doc := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	for k, v := range s.Extra {
		if len(v) > 0 {
			doc[k] = v
		}
	}
	return json.Marshal(doc)
}

func (s *BridgeState) UnmarshalJSON(b []byte) error {
	var typed bridgeStateTyped
	if err := json.Unmarshal(b, &typed); err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		return err
	}
	for _, k := range typedStateKeys {
		delete(doc, k)
	}
	if len(doc) == 0 {
		doc = nil
	}
	typed.Extra = doc
	*s = BridgeState(typed)
	return nil
}

// typedStateKeys are the JSON keys of BridgeState's typed fields.
var typedStateKeys = func() []string {
	rt := reflect.TypeFor[bridgeStateTyped]()
	keys := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		if tag, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ","); tag != "" && tag != "-" {
			keys = append(keys, tag)
		}
	}
	return keys
}()

// IsEmpty reports that no bridge has stored anything for the feed.
func (s BridgeState) IsEmpty() bool {
	return s.Max == nil && s.Maxstat == nil && s.Telegram == nil && s.VKSearch == nil && s.Rutube == nil && s.DzenNews == nil && s.Smotrim == nil && s.Page == nil && len(s.Extra) == 0
}

// Raw returns the document stored under key (a bridge name), nil if none.
func (s BridgeState) Raw(key string) json.RawMessage {
	if v, ok := s.Extra[key]; ok {
		return v
	}
	b, err := s.MarshalJSON()
	if err != nil {
		return nil
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	return doc[key]
}

// With returns a copy of s where key holds raw; the other bridges' documents
// are kept.
func (s BridgeState) With(key string, raw json.RawMessage) BridgeState {
	out := s
	out.Extra = make(map[string]json.RawMessage, len(s.Extra)+1)
	for k, v := range s.Extra {
		out.Extra[k] = v
	}
	if len(raw) == 0 {
		delete(out.Extra, key)
	} else {
		out.Extra[key] = raw
	}
	return out
}

// PageBridgeState is the last snapshot of a watched page fragment: the hash
// decides whether anything changed, the text feeds the diff of the next entry.
type PageBridgeState struct {
	Hash string `json:"hash,omitempty"`
	Text string `json:"text,omitempty"`
}

// RutubeBridgeState holds per-feed Rutube person channel id.
type RutubeBridgeState struct {
	ChannelID string `json:"channel_id,omitempty"`
}

// DzenNewsBridgeState holds per-feed Dzen News search query override.
type DzenNewsBridgeState struct {
	Q string `json:"q,omitempty"`
}

// SmotrimBridgeState holds per-feed Smotrim brand options.
type SmotrimBridgeState struct {
	BrandID   string `json:"brand_id,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	VideoType string `json:"video_type,omitempty"`
}

// VKSearchBridgeState holds cursor state for VK newsfeed.search feeds.
type VKSearchBridgeState struct {
	Q                string     `json:"q,omitempty"`
	LastEndTime      int64      `json:"last_end_time,omitempty"`
	RateLimitedUntil *time.Time `json:"rate_limited_until,omitempty"`
}

// TelegramBridgeState holds optional per-feed Telegram bridge overrides.
type TelegramBridgeState struct {
	UseProxy        *bool  `json:"use_proxy,omitempty"`
	ProxyServiceURL string `json:"proxy_service_url,omitempty"`
	ProxyTargetURL  string `json:"proxy_target_url,omitempty"`
	StaticProxy     string `json:"static_proxy,omitempty"`
	MaxPages        int    `json:"max_pages,omitempty"`
}

type MaxBridgeState struct {
	LastEndTimeMs    int64      `json:"last_end_time_ms,omitempty"`
	RateLimitedUntil *time.Time `json:"rate_limited_until,omitempty"`
}

// MaxstatBridgeState holds per-feed maxstat.ru search settings (like RSS-Bridge api_token).
type MaxstatBridgeState struct {
	Q                string     `json:"q,omitempty"`
	ApiToken         string     `json:"api_token,omitempty"`
	LastEndTime      int64      `json:"last_end_time,omitempty"`
	RateLimitedUntil *time.Time `json:"rate_limited_until,omitempty"`
}

func ParseBridgeState(raw []byte) BridgeState {
	if len(raw) == 0 {
		return BridgeState{}
	}
	var st BridgeState
	_ = json.Unmarshal(raw, &st)
	return st
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
