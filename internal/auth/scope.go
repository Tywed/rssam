package auth

import (
	"net/http"
	"strings"
)

// API key scopes, ordered: read ⊂ write ⊂ admin.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
	ScopeAdmin = "admin"
)

func IsValidScope(s string) bool {
	switch s {
	case ScopeRead, ScopeWrite, ScopeAdmin:
		return true
	}
	return false
}

// ScopeAllows reports whether a key with the given scope may perform the
// request. read: safe methods only. write: additionally the read/starred
// state of entries (PUT /v1/entries, PUT /v1/feeds/{id}/entries/{id},
// the mark-all-as-read endpoints). admin: everything the user may do.
func ScopeAllows(scope, method, path string) bool {
	switch scope {
	case ScopeAdmin, "":
		return true
	case ScopeRead:
		return isSafeMethod(method)
	case ScopeWrite:
		if isSafeMethod(method) {
			return true
		}
		if method != http.MethodPut {
			return false
		}
		switch {
		case path == "/v1/entries":
			return true
		case strings.HasPrefix(path, "/v1/feeds/") && strings.Contains(path[len("/v1/feeds/"):], "/entries/"):
			return true
		case strings.HasSuffix(path, "/mark-all-as-read") &&
			(strings.HasPrefix(path, "/v1/feeds/") || strings.HasPrefix(path, "/v1/categories/")):
			return true
		}
	}
	return false
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}
