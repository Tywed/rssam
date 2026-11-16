package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
	"rssam/internal/ws"

	xws "golang.org/x/net/websocket"
)

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.wsEnabled {
		writeError(w, http.StatusNotFound, "websocket is disabled")
		return
	}
	p, ok, viaCookie := s.authenticateRequestSource(r.Context(), r)
	if !ok || p.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// Cross-site WebSocket hijacking: browsers attach the session cookie to a
	// WebSocket handshake from any page and CORS does not apply, so a
	// cookie-authenticated upgrade must originate from our own origin.
	// Browsers always send Origin on WebSocket handshakes.
	if viaCookie && !sameOriginRequest(r) {
		writeError(w, http.StatusForbidden, "cross-origin websocket rejected")
		return
	}

	if !s.wsHub.Reserve(p.UserID) {
		writeError(w, http.StatusTooManyRequests, "too many websocket connections for this user")
		return
	}
	defer s.wsHub.Release(p.UserID)

	xws.Handler(func(conn *xws.Conn) {
		// Clients only ever send small subscribe/pong messages; the x/net
		// default would let an authenticated peer push 32 MiB frames.
		conn.MaxPayloadBytes = ws.MaxInboundFrameBytes
		client := ws.NewUserClient(s.wsHub, conn, p.UserID)
		client.SessionID = p.SessionID
		client.KeyID = p.KeyID
		client.Alive = s.wsCredentialAlive(p)
		s.wsHub.Register(client)
		client.Run()
	}).ServeHTTP(w, r)
}

type apiKeyExpiryStore interface {
	APIKeyExpiry(ctx context.Context, keyID int64) (*time.Time, error)
}

// wsCredentialAlive re-validates the socket's credential on each ping tick
// (one primary-key lookup per PingInterval per socket).
func (s *Server) wsCredentialAlive(p auth.Principal) func() bool {
	return func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if p.SessionID != "" {
			_, err := s.sessions.LookupSession(ctx, p.SessionID)
			return err == nil || !errors.Is(err, storage.ErrNotFound)
		}
		if p.KeyID > 0 {
			ks, ok := s.users.(apiKeyExpiryStore)
			if !ok {
				return true
			}
			exp, err := ks.APIKeyExpiry(ctx, p.KeyID)
			if errors.Is(err, storage.ErrNotFound) {
				return false
			}
			return err != nil || exp == nil || exp.After(time.Now())
		}
		return true
	}
}
