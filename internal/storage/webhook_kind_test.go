package storage

import (
	"strings"
	"testing"
)

func TestTelegramAPIBase_DefaultOfficialNotLAN(t *testing.T) {
	got := TelegramSendURL("", "secret-token")
	if !strings.HasPrefix(got, TelegramAPIDefault+"/") {
		t.Fatalf("got %s", got)
	}
	if strings.Contains(got, "192.168.") || strings.Contains(got, ":8787") || strings.Contains(got, ":8000") {
		t.Fatalf("LAN default leaked: %s", got)
	}
	if !strings.Contains(got, "/botsecret-token/sendMessage") {
		t.Fatalf("token path: %s", got)
	}
	if strings.Contains(TelegramDisplayURL(""), "secret-token") {
		t.Fatal("display URL must not include token")
	}
}

func TestResolveWebhookWrite_MaxRequiresAPIBase(t *testing.T) {
	_, _, _, err := ResolveWebhookWrite(WebhookKindMax, "", []byte(`{"target":"chat_id","target_id":"1"}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveWebhookWrite_TelegramDisplayURL(t *testing.T) {
	_, display, _, err := ResolveWebhookWrite(WebhookKindTelegram, "", []byte(`{"bot_token":"t","chat_id":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if display != TelegramAPIDefault+"/bot/sendMessage" {
		t.Fatalf("display=%s", display)
	}
}

func TestProviderDeliveryOK_Telegram(t *testing.T) {
	ok, _ := ProviderDeliveryOK(WebhookKindTelegram, 200, `{"ok":true}`)
	if !ok {
		t.Fatal("expected ok")
	}
	ok, msg := ProviderDeliveryOK(WebhookKindTelegram, 200, `{"ok":false,"description":"bad"}`)
	if ok || msg != "bad" {
		t.Fatalf("ok=%v msg=%q", ok, msg)
	}
}
