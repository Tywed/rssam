package storage

import (
	"strings"
	"testing"
)

func TestNormalizeWebhookOnSuccess(t *testing.T) {
	got, err := NormalizeWebhookOnSuccess("")
	if err != nil || got != WebhookOnSuccessNone {
		t.Fatalf("empty: %q %v", got, err)
	}
	got, err = NormalizeWebhookOnSuccess("MARK_READ")
	if err != nil || got != WebhookOnSuccessMarkRead {
		t.Fatalf("mark_read: %q %v", got, err)
	}
	if _, err := NormalizeWebhookOnSuccess("label"); err == nil {
		t.Fatal("expected error for invalid value")
	}
}

func TestMergeOnSuccessActions(t *testing.T) {
	if got := MergeOnSuccessActions(nil); got != WebhookOnSuccessNone {
		t.Fatalf("empty: %q", got)
	}
	if got := MergeOnSuccessActions([]string{WebhookOnSuccessNone, WebhookOnSuccessHash}); got != WebhookOnSuccessHash {
		t.Fatalf("none+hash: %q", got)
	}
	if got := MergeOnSuccessActions([]string{WebhookOnSuccessHash, WebhookOnSuccessDelete}); got != WebhookOnSuccessDelete {
		t.Fatalf("hash+delete: %q", got)
	}
	if got := MergeOnSuccessActions([]string{WebhookOnSuccessNone, WebhookOnSuccessMarkRead}); got != WebhookOnSuccessMarkRead {
		t.Fatalf("none+read: %q", got)
	}
}

func TestNormalizeWebhookName(t *testing.T) {
	got, err := NormalizeWebhookName("  tg  ")
	if err != nil || got != "tg" {
		t.Fatalf("got %q %v", got, err)
	}
	long := strings.Repeat("я", webhookNameMaxLen+1)
	if _, err := NormalizeWebhookName(long); err == nil {
		t.Fatal("expected too-long error")
	}
}
