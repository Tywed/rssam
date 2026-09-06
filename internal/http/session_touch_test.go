package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type countingSessions struct {
	memSessionStore
	touches int
}

func (c *countingSessions) TouchSession(_ context.Context, _ string, _ time.Time) error {
	c.touches++
	return nil
}

// The sliding expiry is only written when the session was last refreshed more
// than SessionTouchInterval ago, not on every authenticated request.
func TestAuthenticateSession_TouchThrottled(t *testing.T) {
	sessions := &countingSessions{}
	s := &Server{sessions: sessions, users: &uiMemUsersWS{user: storage.User{ID: 7, Username: "kim"}}}
	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "s7"})

	// Fresh session (touched just now): no write.
	sessions.s = storage.Session{UserID: 7, SessionID: "s7", ExpiresAt: time.Now().Add(storage.DefaultSessionTTL)}
	for range 5 {
		p, ok := s.authenticateRequest(req.Context(), req)
		if !ok || p.UserID != 7 || p.Username != "kim" {
			t.Fatalf("auth failed: ok=%v p=%+v", ok, p)
		}
	}
	if sessions.touches != 0 {
		t.Fatalf("fresh session touched %d times, want 0", sessions.touches)
	}

	// Session last refreshed 2h ago: exactly one write.
	sessions.s.ExpiresAt = time.Now().Add(storage.DefaultSessionTTL - 2*time.Hour)
	if _, ok := s.authenticateRequest(req.Context(), req); !ok {
		t.Fatal("auth failed")
	}
	if sessions.touches != 1 {
		t.Fatalf("stale session touched %d times, want 1", sessions.touches)
	}
}
