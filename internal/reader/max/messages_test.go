package max

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const fixtureMessages = `{
  "messages": [
    {
      "time": 1716460800000,
      "text": "Line one\nLine two\nhttps://example.com/x",
      "message_url": "https://max.ru/rosgvard_krd/42",
      "id": "42",
      "forwarded_message": {
        "chat_name": "Source",
        "chat_link": "https://max.ru/source",
        "message_text": "Forwarded body"
      }
    }
  ]
}`

func TestEntryFromMessage(t *testing.T) {
	var resp APIResponse
	if err := json.Unmarshal([]byte(fixtureMessages), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Messages) != 1 {
		t.Fatalf("messages=%d", len(resp.Messages))
	}
	e := EntryFromMessage("rosgvard_krd", resp.Messages[0])
	if e.Title != "Line one" {
		t.Fatalf("title=%q", e.Title)
	}
	if e.URL != "https://max.ru/rosgvard_krd/42" {
		t.Fatalf("url=%q", e.URL)
	}
	if e.Author == nil || *e.Author != "rosgvard_krd" {
		t.Fatalf("author=%v", e.Author)
	}
	if e.PublishedAt == nil {
		t.Fatal("expected published_at")
	}
	if e.PublishedAt.Unix() != time.UnixMilli(1716460800000).Unix() {
		t.Fatalf("published_at=%v", e.PublishedAt)
	}
	if !strings.Contains(e.Content, "<br>") {
		t.Fatalf("expected nl2br in content: %q", e.Content)
	}
	if !strings.Contains(e.Content, `href="https://example.com/x"`) {
		t.Fatalf("expected linkify: %q", e.Content)
	}
	if !strings.Contains(e.Content, "max-forwarded") {
		t.Fatalf("expected forwarded block: %q", e.Content)
	}
}

func TestAPIMessage_numericID(t *testing.T) {
	const raw = `{"messages":[{"time":1716460800000,"text":"Hi","id":42}]}`
	var resp APIResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Messages[0].ID.String() != "42" {
		t.Fatalf("id=%q", resp.Messages[0].ID.String())
	}
}
