package middleware

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
)

// SecurityConfig controls HTTP security response headers.
type SecurityConfig struct {
	HSTSEnabled bool
}

type cspNonceKey struct{}

func isUIRoute(path string) bool {
	return path == "/ui" || strings.HasPrefix(path, "/ui/")
}

// uiCSP allows no inline style/handler attributes at all; the only inline
// <style>/<script> blocks are the ones the layout emits with this nonce.
func uiCSP(nonce string) string {
	return "default-src 'self'; script-src 'self' 'nonce-" + nonce + "'; style-src 'self' 'nonce-" + nonce + "'; img-src 'self' https: data:; connect-src 'self' ws: wss:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
}

const apiCSP = "default-src 'none'; frame-ancestors 'none'"

func newCSPNonce() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// CSPNonce returns the per-request nonce set by SecurityHeaders for UI routes,
// or "" outside the middleware.
func CSPNonce(ctx context.Context) string {
	v, _ := ctx.Value(cspNonceKey{}).(string)
	return v
}

// SecurityHeaders sets standard security headers for JSON API responses.
func SecurityHeaders(cfg SecurityConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "no-referrer")
			if isUIRoute(r.URL.Path) {
				nonce := newCSPNonce()
				w.Header().Set("Content-Security-Policy", uiCSP(nonce))
				r = r.WithContext(context.WithValue(r.Context(), cspNonceKey{}, nonce))
			} else {
				w.Header().Set("Content-Security-Policy", apiCSP)
			}
			w.Header().Set("Permissions-Policy", "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")
			if cfg.HSTSEnabled {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}
