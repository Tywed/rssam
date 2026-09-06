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

// authenticateRequest resolves the caller. When the request is authenticated by
// the session cookie (a browser), viaCookie is true so callers can apply the
// cross-origin check to state-changing requests; header tokens are never
// forgeable cross-site and skip it.
func (s *Server) authenticateRequest(ctx context.Context, r *http.Request) (auth.Principal, bool) {
	p, ok, _ := s.authenticateRequestSource(ctx, r)
	return p, ok
}

func (s *Server) authenticateRequestSource(ctx context.Context, r *http.Request) (p auth.Principal, ok bool, viaCookie bool) {
	// A header token takes precedence: a scripted client that sends one is
	// not a browser and must not be subject to the origin check even if a
	// stale cookie is also attached.
	token := tokenFromHeader(r)
	if token == "" {
		if p, ok := s.authenticateSession(ctx, r); ok {
			return p, true, true
		}
		return auth.Principal{}, false, false
	}

	if s.users != nil {
		hash := auth.HashToken(token)
		key, err := s.users.LookupAPIKey(ctx, hash)
		if err == nil {
			_ = s.users.TouchAPIKeyUsed(ctx, key.ID)
			u, uerr := s.users.GetUser(ctx, key.UserID)
			if uerr == nil {
				return auth.Principal{UserID: u.ID, IsAdmin: u.IsAdmin, Username: u.Username}, true, false
			}
		}
	}

	// Dev/migration fallback: global AUTH_TOKEN maps to default user (id=1).
	if s.authToken != "" && token == s.authToken {
		isAdmin := true
		if s.users != nil {
			if u, err := s.users.GetUser(ctx, 1); err == nil {
				isAdmin = u.IsAdmin
				return auth.Principal{UserID: u.ID, IsAdmin: isAdmin, Username: u.Username}, true, false
			}
		}
		return auth.Principal{UserID: 1, IsAdmin: isAdmin}, true, false
	}

	// Invalid header token: fall back to the cookie (keeps previous behaviour
	// for a browser tab that also sends a stale token header).
	if p, ok := s.authenticateSession(ctx, r); ok {
		return p, true, true
	}
	return auth.Principal{}, false, false
}

func tokenFromHeader(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Auth-Token")); v != "" {
		return v
	}
	if v := r.Header.Get("Authorization"); strings.HasPrefix(v, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
	}
	return ""
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
	now := time.Now().UTC()
	if storage.SessionNeedsTouch(sess.ExpiresAt, now) {
		_ = s.sessions.TouchSession(ctx, sessionID, now.Add(storage.DefaultSessionTTL))
	}
	return auth.Principal{UserID: u.ID, IsAdmin: u.IsAdmin, Username: u.Username}, true
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

		p, ok, viaCookie := s.authenticateRequestSource(r.Context(), r)
		if !ok {
			if s.rateLimiter != nil && s.rateLimiter.Enabled() && !s.rateLimiter.Allow(middleware.ClientIP(r)) {
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		// CSRF: a session cookie is attached by the browser automatically, so a
		// state-changing /v1 call must prove it came from our own origin. The
		// web UI never calls /v1 (it uses /ui/* with form CSRF tokens); this
		// covers the case of an attacker page driving the API of a logged-in user.
		if viaCookie && !isSafeMethod(r.Method) && !sameOriginRequest(r) {
			writeError(w, http.StatusForbidden, "cross-origin request rejected: use an API token")
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
	n, err := s.users.CountLoginCapableUsers(ctx)
	if err != nil || n > 0 {
		return false
	}
	return s.adminUsername != "" && s.adminPassword != ""
}

func (s *Server) hasValidAuthToken(r *http.Request) bool {
	p, ok := s.authenticateRequest(r.Context(), r)
	return ok && p.UserID > 0
}
