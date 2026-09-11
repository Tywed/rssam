package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildWebhookOutbound_Kinds(t *testing.T) {
	feed := WebhookFeed{ID: 1, Title: "Feed <1>"}
	entry := Entry{ID: 2, Title: "Hello & bye", URL: "https://example.com/p?a=1&b=2", UpdatedAt: time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)}
	flt := &Filter{ID: 3, Name: "f"}

	out, err := BuildWebhookOutbound(Webhook{URL: "https://h", Method: " put "}, feed, entry, nil)
	if err != nil || out.Method != "PUT" {
		t.Fatalf("http: %+v err=%v", out, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Body, &payload); err != nil || payload["event_type"] != "new_entry" || payload["event_version"] != float64(1) {
		t.Fatalf("http payload: %v err=%v", payload, err)
	}
	out, _ = BuildWebhookOutbound(Webhook{URL: "https://h"}, feed, entry, flt)
	_ = json.Unmarshal(out.Body, &payload)
	if payload["event_type"] != "entry_matched" {
		t.Fatalf("matched event type: %v", payload["event_type"])
	}
	out, err = BuildWebhookOutbound(Webhook{URL: "https://h", BodyTemplate: `{"t":"{{.entry.Title}}","raw":{{.payload}}}`}, feed, entry, nil)
	if err != nil || !json.Valid(out.Body) || !strings.Contains(string(out.Body), `"t":"Hello & bye"`) {
		t.Fatalf("templated http: %s err=%v", out.Body, err)
	}
	if _, err := BuildWebhookOutbound(Webhook{URL: "https://h", BodyTemplate: "{{.entry.Missing}}"}, feed, entry, nil); err == nil {
		t.Fatal("missing template field must fail")
	}
	if _, err := BuildWebhookOutbound(Webhook{URL: "https://h", BodyTemplate: "{{"}, feed, entry, nil); err == nil {
		t.Fatal("broken template must fail")
	}
	if _, err := BuildWebhookOutbound(Webhook{Kind: "fax"}, feed, entry, nil); err == nil {
		t.Fatal("unknown kind must fail")
	}

	tg, err := BuildWebhookOutbound(Webhook{Kind: WebhookKindTelegram, ProviderConfig: []byte(`{"bot_token":"1:x","chat_id":"7","api_base":"https://tg.local/"}`)}, feed, entry, nil)
	if err != nil || tg.URL != "https://tg.local/bot1:x/sendMessage" || tg.Kind != WebhookKindTelegram {
		t.Fatalf("telegram: %+v err=%v", tg, err)
	}
	var msg map[string]any
	_ = json.Unmarshal(tg.Body, &msg)
	text, _ := msg["text"].(string)
	if msg["chat_id"] != "7" || msg["parse_mode"] != "HTML" || !strings.Contains(text, "<b>Feed &lt;1&gt;</b>") || !strings.Contains(text, "Hello &amp; bye") || !strings.Contains(text, `<a href="https://example.com/p?a=1&amp;b=2">`) {
		t.Fatalf("telegram body: %s", tg.Body)
	}
	if _, err := BuildWebhookOutbound(Webhook{Kind: WebhookKindTelegram, ProviderConfig: []byte(`{"chat_id":"7"}`)}, feed, entry, nil); err == nil {
		t.Fatal("telegram without token must fail")
	}
	if _, err := BuildWebhookOutbound(Webhook{Kind: WebhookKindTelegram, BodyTemplate: "{{.nope}}", ProviderConfig: []byte(`{"bot_token":"1:x","chat_id":"7"}`)}, feed, entry, nil); err == nil {
		t.Fatal("telegram broken template must fail")
	}

	mx, err := BuildWebhookOutbound(Webhook{Kind: WebhookKindMax, ProviderConfig: []byte(`{"api_base":"https://max.local/api","target":"channel","target_id":"@news"}`)}, feed, entry, nil)
	if err != nil || mx.URL != "https://max.local/api/channel/news/messages" || mx.Kind != WebhookKindMax {
		t.Fatalf("max: %+v err=%v", mx, err)
	}
	var plain map[string]string
	_ = json.Unmarshal(mx.Body, &plain)
	if plain["text"] != "Feed <1>\n\nHello & bye\n\nhttps://example.com/p?a=1&b=2" {
		t.Fatalf("max text: %q", plain["text"])
	}
	for _, c := range []struct{ cfg, want string }{
		{`{"api_base":"https://m","target_id":"42"}`, "https://m/channel/id/42/messages"},
		{`{"api_base":"https://m/","target":"user","target_id":"a b"}`, "https://m/channel/user/a%20b/messages"},
	} {
		got, err := BuildWebhookOutbound(Webhook{Kind: WebhookKindMax, ProviderConfig: []byte(c.cfg)}, feed, entry, nil)
		if err != nil || got.URL != c.want {
			t.Fatalf("max url for %s: %q err=%v", c.cfg, got.URL, err)
		}
	}
	for _, cfg := range []string{``, `{"target_id":"1"}`, `{"api_base":"https://m","target":"group","target_id":"1"}`, `{"api_base":"https://m"}`, `nope`} {
		if _, err := BuildWebhookOutbound(Webhook{Kind: WebhookKindMax, ProviderConfig: []byte(cfg)}, feed, entry, nil); err == nil {
			t.Fatalf("max config %q accepted", cfg)
		}
	}
	if _, err := BuildWebhookOutbound(Webhook{Kind: WebhookKindMax, BodyTemplate: "{{.nope}}", ProviderConfig: []byte(`{"api_base":"https://m","target_id":"1"}`)}, feed, entry, nil); err == nil {
		t.Fatal("max broken template must fail")
	}

	// Empty parts are skipped in the default chat text.
	text, _ = ChatMessageText("", WebhookFeed{}, Entry{Title: " only "}, nil, false)
	if text != "only" {
		t.Fatalf("chat text: %q", text)
	}
}

