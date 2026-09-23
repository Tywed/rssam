package ws

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"rssam/internal/storage"
)

type stubCounter struct{}

func (stubCounter) CountUnreadByFeed(context.Context, int64, int64) (int, error)     { return 3, nil }
func (stubCounter) CountUnreadByCategory(context.Context, int64, int64) (int, error) { return 0, nil }
func (stubCounter) CountUnreadGlobalForUser(context.Context, int64) (int, error)     { return 7, nil }

type stubSubs []storage.Subscription

func (s stubSubs) ListFeedSubscribers(context.Context, int64) ([]storage.Subscription, error) {
	return s, nil
}

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

// new_entry events are delivered only to the owning user and carry the
// minimal payload, not the whole storage.Entry.
func TestPublisher_NewEntriesAreTenantScopedAndMinimal(t *testing.T) {
	hub := NewHub(16, time.Second)
	owner := newAttachedClient(t, hub, 1)
	other := newAttachedClient(t, hub, 2)

	pub := NewPublisher(nil, hub, stubCounter{}, nil)
	catID := int64(3)
	feed := storage.Feed{ID: 10, OwnerID: 1, Title: "Owner feed"}
	subs := []storage.Subscription{{UserID: 1, FeedID: 10, CategoryID: &catID}}
	secret := "SECRET-BODY-MUST-NOT-LEAK"
	pub.PublishNewEntries(context.Background(), feed, subs, []storage.Entry{{
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
	if !strings.Contains(got[0], `"id":100`) || !strings.Contains(got[0], `"title":"hello"`) || !strings.Contains(got[0], `"feed_title":"Owner feed"`) || !strings.Contains(got[0], `"category_id":3`) {
		t.Fatalf("payload missing whitelisted fields: %s", got[0])
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, `"scope":"global"`) || !strings.Contains(joined, `"unread_count":7`) {
		t.Fatalf("expected per-user global counter, got %s", joined)
	}
}

// A shared feed fans out to every subscriber with that subscriber's own
// category; users who never subscribed hear nothing.
func TestPublisher_NewEntriesFanOutToEverySubscriber(t *testing.T) {
	hub := NewHub(16, time.Second)
	owner := newAttachedClient(t, hub, 1)
	reader := newAttachedClient(t, hub, 2)
	stranger := newAttachedClient(t, hub, 3)

	pub := NewPublisher(nil, hub, stubCounter{}, nil)
	ownerCat, readerCat := int64(3), int64(8)
	feed := storage.Feed{ID: 10, OwnerID: 1, Title: "Shared"}
	subs := []storage.Subscription{
		{UserID: 1, FeedID: 10, CategoryID: &ownerCat},
		{UserID: 2, FeedID: 10, CategoryID: &readerCat},
	}
	pub.PublishNewEntries(context.Background(), feed, subs, []storage.Entry{{ID: 100, FeedID: 10, Title: "hello"}})

	if got := drain(stranger); len(got) != 0 {
		t.Fatalf("non-subscriber must not receive events, got %v", got)
	}
	for _, c := range []struct {
		name  string
		cl    *Client
		catID string
	}{{"owner", owner, `"category_id":3`}, {"reader", reader, `"category_id":8`}} {
		got := drain(c.cl)
		if len(got) < 2 {
			t.Fatalf("%s expected new_entry + counters, got %v", c.name, got)
		}
		if !strings.Contains(got[0], `"event":"new_entry"`) || !strings.Contains(got[0], `"id":100`) || !strings.Contains(got[0], c.catID) {
			t.Fatalf("%s got %s", c.name, got[0])
		}
	}
}

func TestPublisher_FeedStatusIsTenantScoped(t *testing.T) {
	hub := NewHub(16, time.Second)
	owner := newAttachedClient(t, hub, 5)
	other := newAttachedClient(t, hub, 6)
	pub := NewPublisher(nil, hub, nil, stubSubs{{UserID: 5, FeedID: 1}})
	pub.PublishFeedStatusChanged(storage.Feed{ID: 1, OwnerID: 5}, nil)
	if n := len(drain(other)); n != 0 {
		t.Fatalf("other user got %d frames", n)
	}
	if n := len(drain(owner)); n != 1 {
		t.Fatalf("owner got %d frames, want 1", n)
	}
}

func TestPublisher_FeedStatusCarriesPersistedState(t *testing.T) {
	hub := NewHub(16, time.Second)
	owner := newAttachedClient(t, hub, 5)
	pub := NewPublisher(nil, hub, nil, stubSubs{{UserID: 5, FeedID: 1}})
	next := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	pub.PublishFeedStatusChanged(storage.Feed{
		ID: 1, OwnerID: 5, ParsingErrorCount: 4, PollPaused: true, LastError: "old", NextCheckAt: &next,
	}, errors.New("fetch feed: 503"))
	got := drain(owner)
	if len(got) != 1 {
		t.Fatalf("got %d frames, want 1", len(got))
	}
	for _, want := range []string{
		`"event":"feed_status_changed"`, `"feed_id":1`, `"success":false`,
		`"parsing_error_count":4`, `"poll_paused":true`, `"manual_paused":false`,
		`"parsing_error_message":"fetch feed: 503"`, `"next_check_at":"2026-09-08T12:00:00Z"`,
	} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("frame missing %s: %s", want, got[0])
		}
	}

	pub.PublishFeedStatusChanged(storage.Feed{ID: 1, OwnerID: 5}, nil)
	got = drain(owner)
	if len(got) != 1 || !strings.Contains(got[0], `"success":true`) || !strings.Contains(got[0], `"parsing_error_count":0`) || !strings.Contains(got[0], `"parsing_error_message":""`) {
		t.Fatalf("success frame = %v", got)
	}
}

func TestHub_PublishWithoutUserReachesNobody(t *testing.T) {
	hub := NewHub(16, time.Second)
	a := newAttachedClient(t, hub, 1)
	b := newAttachedClient(t, hub, 2)
	hub.PublishToUser(0, []string{ChannelAll}, Envelope{Event: "system"})
	if len(drain(a)) != 0 || len(drain(b)) != 0 {
		t.Fatal("an event without an owner must not be broadcast")
	}
}
