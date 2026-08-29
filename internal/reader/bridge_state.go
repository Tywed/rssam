package reader

import (
	"encoding/json"
	"time"
)

// BridgeState is persisted per feed (JSONB).
type BridgeState struct {
	Max      *MaxBridgeState      `json:"max,omitempty"`
	Maxstat  *MaxstatBridgeState  `json:"maxstat,omitempty"`
	Telegram *TelegramBridgeState `json:"telegram,omitempty"`
	VKSearch *VKSearchBridgeState `json:"vk_search,omitempty"`
	Rutube   *RutubeBridgeState   `json:"rutube,omitempty"`
	DzenNews *DzenNewsBridgeState `json:"dzen_news,omitempty"`
	Smotrim  *SmotrimBridgeState  `json:"smotrim,omitempty"`
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
	if s.Max == nil && s.Maxstat == nil && s.Telegram == nil && s.VKSearch == nil && s.Rutube == nil && s.DzenNews == nil && s.Smotrim == nil {
		return []byte("{}")
	}
	b, err := json.Marshal(s)
	if err != nil {
		return []byte("{}")
	}
	return b
}
