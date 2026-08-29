package storage

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const (
	WebhookKindHTTP     = "http"
	WebhookKindTelegram = "telegram"
	WebhookKindMax      = "max"

	TelegramAPIDefault = "https://api.telegram.org"

	MaxTargetChatID  = "chat_id"
	MaxTargetChannel = "channel"
	MaxTargetUser    = "user"
)

type TelegramProviderConfig struct {
	APIBase  string `json:"api_base"`
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type MaxProviderConfig struct {
	APIBase  string `json:"api_base"`
	Target   string `json:"target"`
	TargetID string `json:"target_id"`
}

func NormalizeWebhookKind(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return WebhookKindHTTP, nil
	}
	switch s {
	case WebhookKindHTTP, WebhookKindTelegram, WebhookKindMax:
		return s, nil
	default:
		return "", fmt.Errorf("kind must be http, telegram, or max")
	}
}

func TrimAPIBase(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func TelegramAPIBase(raw string) string {
	if b := TrimAPIBase(raw); b != "" {
		return b
	}
	return TelegramAPIDefault
}

func ParseTelegramProviderConfig(raw []byte) (TelegramProviderConfig, error) {
	var c TelegramProviderConfig
	if len(bytesTrim(raw)) == 0 {
		return c, fmt.Errorf("telegram config is required")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return TelegramProviderConfig{}, fmt.Errorf("telegram config: %w", err)
	}
	c.APIBase = TrimAPIBase(c.APIBase)
	c.BotToken = strings.TrimSpace(c.BotToken)
	c.ChatID = strings.TrimSpace(c.ChatID)
	if c.BotToken == "" {
		return c, fmt.Errorf("telegram bot_token is required")
	}
	if c.ChatID == "" {
		return c, fmt.Errorf("telegram chat_id is required")
	}
	return c, nil
}

func ParseMaxProviderConfig(raw []byte) (MaxProviderConfig, error) {
	var c MaxProviderConfig
	if len(bytesTrim(raw)) == 0 {
		return c, fmt.Errorf("max config is required")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return MaxProviderConfig{}, fmt.Errorf("max config: %w", err)
	}
	c.APIBase = TrimAPIBase(c.APIBase)
	c.Target = strings.ToLower(strings.TrimSpace(c.Target))
	c.TargetID = strings.TrimSpace(c.TargetID)
	if c.APIBase == "" {
		return c, fmt.Errorf("max api_base is required")
	}
	switch c.Target {
	case MaxTargetChatID, MaxTargetChannel, MaxTargetUser:
	case "":
		c.Target = MaxTargetChatID
	default:
		return c, fmt.Errorf("max target must be chat_id, channel, or user")
	}
	if c.TargetID == "" {
		return c, fmt.Errorf("max target_id is required")
	}
	return c, nil
}

func TelegramDisplayURL(apiBase string) string {
	return TelegramAPIBase(apiBase) + "/bot/sendMessage"
}

func TelegramSendURL(apiBase, botToken string) string {
	return TelegramAPIBase(apiBase) + "/bot" + strings.TrimSpace(botToken) + "/sendMessage"
}

func MaxDisplayURL(c MaxProviderConfig) string {
	return maxMessagesPath(c.APIBase, c.Target, c.TargetID)
}

func maxMessagesPath(apiBase, target, targetID string) string {
	base := TrimAPIBase(apiBase)
	id := strings.TrimSpace(targetID)
	switch target {
	case MaxTargetChannel:
		return base + "/channel/" + url.PathEscape(strings.TrimPrefix(id, "@")) + "/messages"
	case MaxTargetUser:
		return base + "/channel/user/" + url.PathEscape(id) + "/messages"
	default:
		return base + "/channel/id/" + url.PathEscape(id) + "/messages"
	}
}

func MaskTelegramConfig(raw []byte) []byte {
	c, err := ParseTelegramProviderConfig(raw)
	if err != nil {
		return []byte(`{}`)
	}
	if c.BotToken != "" {
		c.BotToken = "••••"
	}
	b, err := json.Marshal(c)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}

func MergeTelegramToken(incoming, existing []byte) []byte {
	in, err := parseTelegramLoose(incoming)
	if err != nil {
		return incoming
	}
	if strings.TrimSpace(in.BotToken) != "" {
		return incoming
	}
	ex, err := parseTelegramLoose(existing)
	if err != nil {
		return incoming
	}
	in.BotToken = ex.BotToken
	b, err := json.Marshal(in)
	if err != nil {
		return incoming
	}
	return b
}

func parseTelegramLoose(raw []byte) (TelegramProviderConfig, error) {
	var c TelegramProviderConfig
	if len(bytesTrim(raw)) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	return c, nil
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

func ResolveWebhookWrite(kind, rawURL string, providerConfig []byte) (kindOut, displayURL string, cfg []byte, err error) {
	kindOut, err = NormalizeWebhookKind(kind)
	if err != nil {
		return "", "", nil, err
	}
	switch kindOut {
	case WebhookKindTelegram:
		cfg = providerConfig
		if len(bytesTrim(cfg)) == 0 {
			cfg = []byte(`{}`)
		}
		c, err := ParseTelegramProviderConfig(cfg)
		if err != nil {
			return "", "", nil, err
		}
		b, err := json.Marshal(c)
		if err != nil {
			return "", "", nil, err
		}
		return kindOut, TelegramDisplayURL(c.APIBase), b, nil
	case WebhookKindMax:
		cfg = providerConfig
		if len(bytesTrim(cfg)) == 0 {
			cfg = []byte(`{}`)
		}
		c, err := ParseMaxProviderConfig(cfg)
		if err != nil {
			return "", "", nil, err
		}
		b, err := json.Marshal(c)
		if err != nil {
			return "", "", nil, err
		}
		return kindOut, MaxDisplayURL(c), b, nil
	default:
		u := strings.TrimSpace(rawURL)
		if u == "" {
			return "", "", nil, fmt.Errorf("url is required")
		}
		pu, err := url.Parse(u)
		if err != nil || pu.Scheme == "" || pu.Host == "" {
			return "", "", nil, fmt.Errorf("url must be a valid URL")
		}
		if len(bytesTrim(providerConfig)) == 0 {
			providerConfig = []byte(`{}`)
		}
		if !json.Valid(providerConfig) {
			return "", "", nil, fmt.Errorf("provider_config must be valid JSON")
		}
		return kindOut, u, providerConfig, nil
	}
}
