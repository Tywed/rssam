package ws

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// dialTestHub serves a hub over a real WebSocket so the client's read loop is
// exercised end to end (subscribe → ack → published event).
func dialTestHub(t *testing.T, hub *Hub, userID int64) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		c := NewUserClient(hub, conn, userID)
		hub.Register(c)
		c.Run()
	}))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, err := websocket.Dial(url, "", "http://localhost/")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn
}

func recvEnvelope(t *testing.T, conn *websocket.Conn) (Envelope, json.RawMessage) {
	t.Helper()
	var raw []byte
	if err := websocket.Message.Receive(conn, &raw); err != nil {
		t.Fatalf("receive: %v", err)
	}
	var env struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return Envelope{Event: env.Event}, env.Data
}

func TestClient_SubscribeIsAcknowledgedBeforeEvents(t *testing.T) {
	hub := NewHub(16, time.Minute)
	conn := dialTestHub(t, hub, 1)

	if err := websocket.Message.Send(conn, `{"action":"subscribe","channels":["feed:7","bogus","feed:7"]}`); err != nil {
		t.Fatal(err)
	}
	env, data := recvEnvelope(t, conn)
	if env.Event != "subscribed" {
		t.Fatalf("first frame should be the ack, got %q", env.Event)
	}
	var ack struct {
		Channels []string `json:"channels"`
	}
	if err := json.Unmarshal(data, &ack); err != nil {
		t.Fatal(err)
	}
	// Ack reflects the normalized set: unknown channels dropped, duplicates removed.
	if len(ack.Channels) != 1 || ack.Channels[0] != "feed:7" {
		t.Fatalf("ack channels: %v", ack.Channels)
	}

	// Anything published after the ack is guaranteed to reach the client.
	hub.PublishToUser(1, []string{"feed:7"}, Envelope{Event: "ping-test", Data: map[string]int{"n": 1}})
	env, _ = recvEnvelope(t, conn)
	if env.Event != "ping-test" {
		t.Fatalf("expected published event, got %q", env.Event)
	}
}

func TestClient_IgnoresGarbageAndPong(t *testing.T) {
	hub := NewHub(16, time.Minute)
	conn := dialTestHub(t, hub, 1)

	for _, frame := range []string{`{"action":"pong"}`, `not json`, `{"action":"unsubscribe"}`} {
		if err := websocket.Message.Send(conn, frame); err != nil {
			t.Fatal(err)
		}
	}
	// A real subscribe afterwards still works — the loop did not die.
	if err := websocket.Message.Send(conn, `{"action":"subscribe","channels":["all"]}`); err != nil {
		t.Fatal(err)
	}
	env, _ := recvEnvelope(t, conn)
	if env.Event != "subscribed" {
		t.Fatalf("got %q, want subscribed", env.Event)
	}
}
