package worker

import (
	"encoding/json"
	"testing"
	"time"

	"rssam/internal/storage"
)

func TestWebhookEventType(t *testing.T) {
	filter := &storage.Filter{ID: 1, Name: "promo"}
	if got := webhookEventType(storage.WebhookDeliveryContext{Filter: filter}); got != webhookEventEntryMatched {
		t.Fatalf("got %q, want %q", got, webhookEventEntryMatched)
	}
	if got := webhookEventType(storage.WebhookDeliveryContext{}); got != webhookEventNewEntry {
		t.Fatalf("got %q, want %q", got, webhookEventNewEntry)
	}
}

func TestBuildWebhookPayloadBytes_FilterMatch(t *testing.T) {
	sentAt := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	filter := &storage.Filter{ID: 3, Name: "news"}
	ctx := storage.WebhookDeliveryContext{
		Entry:        storage.Entry{ID: 42, FeedID: 7, Title: "Hello", URL: "https://example.com/a"},
		Feed:         storage.WebhookFeed{ID: 7, Title: "Example Feed"},
		Filter:       filter,
		MatchDetails: []byte(`{"rule_id":1}`),
	}
	b, err := buildWebhookPayloadBytes(ctx, sentAt)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["event_version"] != float64(1) {
		t.Fatalf("event_version: %v", got["event_version"])
	}
	if got["event_type"] != webhookEventEntryMatched {
		t.Fatalf("event_type: %v", got["event_type"])
	}
	if got["filter"] == nil {
		t.Fatal("expected filter in payload")
	}
	if got["match_details"] == nil {
		t.Fatal("expected match_details in payload")
	}
	feed, ok := got["feed"].(map[string]any)
	if !ok {
		t.Fatalf("feed: %T", got["feed"])
	}
	if feed["Title"] != "Example Feed" {
		t.Fatalf("feed title: %v", feed["Title"])
	}
	if feed["ID"] != float64(7) {
		t.Fatalf("feed id: %v", feed["ID"])
	}
}

func TestBuildWebhookPayloadBytes_FeedLevel(t *testing.T) {
	sentAt := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	ctx := storage.WebhookDeliveryContext{
		Entry: storage.Entry{ID: 42, FeedID: 7, Title: "Hello", URL: "https://example.com/a"},
		Feed:  storage.WebhookFeed{ID: 7, Title: "Example Feed"},
	}
	b, err := buildWebhookPayloadBytes(ctx, sentAt)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["event_type"] != webhookEventNewEntry {
		t.Fatalf("event_type: %v", got["event_type"])
	}
	if _, ok := got["filter"]; ok {
		t.Fatalf("filter should be omitted, got %v", got["filter"])
	}
	if _, ok := got["match_details"]; ok {
		t.Fatalf("match_details should be omitted, got %v", got["match_details"])
	}
	entry, ok := got["entry"].(map[string]any)
	if !ok {
		t.Fatalf("entry: %T", got["entry"])
	}
	if entry["Title"] != "Hello" {
		t.Fatalf("entry title: %v", entry["Title"])
	}
	feed, ok := got["feed"].(map[string]any)
	if !ok {
		t.Fatalf("feed: %T", got["feed"])
	}
	if feed["Title"] != "Example Feed" {
		t.Fatalf("feed title: %v", feed["Title"])
	}
}

func TestRenderBodyTemplate_FeedTitle(t *testing.T) {
	ctx := storage.WebhookDeliveryContext{
		Entry: storage.Entry{Title: "Hello", URL: "https://example.com/a"},
		Feed:  storage.WebhookFeed{ID: 7, Title: "Вести Кубань"},
	}
	got, err := renderBodyTemplate(`{"text":"{{.feed.Title}}: {{.entry.Title}}"}`, ctx, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"text":"Вести Кубань: Hello"}` {
		t.Fatalf("got %s", got)
	}
}

func TestSignWebhookPayload_HMACSHA256(t *testing.T) {
	secret := "topsecret"
	payload := []byte(`{"hello":"world"}`)
	got := signWebhookPayload(secret, payload)
	// Verified via independent implementation.
	const want = "afd00617ceb8f63e65ea5c310f06bf78c3901e7a713db532e25da26ad63c7236"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestWebhookBackoffWithJitter_Range(t *testing.T) {
	base := 5 * time.Second
	max := time.Hour

	for attempt := 1; attempt <= 6; attempt++ {
		got := webhookBackoffWithJitter(attempt, base, max)
		exp := base
		for i := 1; i < attempt; i++ {
			exp *= 2
		}
		if got < exp {
			t.Fatalf("attempt=%d: got %s < exp %s", attempt, got, exp)
		}
		if got >= exp+(exp/2) {
			t.Fatalf("attempt=%d: got %s out of jitter range", attempt, got)
		}
	}
}

func TestWebhookBackoffWithJitter_ClampedToMax(t *testing.T) {
	base := 10 * time.Minute
	max := 1 * time.Hour

	got := webhookBackoffWithJitter(10, base, max)
	if got < max {
		t.Fatalf("expected >= max due to clamp, got %s", got)
	}
	if got >= max+(max/2) {
		t.Fatalf("expected jitter < max/2, got %s", got)
	}
}

func TestWebhookBackoffWithJitter_TinyDuration(t *testing.T) {
	got := webhookBackoffWithJitter(1, time.Nanosecond, time.Nanosecond)
	if got != time.Nanosecond {
		t.Fatalf("got %s", got)
	}
}