func TestProviderDeliveryOK_AllKinds(t *testing.T) {
	cases := []struct {
		kind    string
		status  int
		snippet string
		ok      bool
		msg     string
	}{
		{WebhookKindHTTP, 204, "", true, ""},
		{WebhookKindHTTP, 500, "", false, "non-2xx status: 500"},
		{WebhookKindTelegram, 200, `{"ok":true}`, true, ""},
		{WebhookKindTelegram, 200, `{"ok":false,"description":"chat not found"}`, false, "chat not found"},
		{WebhookKindTelegram, 200, `{"ok":false}`, false, "telegram: ok=false"},
		{WebhookKindTelegram, 200, `<html>`, false, "telegram: invalid JSON response"},
		{WebhookKindMax, 200, `{"success":true}`, true, ""},
		{WebhookKindMax, 200, `{"success":false,"detail":"bad token"}`, false, "bad token"},
		{WebhookKindMax, 200, `{"success":false}`, false, "max: success=false"},
		{WebhookKindMax, 200, `x`, false, "max: invalid JSON response"},
		{"", 200, "", true, ""},
	}
	for _, c := range cases {
		ok, msg := ProviderDeliveryOK(c.kind, c.status, c.snippet)
		if ok != c.ok || msg != c.msg {
			t.Errorf("%s %d %q → ok=%v msg=%q, want %v %q", c.kind, c.status, c.snippet, ok, msg, c.ok, c.msg)
		}
	}
}

func TestTelegramConfigHelpers(t *testing.T) {
	raw := []byte(`{"api_base":"https://tg.local/","bot_token":"1:secret","chat_id":"9"}`)
	masked := MaskTelegramConfig(raw)
	if strings.Contains(string(masked), "secret") || !strings.Contains(string(masked), `"chat_id":"9"`) {
		t.Fatalf("masked: %s", masked)
	}
	if string(MaskTelegramConfig([]byte(`{}`))) != `{}` || string(MaskTelegramConfig([]byte(`nope`))) != `{}` {
		t.Fatal("invalid configs must mask to {}")
	}
	merged := MergeTelegramToken([]byte(`{"chat_id":"10"}`), raw)
	c, err := ParseTelegramProviderConfig(merged)
	if err != nil || c.BotToken != "1:secret" || c.ChatID != "10" {
		t.Fatalf("merge keeps token: %+v err=%v", c, err)
	}
	if got := MergeTelegramToken([]byte(`{"bot_token":"2:new","chat_id":"10"}`), raw); string(got) != `{"bot_token":"2:new","chat_id":"10"}` {
		t.Fatalf("explicit token wins: %s", got)
	}
	if got := MergeTelegramToken([]byte(`broken`), raw); string(got) != "broken" {
		t.Fatalf("unparsable incoming returned as-is: %s", got)
	}
	if got := MergeTelegramToken([]byte(`{"chat_id":"10"}`), []byte(`broken`)); string(got) != `{"chat_id":"10"}` {
		t.Fatalf("unparsable existing: %s", got)
	}
	if got := MergeTelegramToken(nil, raw); string(got) != `{"api_base":"","bot_token":"1:secret","chat_id":""}` {
		t.Fatalf("empty incoming adopts token: %s", got)
	}
	if MaxDisplayURL(MaxProviderConfig{APIBase: "https://m/", TargetID: "5"}) != "https://m/channel/id/5/messages" {
		t.Fatal("MaxDisplayURL")
	}
	if _, _, _, err := ResolveWebhookWrite(WebhookKindHTTP, "https://h", []byte(`{bad`)); err == nil {
		t.Fatal("invalid provider_config for http accepted")
	}
	if _, _, _, err := ResolveWebhookWrite(WebhookKindHTTP, "not a url", nil); err == nil {
		t.Fatal("invalid url accepted")
	}
	kind, display, cfg, err := ResolveWebhookWrite("", " https://h/x ", nil)
	if err != nil || kind != WebhookKindHTTP || display != "https://h/x" || string(cfg) != "{}" {
		t.Fatalf("http resolve: %s %s %s err=%v", kind, display, cfg, err)
	}
}
