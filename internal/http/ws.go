package httpserver

import (
	"net/http"

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

	xws.Handler(func(conn *xws.Conn) {
		// Clients only ever send small subscribe/pong messages; the x/net
		// default would let an authenticated peer push 32 MiB frames.
		conn.MaxPayloadBytes = ws.MaxInboundFrameBytes
		client := ws.NewUserClient(s.wsHub, conn, p.UserID)
		s.wsHub.Register(client)
		client.Run()
	}).ServeHTTP(w, r)
}
