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
	p, ok := s.authenticateRequest(r.Context(), r)
	if !ok || p.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	xws.Handler(func(conn *xws.Conn) {
		client := ws.NewUserClient(s.wsHub, conn, p.UserID)
		s.wsHub.Register(client)
		client.Run()
	}).ServeHTTP(w, r)
}
