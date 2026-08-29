package httpserver

import (
	"net/http"

	"rssam/internal/auth"
)

func principalFromRequest(r *http.Request) (auth.Principal, bool) {
	return auth.PrincipalFromContext(r.Context())
}

func requireUser(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, ok := principalFromRequest(r)
	if !ok || p.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return auth.Principal{}, false
	}
	return p, true
}

func requireAdmin(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, ok := requireUser(w, r)
	if !ok {
		return auth.Principal{}, false
	}
	if !p.IsAdmin {
		writeError(w, http.StatusForbidden, "admin required")
		return auth.Principal{}, false
	}
	return p, true
}
