package auth

import (
	"net/http"
	"strings"
	"time"
)

const SessionCookieName = "session_id"

type SessionCookieConfig struct {
	Secure   bool
	MaxAge   int
	SameSite http.SameSite
}

func SessionCookieConfigFor(secure bool, maxAge time.Duration) SessionCookieConfig {
	return SessionCookieConfig{
		Secure:   secure,
		MaxAge:   int(maxAge.Seconds()),
		SameSite: http.SameSiteStrictMode,
	}
}

func SetSessionCookie(w http.ResponseWriter, cfg SessionCookieConfig, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   cfg.MaxAge,
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: cfg.SameSite,
	})
}

func ClearSessionCookie(w http.ResponseWriter, cfg SessionCookieConfig) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: cfg.SameSite,
	})
}

// LoginNonceCookieName carries the per-browser nonce the anonymous login
// form's CSRF token is bound to.
const LoginNonceCookieName = "rssam_login"

func SetLoginNonceCookie(w http.ResponseWriter, cfg SessionCookieConfig, nonce string) {
	http.SetCookie(w, &http.Cookie{
		Name:     LoginNonceCookieName,
		Value:    nonce,
		Path:     "/ui/login",
		MaxAge:   int((24 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: cfg.SameSite,
	})
}

func ClearLoginNonceCookie(w http.ResponseWriter, cfg SessionCookieConfig) {
	http.SetCookie(w, &http.Cookie{
		Name:     LoginNonceCookieName,
		Value:    "",
		Path:     "/ui/login",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: cfg.SameSite,
	})
}

func LoginNonceFromRequest(r *http.Request) string {
	c, err := r.Cookie(LoginNonceCookieName)
	if err != nil || c == nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}

func SessionIDFromRequest(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err != nil || c == nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}
