package ui

import (
	"net/http"
	"strconv"
	"strings"

	"rssam/internal/auth"
	"rssam/internal/bridgeconfig"
)

func (h *Handler) handleSettingsBridges(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	data := h.bridgeSettingsData(r, "overview")
	data.Title = "Мосты"
	h.render(w, r, "settings_bridges", data)
}

func (h *Handler) handleSettingsBridgeTelegram(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	data := h.bridgeSettingsData(r, "telegram")
	data.Title = "Telegram"
	h.render(w, r, "settings_bridge_telegram", data)
}

func (h *Handler) handleSettingsBridgeTelegramSave(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	stored := h.currentBridgeStored(r)
	tg := stored.Telegram
	tg.UseProxy = r.FormValue("use_proxy") == "1"
	tg.ProxyServiceURL = strings.TrimSpace(r.FormValue("proxy_service_url"))
	tg.ProxyTargetURL = strings.TrimSpace(r.FormValue("proxy_target_url"))
	tg.StaticProxy = strings.TrimSpace(r.FormValue("static_proxy"))
	tg.ConnectTimeout = strings.TrimSpace(r.FormValue("connect_timeout"))
	tg.RequestTimeout = strings.TrimSpace(r.FormValue("request_timeout"))
	tg.ProxyRetry = parseIntDefault(r.FormValue("proxy_retry"), tg.ProxyRetry)
	tg.MaxPages = parseIntDefault(r.FormValue("max_pages"), tg.MaxPages)
	if tok := strings.TrimSpace(r.FormValue("proxy_service_token")); tok != "" {
		tg.ProxyServiceToken = tok
	}
	stored.Telegram = tg
	if err := h.saveBridgeStored(r, stored); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/ui/settings/bridges/telegram?saved=1", http.StatusFound)
}

func (h *Handler) handleSettingsBridgeMax(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	data := h.bridgeSettingsData(r, "max")
	data.Title = "Max"
	h.render(w, r, "settings_bridge_max", data)
}

func (h *Handler) handleSettingsBridgeMaxSave(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	stored := h.currentBridgeStored(r)
	mx := stored.Max
	mx.APIBaseURL = strings.TrimSpace(r.FormValue("api_base_url"))
	mx.DefaultLimit = parseIntDefault(r.FormValue("default_limit"), mx.DefaultLimit)
	mx.DefaultLookback = strings.TrimSpace(r.FormValue("default_lookback"))
	mx.Overlap = strings.TrimSpace(r.FormValue("overlap"))
	mx.RateLimitSeconds = parseIntDefault(r.FormValue("rate_limit_seconds"), mx.RateLimitSeconds)
	mx.RequestIntervalMs = parseIntDefault(r.FormValue("request_interval_ms"), mx.RequestIntervalMs)
	if mx.RequestIntervalMs < 0 {
		mx.RequestIntervalMs = 0
	}
	if mx.RequestIntervalMs > 60000 {
		mx.RequestIntervalMs = 60000
	}
	mx.ConcurrentSlots = parseIntDefault(r.FormValue("concurrent_slots"), mx.ConcurrentSlots)
	if mx.ConcurrentSlots < 1 {
		mx.ConcurrentSlots = 1
	}
	if mx.ConcurrentSlots > 32 {
		mx.ConcurrentSlots = 32
	}
	mx.AllowPrivateAPI = r.FormValue("allow_private_api") == "1"
	stored.Max = mx
	if err := h.saveBridgeStored(r, stored); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/ui/settings/bridges/max?saved=1", http.StatusFound)
}

func (h *Handler) handleSettingsBridgeVK(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	data := h.bridgeSettingsData(r, "vk")
	data.Title = "VK Search"
	h.render(w, r, "settings_bridge_vk", data)
}

