package httpserver

import (
	"errors"
	"net/http"

	"rssam/internal/auth"
	"rssam/internal/storage"
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

// requireRole is requireUser plus a minimum role ("" = any).
func requireRole(w http.ResponseWriter, r *http.Request, role string) (auth.Principal, bool) {
	p, ok := requireUser(w, r)
	if !ok {
		return auth.Principal{}, false
	}
	if role != "" && !auth.RoleAtLeast(p.Role, role) {
		writeError(w, http.StatusForbidden, role+" role required")
		return auth.Principal{}, false
	}
	return p, true
}

func requireAdmin(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	return requireRole(w, r, auth.RoleAdmin)
}

// requireStore is the common handler prologue: authenticated principal of at
// least role ("" = any) plus a configured backing store, otherwise the
// response is already written.
func requireStore(w http.ResponseWriter, r *http.Request, role string, configured bool, unavailable string) (auth.Principal, bool) {
	p, ok := requireRole(w, r, role)
	if !ok {
		return auth.Principal{}, false
	}
	if !configured {
		writeError(w, http.StatusServiceUnavailable, unavailable)
		return auth.Principal{}, false
	}
	return p, true
}

// requireStoreID is requireStore followed by the {id} path parameter.
func requireStoreID(w http.ResponseWriter, r *http.Request, role string, configured bool, unavailable string) (auth.Principal, int64, bool) {
	p, ok := requireStore(w, r, role, configured, unavailable)
	if !ok {
		return auth.Principal{}, 0, false
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return auth.Principal{}, 0, false
	}
	return p, id, true
}

// storeError maps a storage error to the API response: ErrNotFound becomes
// 404 with notFound, anything else is logged as logMsg and answered 500.
func (s *Server) storeError(w http.ResponseWriter, r *http.Request, err error, notFound, logMsg string) {
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, notFound)
		return
	}
	s.log.ErrorContext(r.Context(), logMsg, "err", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}
