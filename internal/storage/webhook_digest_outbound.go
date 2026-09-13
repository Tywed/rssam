package storage

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"
)

// webhookChatTextMax keeps a digest under the Telegram message limit (4096).
const webhookChatTextMax = 3900

// BuildWebhookDigestOutbound renders one message for a batch of entries.
// HTTP: JSON {event_type:"digest", items:[{entry,feed}], count, sent_at}; a
// body_template is executed once with .items/.count/.payload. Telegram/Max:
// entries grouped by feed; a body_template is rendered per item and the
// results joined.
func BuildWebhookDigestOutbound(w Webhook, items []WebhookDigestItem, sentAt time.Time) (WebhookOutbound, error) {
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
		text, err := digestChatText(w.BodyTemplate, items, true)
		if err != nil {
			return WebhookOutbound{}, err
		}
		body, err := json.Marshal(map[string]any{
			"chat_id":                  c.ChatID,
			"text":                     text,
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
		text, err := digestChatText(w.BodyTemplate, items, false)
		if err != nil {
			return WebhookOutbound{}, err
		}
		body, err := json.Marshal(map[string]string{"text": text})
		if err != nil {
			return WebhookOutbound{}, err
		}
		return WebhookOutbound{Method: "POST", URL: MaxDisplayURL(c), Body: body, Kind: kind}, nil
	}

	// Items use the same keys as a per-entry template (.entry/.feed), so
	// one template style works for both modes.
	list := make([]map[string]any, 0, len(items))
	for _, it := range items {
		list = append(list, map[string]any{"entry": it.Entry, "feed": it.Feed})
	}
	payload, err := json.Marshal(map[string]any{
		"event_version": 1,
		"event_type":    "digest",
		"count":         len(list),
		"items":         list,
		"sent_at":       sentAt,
	})
	if err != nil {
		return WebhookOutbound{}, err
	}
	body := payload
	if strings.TrimSpace(w.BodyTemplate) != "" {
		tmpl, err := parseBodyTemplate(w.BodyTemplate)
		if err != nil {
			return WebhookOutbound{}, err
		}
		var buf strings.Builder
		if err := tmpl.Execute(&buf, map[string]any{"items": list, "count": len(list), "payload": string(payload)}); err != nil {
			return WebhookOutbound{}, fmt.Errorf("render body_template: %w", err)
		}
		body = []byte(buf.String())
	}
	method := strings.ToUpper(strings.TrimSpace(w.Method))
	if method == "" {
		method = "POST"
	}
	return WebhookOutbound{Method: method, URL: w.URL, Body: body, Kind: WebhookKindHTTP}, nil
}

func digestChatText(bodyTemplate string, items []WebhookDigestItem, asHTML bool) (string, error) {
	head := fmt.Sprintf("Дайджест: %d", len(items))
	if asHTML {
		head = "<b>" + head + "</b>"
	}
	var b strings.Builder
	b.WriteString(head)
	if strings.TrimSpace(bodyTemplate) != "" {
		for i, it := range items {
			s, err := renderChatTemplate(bodyTemplate, it.Feed, it.Entry, nil)
			if err != nil {
				return "", err
			}
			if !chatAppend(&b, "\n\n"+strings.TrimSpace(s), len(items)-i) {
				break
			}
		}
		return b.String(), nil
	}
	lastFeed := int64(-1)
	for i, it := range items {
		var line strings.Builder
		if it.Feed.ID != lastFeed {
			lastFeed = it.Feed.ID
			title := strings.TrimSpace(it.Feed.Title)
			if asHTML {
				title = "<b>" + html.EscapeString(title) + "</b>"
			}
			line.WriteString("\n\n" + title)
		}
		t := strings.TrimSpace(it.Entry.Title)
		if t == "" {
			t = strings.TrimSpace(it.Entry.URL)
		}
		if asHTML {
			if u := strings.TrimSpace(it.Entry.URL); u != "" {
				line.WriteString("\n• <a href=\"" + html.EscapeString(u) + "\">" + html.EscapeString(t) + "</a>")
			} else {
				line.WriteString("\n• " + html.EscapeString(t))
			}
		} else {
			line.WriteString("\n• " + t)
			if u := strings.TrimSpace(it.Entry.URL); u != "" {
				line.WriteString("\n  " + u)
			}
		}
		if !chatAppend(&b, line.String(), len(items)-i) {
			break
		}
	}
	return b.String(), nil
}

// chatAppend adds s unless the text would exceed the chat limit, in which
// case a "… ещё N" trailer is written instead and false is returned.
func chatAppend(b *strings.Builder, s string, remaining int) bool {
	if b.Len()+len(s) > webhookChatTextMax {
		fmt.Fprintf(b, "\n\n… ещё %d", remaining)
		return false
	}
	b.WriteString(s)
	return true
}
