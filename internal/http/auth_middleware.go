package httpserver

import (
	"context"
	"net/http"
	"strings"
	"time"

	"rssam/internal/auth"
	"rssam/internal/http/middleware"
	"rssam/internal/storage"
)

func (s *Server) authenticateRequest(ctx context.Context, r *http.Request) (auth.Principal, bool) {
	if p, ok := s.authenticateSession(ctx, r); ok {
		return p, true
	}

	token := strings.TrimSpace(r.Header.Get("X-Auth-Token"))
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if token == "" {
		return auth.Principal{}, false
	}

	if s.users != nil {
		hash := auth.HashToken(token)
		key, err := s.users.LookupAPIKey(ctx, hash)
		if err == nil {
			_ = s.users.TouchAPIKeyUsed(ctx, key.ID)
			u, uerr := s.users.GetUser(ctx, key.UserID)
			if uerr == nil {
				return auth.Principal{UserID: u.ID, IsAdmin: u.IsAdmin}, true
			}
		}
	}

	// Dev/migration fallback: global AUTH_TOKEN maps to default user (id=1).
	if s.authToken != "" && token == s.authToken {
		isAdmin := true
		if s.users != nil {
			if u, err := s.users.GetUser(ctx, 1); err == nil {
				isAdmin = u.IsAdmin
				return auth.Principal{UserID: u.ID, IsAdmin: isAdmin}, true
			}
		}
		return auth.Principal{UserID: 1, IsAdmin: isAdmin}, true
	}

	return auth.Principal{}, false
}

func (s *Server) authenticateSession(ctx context.Context, r *http.Request) (auth.Principal, bool) {
	if s.sessions == nil || s.users == nil {
		return auth.Principal{}, false
	}
	sessionID := auth.SessionIDFromRequest(r)
	if sessionID == "" {
		return auth.Principal{}, false
	}
	sess, err := s.sessions.LookupSession(ctx, sessionID)
	if err != nil {
		return auth.Principal{}, false
	}
	u, err := s.users.GetUser(ctx, sess.UserID)
	if err != nil {
		return auth.Principal{}, false
	}
	expires := time.Now().UTC().Add(storage.DefaultSessionTTL)
	_ = s.sessions.TouchSession(ctx, sessionID, expires)
	return auth.Principal{UserID: u.ID, IsAdmin: u.IsAdmin}, true
}

func (s *Server) wrapAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bootstrap: POST /v1/users without auth when no users exist.
		if r.Method == http.MethodPost && r.URL.Path == "/v1/users" {
			if s.allowBootstrapCreateUser(r.Context()) {
				next.ServeHTTP(w, r)
				return
			}
		}

		p, ok := s.authenticateRequest(r.Context(), r)
		if !ok {
			if s.rateLimiter != nil && s.rateLimiter.Enabled() && !s.rateLimiter.Allow(middleware.ClientIP(r)) {
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		r = r.WithContext(auth.WithPrincipal(r.Context(), p))
		next.ServeHTTP(w, r)
	})
}

func (s *Server) allowBootstrapCreateUser(ctx context.Context) bool {
	if s.users == nil {
		return false
	}
	n, err := s.users.CountUsers(ctx)
	if err != nil || n > 0 {
		return false
	}
	return s.adminUsername != "" && s.adminPassword != ""
}

func (s *Server) hasValidAuthToken(r *http.Request) bool {
	p, ok := s.authenticateRequest(r.Context(), r)
	return ok && p.UserID > 0
}
