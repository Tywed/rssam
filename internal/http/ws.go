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
	if !s.hasValidAuthToken(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	xws.Handler(func(conn *xws.Conn) {
		client := ws.NewClient(s.wsHub, conn)
		s.wsHub.Register(client)
		client.Run()
	}).ServeHTTP(w, r)
}
