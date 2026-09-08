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

func DefaultSessionCookieConfig(secure bool) SessionCookieConfig {
	return SessionCookieConfig{
		Secure:   secure,
		MaxAge:   int((30 * 24 * time.Hour).Seconds()),
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

func SessionIDFromRequest(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err != nil || c == nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}
