package reader

import (
	"testing"
	"time"

	"rssam/internal/bridge/v1"
	"rssam/internal/reader/dzen"
	maxbridge "rssam/internal/reader/max"
	"rssam/internal/reader/maxstat"
	"rssam/internal/reader/page"
	"rssam/internal/reader/rutube"
	"rssam/internal/reader/smotrim"
	"rssam/internal/reader/telegram"
	"rssam/internal/reader/vk"
)

// The bridges moved onto the contract in 0.2.1; feeds.bridge_state rows
// written by 0.2.0 (one typed document per bridge) must decode unchanged.
func TestLegacyBridgeState_DecodesWithContractBridges(t *testing.T) {
	st := ParseBridgeState([]byte(`{
		"telegram":{"use_proxy":false,"proxy_service_url":"http://p","static_proxy":"socks5://s","max_pages":2},
		"max":{"last_end_time_ms":1700000000000,"rate_limited_until":"2026-10-05T10:00:00Z"},
		"maxstat":{"q":"краснодар","api_token":"tok","last_end_time":1700000000},
		"vk_search":{"q":"golang","last_end_time":150},
		"rutube":{"channel_id":"26119699"},
		"dzen_news":{"q":"python"},
		"smotrim":{"brand_id":"67725","limit":5,"video_type":"plot"},
		"page":{"hash":"abc","text":"v1"}}`))

	var tg telegram.BridgeOverride
	if err := bridge.DecodeState(st.Raw(FeedTypeTelegram), &tg); err != nil || tg.UseProxy == nil || *tg.UseProxy || tg.ProxyServiceURL != "http://p" || tg.StaticProxy != "socks5://s" || tg.MaxPages != 2 {
		t.Fatalf("telegram: %+v %v", tg, err)
	}
	var mx maxbridge.FetchState
	if err := bridge.DecodeState(st.Raw(FeedTypeMax), &mx); err != nil || mx.LastEndTimeMs != 1700000000000 || mx.RateLimitedUntil == nil || !mx.RateLimitedUntil.Equal(time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("max: %+v %v", mx, err)
	}
	var ms maxstat.FetchState
	if err := bridge.DecodeState(st.Raw(FeedTypeMaxstat), &ms); err != nil || ms.Query != "краснодар" || ms.ApiToken != "tok" || ms.LastEndTime != 1700000000 {
		t.Fatalf("maxstat: %+v %v", ms, err)
	}
	var v vk.FetchState
	if err := bridge.DecodeState(st.Raw(FeedTypeVKSearch), &v); err != nil || v.Query != "golang" || v.LastEndTime != 150 {
		t.Fatalf("vk_search: %+v %v", v, err)
	}
	var rt rutube.FetchState
	if err := bridge.DecodeState(st.Raw(FeedTypeRutube), &rt); err != nil || rt.ChannelID != "26119699" {
		t.Fatalf("rutube: %+v %v", rt, err)
	}
	var dz dzen.FetchState
	if err := bridge.DecodeState(st.Raw(FeedTypeDzenNews), &dz); err != nil || dz.Query != "python" {
		t.Fatalf("dzen_news: %+v %v", dz, err)
	}
	var sm smotrim.FetchState
	if err := bridge.DecodeState(st.Raw(FeedTypeSmotrim), &sm); err != nil || sm.BrandID != "67725" || sm.Limit != 5 || sm.VideoType != "plot" {
		t.Fatalf("smotrim: %+v %v", sm, err)
	}
	var pg page.State
	if err := bridge.DecodeState(st.Raw(FeedTypePage), &pg); err != nil || pg.Hash != "abc" || pg.Text != "v1" {
		t.Fatalf("page: %+v %v", pg, err)
	}

	// And the bridges write the same keys back.
	if got := string(bridge.EncodeState(mx)); got != `{"last_end_time_ms":1700000000000,"rate_limited_until":"2026-10-05T10:00:00Z"}` {
		t.Fatalf("max encode = %s", got)
	}
	if got := string(bridge.EncodeState(ms)); got != `{"q":"краснодар","api_token":"tok","last_end_time":1700000000}` {
		t.Fatalf("maxstat encode = %s", got)
	}
	if got := string(bridge.EncodeState(pg)); got != `{"hash":"abc","text":"v1"}` {
		t.Fatalf("page encode = %s", got)
	}
}
