package httpserver

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.json
var openAPISpec []byte

func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openAPISpec)
}
