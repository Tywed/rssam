package storage

import (
	"encoding/json"
	"strings"
)

// BuildWebhookTextOutbound sends a free-form message through a webhook:
// Telegram gets htmlText, Max plainText, HTTP the given JSON payload (the
// body_template is not applied — it describes entries).
func BuildWebhookTextOutbound(w Webhook, plainText, htmlText string, payload []byte) (WebhookOutbound, error) {
	kind, err := NormalizeWebhookKind(w.Kind)
	if err != nil {
		return WebhookOutbound{}, err
	}
	switch kind {
	case WebhookKindTelegram:
		c, err := ParseTelegramProviderConfig(w.ProviderConfig)
		if err != nil {
			return WebhookOutbound{}, err
		}
		body, err := json.Marshal(map[string]any{
			"chat_id":                  c.ChatID,
			"text":                     htmlText,
			"parse_mode":               "HTML",
			"disable_web_page_preview": true,
		})
		if err != nil {
			return WebhookOutbound{}, err
		}
		return WebhookOutbound{Method: "POST", URL: TelegramSendURL(c.APIBase, c.BotToken), Body: body, Kind: kind}, nil
	case WebhookKindMax:
		c, err := ParseMaxProviderConfig(w.ProviderConfig)
		if err != nil {
			return WebhookOutbound{}, err
		}
		body, err := json.Marshal(map[string]string{"text": plainText})
		if err != nil {
			return WebhookOutbound{}, err
		}
		return WebhookOutbound{Method: "POST", URL: MaxDisplayURL(c), Body: body, Kind: kind}, nil
	}
	method := strings.ToUpper(strings.TrimSpace(w.Method))
	if method == "" {
		method = "POST"
	}
	return WebhookOutbound{Method: method, URL: w.URL, Body: payload, Kind: WebhookKindHTTP}, nil
}
