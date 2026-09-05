package ws

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"rssam/internal/storage"
)

type stubCounter struct{}

func (stubCounter) CountUnreadByFeed(context.Context, int64) (int, error)        { return 3, nil }
func (stubCounter) CountUnreadByCategory(context.Context, int64) (int, error)    { return 0, nil }
func (stubCounter) CountUnreadGlobalForUser(context.Context, int64) (int, error) { return 7, nil }

// newAttachedClient registers a client without a real websocket connection so
// we can observe what the hub would send it.
func newAttachedClient(t *testing.T, hub *Hub, userID int64) *Client {
	t.Helper()
	c := &Client{hub: hub, UserID: userID, send: make(chan []byte, 16), subscriptions: map[string]struct{}{}}
	c.setSubscriptions([]string{ChannelAll})
	hub.Register(c)
	return c
}

func drain(c *Client) []string {
	var out []string
	for {
		select {
		case b := <-c.send:
			out = append(out, string(b))
		case <-time.After(50 * time.Millisecond):
			return out
		}
	}
}

// Regression: events used to be broadcast to every authenticated socket and the
// new_entry payload was the whole storage.Entry (content included).
func TestPublisher_NewEntriesAreTenantScopedAndMinimal(t *testing.T) {
	hub := NewHub(16, time.Second)
	owner := newAttachedClient(t, hub, 1)
	other := newAttachedClient(t, hub, 2)

	pub := NewPublisher(nil, hub, stubCounter{})
	feed := storage.Feed{ID: 10, UserID: 1, Title: "Owner feed"}
	secret := "SECRET-BODY-MUST-NOT-LEAK"
	pub.PublishNewEntries(context.Background(), feed, []storage.Entry{{
		ID: 100, FeedID: 10, Title: "hello", URL: "https://example.com/a", Content: secret, Hash: "deadbeef",
	}})

	if got := drain(other); len(got) != 0 {
		t.Fatalf("user 2 must not receive user 1 events, got %d: %v", len(got), got)
	}
	got := drain(owner)
	if len(got) < 2 {
		t.Fatalf("owner expected new_entry + counters, got %v", got)
	}
	var env struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(got[0]), &env); err != nil || env.Event != "new_entry" {
		t.Fatalf("first frame should be new_entry: %s", got[0])
	}
	if strings.Contains(got[0], secret) || strings.Contains(got[0], "deadbeef") || strings.Contains(got[0], "Content") {
		t.Fatalf("payload leaks internal fields: %s", got[0])
	}
	if !strings.Contains(got[0], `"id":100`) || !strings.Contains(got[0], `"title":"hello"`) || !strings.Contains(got[0], `"feed_title":"Owner feed"`) {
		t.Fatalf("payload missing whitelisted fields: %s", got[0])
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, `"scope":"global"`) || !strings.Contains(joined, `"unread_count":7`) {
		t.Fatalf("expected per-user global counter, got %s", joined)
	}
}

func TestPublisher_FeedStatusIsTenantScoped(t *testing.T) {
	hub := NewHub(16, time.Second)
	owner := newAttachedClient(t, hub, 5)
	other := newAttachedClient(t, hub, 6)
	pub := NewPublisher(nil, hub, nil)
	pub.PublishFeedStatusChanged(storage.Feed{ID: 1, UserID: 5}, nil)
	if n := len(drain(other)); n != 0 {
		t.Fatalf("other user got %d frames", n)
	}
	if n := len(drain(owner)); n != 1 {
		t.Fatalf("owner got %d frames, want 1", n)
	}
}

func TestHub_PublishWithoutUserIsBroadcast(t *testing.T) {
	hub := NewHub(16, time.Second)
	a := newAttachedClient(t, hub, 1)
	b := newAttachedClient(t, hub, 2)
	hub.Publish([]string{ChannelAll}, Envelope{Event: "system"})
	if len(drain(a)) != 1 || len(drain(b)) != 1 {
		t.Fatal("legacy Publish must reach all clients")
	}
}
