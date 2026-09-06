package httpserver

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"rssam/internal/ws"
)

// An authenticated client cannot make the server buffer arbitrarily large
// inbound frames: anything above ws.MaxInboundFrameBytes closes the
// connection, while normal subscribe messages keep working.
func TestWSInboundFrameLimit(t *testing.T) {
	hub := ws.NewHub(10, time.Second)
	s := New(Dependencies{
		AuthToken:      "secret",
		WSEnabled:      true,
		WSHub:          hub,
		WSClientBuffer: 10,
		WSPingInterval: time.Second,
		CategoryStore:  &fakeCategoryStore{},
		Logger:         slog.Default(),
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/v1", s.handleWS)
	ts := httptest.NewServer(s.wrapMiddleware(mux))
	t.Cleanup(ts.Close)

	dial := func() *websocket.Conn {
		cfg, err := websocket.NewConfig("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/v1", "http://localhost/")
		if err != nil {
			t.Fatal(err)
		}
		cfg.Header.Set("X-Auth-Token", "secret")
		conn, err := websocket.DialConfig(cfg)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return conn
	}

	// Oversized frame: the server must drop the connection.
	conn := dial()
	big := strings.Repeat("x", ws.MaxInboundFrameBytes+1)
	if err := websocket.Message.Send(conn, big); err != nil {
		t.Fatalf("send oversized: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var raw string
	if err := websocket.Message.Receive(conn, &raw); err == nil {
		t.Fatalf("connection survived an oversized frame; got %q", raw)
	}
	conn.Close()

	// Normal traffic on a fresh connection still works end-to-end.
	conn = dial()
	defer conn.Close()
	if err := websocket.JSON.Send(conn, map[string]any{"action": "subscribe", "channels": []string{"all"}}); err != nil {
		t.Fatal(err)
	}
	awaitSubscribed(t, conn)
	hub.Publish([]string{"all"}, ws.Envelope{Event: "new_entry", Data: map[string]any{"id": 1}})
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := websocket.Message.Receive(conn, &raw); err != nil {
		t.Fatalf("receive after limit: %v", err)
	}
	if !strings.Contains(raw, "new_entry") {
		t.Fatalf("unexpected payload %q", raw)
	}
}
