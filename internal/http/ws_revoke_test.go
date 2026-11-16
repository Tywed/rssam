package httpserver

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
	"rssam/internal/ws"

	"golang.org/x/net/websocket"
)

func newWSTestServer(t *testing.T, sessions *memSessionStore, users *uiMemUsersWS) (*Server, string, string) {
	t.Helper()
	hub := ws.NewHub(10, 50*time.Millisecond)
	s := New(Dependencies{
		UserStore:      users,
		SessionStore:   sessions,
		WSEnabled:      true,
		WSHub:          hub,
		WSClientBuffer: 10,
		WSPingInterval: 50 * time.Millisecond,
		Logger:         slog.Default(),
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/v1", s.handleWS)
	ts := httptest.NewServer(s.wrapMiddleware(mux))
	t.Cleanup(ts.Close)
	return s, "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/v1", ts.URL + "/"
}

func wsDial(t *testing.T, wsURL, origin string, hdr map[string]string) (*websocket.Conn, error) {
	t.Helper()
	cfg, err := websocket.NewConfig(wsURL, origin)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		cfg.Header.Set(k, v)
	}
	return websocket.DialConfig(cfg)
}

// closedWithin reports whether a read on conn fails before d elapses.
func closedWithin(conn *websocket.Conn, d time.Duration) bool {
	_ = conn.SetReadDeadline(time.Now().Add(d))
	for {
		var msg []byte
		if err := websocket.Message.Receive(conn, &msg); err != nil {
			return !strings.Contains(err.Error(), "timeout")
		}
	}
}

func TestWS_LogoutClosesSocket(t *testing.T) {
	sessions := &memSessionStore{s: storage.Session{UserID: 1, SessionID: "sess1", ExpiresAt: time.Now().Add(time.Hour)}}
	users := &uiMemUsersWS{user: storage.User{ID: 1, Role: auth.RoleAdmin}, token: "tok"}
	s, wsURL, origin := newWSTestServer(t, sessions, users)

	conn, err := wsDial(t, wsURL, origin, map[string]string{"Cookie": auth.SessionCookieName + "=sess1"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	other, err := wsDial(t, wsURL, "https://evil.example/", map[string]string{"X-Auth-Token": "tok"})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if n := s.wsHub.UserConnections(1); n != 2 {
		t.Fatalf("connections=%d want 2", n)
	}
	// The server's session store is the revoking wrapper; a logout goes through it.
	if err := s.sessions.DeleteSession(t.Context(), "sess1"); err != nil {
		t.Fatal(err)
	}
	if !closedWithin(conn, 2*time.Second) {
		t.Fatal("socket of the deleted session stayed open")
	}
	// The API-key socket of the same user is untouched by a session logout…
	if closedWithin(other, 300*time.Millisecond) {
		t.Fatal("api-key socket must survive a session logout")
	}
	// …but dies when its key is deleted.
	if err := s.users.DeleteAPIKey(t.Context(), 1, 1); err != nil {
		t.Fatal(err)
	}
	if !closedWithin(other, 2*time.Second) {
		t.Fatal("socket of the deleted api key stayed open")
	}
}

func TestWS_PingRecheckClosesExpiredSession(t *testing.T) {
	sessions := &memSessionStore{s: storage.Session{UserID: 1, SessionID: "sess1", ExpiresAt: time.Now().Add(time.Hour)}}
	users := &uiMemUsersWS{user: storage.User{ID: 1, Role: auth.RoleAdmin}}
	_, wsURL, origin := newWSTestServer(t, sessions, users)
	conn, err := wsDial(t, wsURL, origin, map[string]string{"Cookie": auth.SessionCookieName + "=sess1"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Expire the row behind the hub's back (no Delete* call → no kick).
	sessions.expire()
	if !closedWithin(conn, 2*time.Second) {
		t.Fatal("socket must be closed by the ping-time recheck")
	}
}

func TestWS_PerUserConnectionCap(t *testing.T) {
	sessions := &memSessionStore{s: storage.Session{UserID: 1, SessionID: "sess1", ExpiresAt: time.Now().Add(time.Hour)}}
	users := &uiMemUsersWS{user: storage.User{ID: 1, Role: auth.RoleAdmin}}
	_, wsURL, origin := newWSTestServer(t, sessions, users)
	hdr := map[string]string{"Cookie": auth.SessionCookieName + "=sess1"}
	var conns []*websocket.Conn
	t.Cleanup(func() {
		for _, c := range conns {
			c.Close()
		}
	})
	for i := 0; i < ws.MaxConnectionsPerUser; i++ {
		c, err := wsDial(t, wsURL, origin, hdr)
		if err != nil {
			t.Fatalf("dial %d: %v", i+1, err)
		}
		conns = append(conns, c)
	}
	req, _ := http.NewRequest(http.MethodGet, strings.Replace(wsURL, "ws", "http", 1), nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Origin", origin)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "sess1"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("connection %d: status=%d want 429", ws.MaxConnectionsPerUser+1, resp.StatusCode)
	}
	conns[0].Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := wsDial(t, wsURL, origin, hdr)
		if err == nil {
			conns = append(conns, c)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot not freed after close: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
