package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/ws"

	"golang.org/x/net/websocket"
)

func TestWSUnauthorized(t *testing.T) {
	hub := ws.NewHub(10, time.Second)
	s := New(Dependencies{
		AuthToken:     "secret",
		WSEnabled:     true,
		WSHub:         hub,
		CategoryStore: &fakeCategoryStore{},
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/v1", s.handleWS)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/ws/v1", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestWSConnectAndReceiveNewEntry(t *testing.T) {
	hub := ws.NewHub(10, time.Second)
	s := New(Dependencies{
		AuthToken:      "secret",
		WSEnabled:      true,
		WSHub:          hub,
		WSClientBuffer: 10,
		WSPingInterval: time.Second,
		CategoryStore:  &fakeCategoryStore{},
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/v1", s.handleWS)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/v1"
	cfg, err := websocket.NewConfig(wsURL, "http://localhost/")
	if err != nil {
		t.Fatalf("ws config: %v", err)
	}
	cfg.Header.Set("X-Auth-Token", "secret")
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()

	subscribe := map[string]any{
		"action":   "subscribe",
		"channels": []string{"all"},
	}
	if err := websocket.JSON.Send(conn, subscribe); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	hub.Publish([]string{"all"}, ws.Envelope{
		Event: "new_entry",
		Data: map[string]any{
			"entry": map[string]any{
				"id":      1,
				"feed_id": 7,
				"title":   "hello",
			},
		},
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		_ = conn.SetDeadline(deadline)
		payload := make([]byte, 4096)
		n, err := conn.Read(payload)
		if err != nil {
			t.Fatalf("read message: %v", err)
		}
		var got struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal(payload[:n], &got); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if got.Event == "new_entry" {
			break
		}
	}
}
