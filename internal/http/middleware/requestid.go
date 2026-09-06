package middleware

import (
	"net/http"

	"rssam/internal/requestid"
)

// RequestID assigns a correlation id to every request. An X-Request-Id sent
// by a trusted reverse proxy is kept (so the id matches the proxy's own
// access log); anything else is replaced by a generated one, because an
// arbitrary client must not be able to forge ids that collide with, or
// pollute, server logs. The id is stored in the request context and echoed
// in the response header. It must be the outermost middleware so the access
// log and error responses can see it.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := ""
		if _, peer, ok := remoteAddr(r); ok && isTrustedProxy(peer) {
			if v := r.Header.Get(requestid.Header); requestid.Valid(v) {
				id = v
			}
		}
		if id == "" {
			id = requestid.Generate()
		}
		w.Header().Set(requestid.Header, id)
		next.ServeHTTP(w, r.WithContext(requestid.NewContext(r.Context(), id)))
	})
}