func (h *Handler) handleSettingsBridgeVKSave(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	stored := h.currentBridgeStored(r)
	vk := stored.VK
	vk.APIVersion = strings.TrimSpace(r.FormValue("api_version"))
	vk.DefaultCount = parseIntDefault(r.FormValue("default_count"), vk.DefaultCount)
	vk.DefaultLookback = strings.TrimSpace(r.FormValue("default_lookback"))
	vk.Overlap = strings.TrimSpace(r.FormValue("overlap"))
	vk.RateLimitSeconds = parseIntDefault(r.FormValue("rate_limit_seconds"), vk.RateLimitSeconds)
	if tok := strings.TrimSpace(r.FormValue("access_token")); tok != "" {
		vk.AccessToken = tok
	}
	stored.VK = vk
	if err := h.saveBridgeStored(r, stored); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/ui/settings/bridges/vk?saved=1", http.StatusFound)
}

func (h *Handler) handleSettingsBridgeRutube(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	data := h.bridgeSettingsData(r, "rutube")
	data.Title = "Rutube"
	h.render(w, r, "settings_bridge_rutube", data)
}

func (h *Handler) handleSettingsBridgeRutubeSave(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	stored := h.currentBridgeStored(r)
	stored.Rutube = bridgeconfig.RutubeStored{
		APIBaseURL: strings.TrimSpace(r.FormValue("api_base_url")),
	}
	if err := h.saveBridgeStored(r, stored); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/ui/settings/bridges/rutube?saved=1", http.StatusFound)
}

func (h *Handler) handleSettingsTelegramRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/ui/settings/bridges/telegram", http.StatusFound)
}

func (h *Handler) requireAdminPrincipal(w http.ResponseWriter, r *http.Request) bool {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok || !p.IsAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func (h *Handler) bridgeSettingsData(r *http.Request, section string) pageData {
	data := h.baseData(r, "settings")
	data.SettingsSection = "bridges"
	data.BridgeSection = section
	if h.cfg.GetBridgeSettings != nil {
		data.BridgeSettings = h.cfg.GetBridgeSettings()
	}
	if r.URL.Query().Get("saved") == "1" {
		data.FlashMsg = "Настройки сохранены и применены"
	}
	if p, ok := auth.PrincipalFromContext(r.Context()); ok && p.IsAdmin {
		if counts, err := h.bridgeFeedCounts(r); err == nil {
			data.BridgeFeedCounts = counts
		}
	}
	return data
}

func normalizeBridgeFeedType(feedType string) string {
	ft := strings.TrimSpace(feedType)
	switch ft {
	case "", "atom", "json":
		return "rss"
	case "vk":
		return "vk_search"
	default:
		return ft
	}
}

func (h *Handler) bridgeFeedCounts(r *http.Request) (map[string]int, error) {
	counts := map[string]int{
		"telegram":  0,
		"max":       0,
		"maxstat":   0,
		"vk_search": 0,
		"dzen_news": 0,
		"smotrim":   0,
		"rss":       0,
	}
	if h.cfg.Feeds == nil {
		return counts, nil
	}
	feeds, err := h.cfg.Feeds.ListAllFeeds(r.Context(), 10000)
	if err != nil {
		return nil, err
	}
	for _, f := range feeds {
		counts[normalizeBridgeFeedType(f.FeedType)]++
	}
	return counts, nil
}

func (h *Handler) currentBridgeStored(r *http.Request) bridgeconfig.Stored {
	if h.cfg.GetBridgeStored != nil {
		return h.cfg.GetBridgeStored()
	}
	return bridgeconfig.Stored{}
}

func (h *Handler) saveBridgeStored(r *http.Request, stored bridgeconfig.Stored) error {
	if h.cfg.SaveBridgeSettings == nil {
		return nil
	}
	return h.cfg.SaveBridgeSettings(r.Context(), stored)
}

func parseIntDefault(raw string, fallback int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func bridgeFeedCount(counts map[string]int, key string) int {
	if counts == nil {
		return 0
	}
	return counts[key]
}
