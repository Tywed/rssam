package httpserver

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/ws"

	"golang.org/x/net/websocket"
)

// The WebSocket upgrade must work through the production handler chain
// (AccessLog → SecurityHeaders → Compress → BodyLimit → RateLimit): every
// wrapper must forward http.Hijacker. The other WS tests bypass wrapMiddleware.
func TestWSUpgradeThroughFullMiddlewareChain(t *testing.T) {
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
	s.compressEnabled = true

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/v1", s.handleWS)
	ts := httptest.NewServer(s.wrapMiddleware(mux))
	t.Cleanup(ts.Close)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/v1"
	cfg, err := websocket.NewConfig(wsURL, "http://localhost/")
	if err != nil {
		t.Fatalf("ws config: %v", err)
	}
	cfg.Header.Set("X-Auth-Token", "secret")
	cfg.Header.Set("Accept-Encoding", "gzip")
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("dial ws through middleware chain: %v", err)
	}
	defer conn.Close()

	if err := websocket.JSON.Send(conn, map[string]any{"action": "subscribe", "channels": []string{"all"}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	awaitSubscribed(t, conn)
	hub.Publish([]string{"all"}, ws.Envelope{Event: "new_entry", Data: map[string]any{"id": 1}})

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var raw string
	if err := websocket.Message.Receive(conn, &raw); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if !strings.Contains(raw, "new_entry") {
		t.Fatalf("unexpected frame: %s", raw)
	}
}
