package httpserver

import (
	"context"
	"net/http"

	"rssam/internal/auth"
)

func (s *Server) authenticateRequest(ctx context.Context, r *http.Request) (auth.Principal, bool) {
	p, ok, _ := s.authenticateRequestSource(ctx, r)
	return p, ok
}
