package middleware

import (
	"errors"
	"net/http"
)

// BodyLimit rejects requests whose Content-Length exceeds maxBytes and wraps the body with MaxBytesReader.
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if maxBytes > 0 && r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
				if r.ContentLength > maxBytes {
					WriteJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// IsBodyTooLarge reports whether err is from http.MaxBytesReader.
func IsBodyTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}
