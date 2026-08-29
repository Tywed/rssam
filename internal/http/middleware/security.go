package middleware

import (
	"net/http"
	"strings"
)

// SecurityConfig controls HTTP security response headers.
type SecurityConfig struct {
	HSTSEnabled bool
}

func isUIRoute(path string) bool {
	return path == "/ui" || strings.HasPrefix(path, "/ui/")
}

func cspForPath(path string) string {
	if isUIRoute(path) {
		return "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' https: data:; connect-src 'self' ws: wss:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
	}
	return "default-src 'none'; frame-ancestors 'none'"
}

// SecurityHeaders sets standard security headers for JSON API responses.
func SecurityHeaders(cfg SecurityConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Security-Policy", cspForPath(r.URL.Path))
			w.Header().Set("Permissions-Policy", "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")
			if cfg.HSTSEnabled {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}
