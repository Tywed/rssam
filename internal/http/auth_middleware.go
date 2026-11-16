package httpserver

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"rssam/internal/auth"
	"rssam/internal/http/middleware"
	"rssam/internal/storage"
)

// authenticateRequestSource resolves the caller. When the request is
// authenticated by the session cookie (a browser), viaCookie is true so
// callers can apply the cross-origin check to state-changing requests;
// header tokens are never forgeable cross-site and skip it.
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
		if s.knownBadToken(hash) || s.badTokenLimiter.Exhausted(middleware.ClientIP(r)) {
			return auth.Principal{}, false, false
		}
		key, err := s.users.LookupAPIKey(ctx, hash)
		if err == nil && !key.Expired(time.Now()) {
			s.touchAPIKey(ctx, key.ID)
			u, uerr := s.users.GetUser(ctx, key.UserID)
			if uerr == nil {
				return auth.Principal{UserID: u.ID, Role: u.Role, Username: u.Username, Scope: key.Scope, KeyID: key.ID}, true, false
			}
		}
		if errors.Is(err, storage.ErrNotFound) {
			s.rememberBadToken(hash)
			s.badTokenLimiter.Allow(middleware.ClientIP(r))
		}
	}

	// Invalid header token: fall back to the cookie (a browser tab may also
	// send a stale token header).
	if p, ok := s.authenticateSession(ctx, r); ok {
		return p, true, true
	}
	return auth.Principal{}, false, false
}

const (
	badTokenTTL = time.Minute
	badTokenMax = 4096
)

// badTokenCache remembers token hashes that missed the api_keys table.
type badTokenCache struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func (c *badTokenCache) known(hash string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.m[hash]
	if !ok {
		return false
	}
	if time.Since(t) > badTokenTTL {
		delete(c.m, hash)
		return false
	}
	return true
}

// remember inserts hash; when the cache is full it first drops expired
// entries and, if that is not enough, the oldest half — one pass per
// badTokenMax/2 misses instead of a full scan per miss.
func (c *badTokenCache) remember(hash string) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = make(map[string]time.Time, 64)
	}
	if _, ok := c.m[hash]; !ok && len(c.m) >= badTokenMax {
		for k, t := range c.m {
			if now.Sub(t) > badTokenTTL {
				delete(c.m, k)
			}
		}
		if len(c.m) >= badTokenMax {
			type kv struct {
				k string
				t int64
			}
			all := make([]kv, 0, len(c.m))
			for k, t := range c.m {
				all = append(all, kv{k, t.UnixNano()})
			}
			slices.SortFunc(all, func(a, b kv) int { return cmp.Compare(a.t, b.t) })
			for _, e := range all[:len(all)/2] {
				delete(c.m, e.k)
			}
		}
	}
	c.m[hash] = now
}

func (s *Server) knownBadToken(hash string) bool { return s.badTokens.known(hash) }

func (s *Server) rememberBadToken(hash string) { s.badTokens.remember(hash) }

// touchAPIKey updates last_used_at at most once a minute per key: the
// column is informational, one UPDATE per request is not.
func (s *Server) touchAPIKey(ctx context.Context, keyID int64) {
	now := time.Now()
	if v, ok := s.apiKeyTouched.Load(keyID); ok && now.Sub(v.(time.Time)) < time.Minute {
		return
	}
	s.apiKeyTouched.Store(keyID, now)
	_ = s.users.TouchAPIKeyUsed(ctx, keyID)
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
	sess, u, err := storage.LookupSessionUser(ctx, s.sessions, s.users, sessionID)
	if err != nil {
		return auth.Principal{}, false
	}
	now := time.Now().UTC()
	if ttl := s.sessionTTL(); storage.SessionNeedsTouch(sess.ExpiresAt, now, ttl) {
		_ = s.sessions.TouchSession(ctx, sessionID, now.Add(ttl))
	}
	return auth.Principal{UserID: u.ID, Role: u.Role, Username: u.Username, SessionID: sessionID}, true
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
		// web UI never calls /v1 (it uses /ui/* with form CSRF tokens).
		if viaCookie && !isSafeMethod(r.Method) && !sameOriginRequest(r) {
			writeError(w, http.StatusForbidden, "cross-origin request rejected: use an API token")
			return
		}
		if !auth.ScopeAllows(p.Scope, r.Method, r.URL.Path) {
			writeError(w, http.StatusForbidden, "api key scope "+p.Scope+" does not allow this request")
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

func (s *Server) sessionTTL() time.Duration {
	if s.sessionMaxAge > 0 {
		return s.sessionMaxAge
	}
	return storage.DefaultSessionTTL
}
