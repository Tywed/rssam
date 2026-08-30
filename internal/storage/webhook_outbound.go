package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"text/template"
)

type WebhookOutbound struct {
	Method string
	URL    string
	Body   []byte
	Kind   string
}

func BuildWebhookOutbound(w Webhook, feed WebhookFeed, entry Entry, filter *Filter) (WebhookOutbound, error) {
	kind, err := NormalizeWebhookKind(w.Kind)
	if err != nil {
		return WebhookOutbound{}, err
	}
	switch kind {
	case WebhookKindTelegram:
		return buildTelegramOutbound(w, feed, entry, filter)
	case WebhookKindMax:
		return buildMaxOutbound(w, feed, entry, filter)
	default:
		return buildHTTPOutbound(w, feed, entry, filter)
	}
}

func buildHTTPOutbound(w Webhook, feed WebhookFeed, entry Entry, filter *Filter) (WebhookOutbound, error) {
	payload, err := json.Marshal(map[string]any{
		"event_version": 1,
		"event_type":    httpEventType(filter),
		"entry":         entry,
		"feed":          feed,
		"filter":        filter,
		"sent_at":       entry.UpdatedAt,
	})
	if err != nil {
		return WebhookOutbound{}, err
	}
	body := payload
	if strings.TrimSpace(w.BodyTemplate) != "" {
		body, err = RenderWebhookBodyTemplate(w.BodyTemplate, feed, entry, filter, payload)
		if err != nil {
			return WebhookOutbound{}, err
		}
	}
	method := strings.ToUpper(strings.TrimSpace(w.Method))
	if method == "" {
		method = "POST"
	}
	return WebhookOutbound{Method: method, URL: w.URL, Body: body, Kind: WebhookKindHTTP}, nil
}

func httpEventType(filter *Filter) string {
	if filter != nil {
		return "entry_matched"
	}
	return "new_entry"
}

func buildTelegramOutbound(w Webhook, feed WebhookFeed, entry Entry, filter *Filter) (WebhookOutbound, error) {
	c, err := ParseTelegramProviderConfig(w.ProviderConfig)
	if err != nil {
		return WebhookOutbound{}, err
	}
	text, err := ChatMessageText(w.BodyTemplate, feed, entry, filter, true)
	if err != nil {
		return WebhookOutbound{}, err
	}
	body, err := json.Marshal(map[string]any{
		"chat_id":                  c.ChatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": false,
	})
	if err != nil {
		return WebhookOutbound{}, err
	}
	return WebhookOutbound{
		Method: "POST",
		URL:    TelegramSendURL(c.APIBase, c.BotToken),
		Body:   body,
		Kind:   WebhookKindTelegram,
	}, nil
}

func buildMaxOutbound(w Webhook, feed WebhookFeed, entry Entry, filter *Filter) (WebhookOutbound, error) {
	c, err := ParseMaxProviderConfig(w.ProviderConfig)
	if err != nil {
		return WebhookOutbound{}, err
	}
	text, err := ChatMessageText(w.BodyTemplate, feed, entry, filter, false)
	if err != nil {
		return WebhookOutbound{}, err
	}
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return WebhookOutbound{}, err
	}
	return WebhookOutbound{
		Method: "POST",
		URL:    MaxDisplayURL(c),
		Body:   body,
		Kind:   WebhookKindMax,
	}, nil
}

func ChatMessageText(bodyTemplate string, feed WebhookFeed, entry Entry, filter *Filter, asHTML bool) (string, error) {
	if strings.TrimSpace(bodyTemplate) != "" {
		return renderChatTemplate(bodyTemplate, feed, entry, filter)
	}
	title := strings.TrimSpace(entry.Title)
	feedTitle := strings.TrimSpace(feed.Title)
	link := strings.TrimSpace(entry.URL)
	if asHTML {
		title = html.EscapeString(title)
		feedTitle = html.EscapeString(feedTitle)
		if link != "" {
			esc := html.EscapeString(link)
			link = `<a href="` + esc + `">` + esc + `</a>`
		}
	}
	parts := make([]string, 0, 3)
	if feedTitle != "" {
		if asHTML {
			parts = append(parts, "<b>"+feedTitle+"</b>")
		} else {
			parts = append(parts, feedTitle)
		}
	}
	if title != "" {
		parts = append(parts, title)
	}
	if link != "" {
		parts = append(parts, link)
	}
	return strings.Join(parts, "\n\n"), nil
}

func renderChatTemplate(bodyTemplate string, feed WebhookFeed, entry Entry, filter *Filter) (string, error) {
	b, err := RenderWebhookBodyTemplate(bodyTemplate, feed, entry, filter, nil)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func RenderWebhookBodyTemplate(bodyTemplate string, feed WebhookFeed, entry Entry, filter *Filter, payloadJSON []byte) ([]byte, error) {
	tmpl, err := template.New("webhook_body").Option("missingkey=error").Parse(bodyTemplate)
	if err != nil {
		return nil, fmt.Errorf("invalid body_template: %w", err)
	}
	data := map[string]any{
		"entry":  entry,
		"feed":   feed,
		"filter": filter,
	}
	if payloadJSON != nil {
		data["payload"] = string(payloadJSON)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render body_template: %w", err)
	}
	return buf.Bytes(), nil
}

func ProviderDeliveryOK(kind string, statusCode int, snippet string) (bool, string) {
	kind, _ = NormalizeWebhookKind(kind)
	if statusCode < 200 || statusCode > 299 {
		return false, fmt.Sprintf("non-2xx status: %d", statusCode)
	}
	switch kind {
	case WebhookKindTelegram:
		var resp struct {
			OK          bool   `json:"ok"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal([]byte(snippet), &resp); err != nil {
			return false, "telegram: invalid JSON response"
		}
		if !resp.OK {
			msg := strings.TrimSpace(resp.Description)
			if msg == "" {
				msg = "telegram: ok=false"
			}
			return false, msg
		}
		return true, ""
	case WebhookKindMax:
		var resp struct {
			Success bool   `json:"success"`
			Detail  string `json:"detail"`
		}
		if err := json.Unmarshal([]byte(snippet), &resp); err != nil {
			return false, "max: invalid JSON response"
		}
		if !resp.Success {
			msg := strings.TrimSpace(resp.Detail)
			if msg == "" {
				msg = "max: success=false"
			}
			return false, msg
		}
		return true, ""
	default:
		return true, ""
	}
}
